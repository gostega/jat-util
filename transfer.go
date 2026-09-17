package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Everything a bundle carries lives under this prefix, which keeps the
// manifest from colliding with a real dotfile and gives import one thing to
// check every entry against.
const bundlePrefix = "home/"

type Manifest struct {
	Created  time.Time `json:"created"`
	Host     string    `json:"host"`
	Profile  string    `json:"profile"`
	Captured []string  `json:"captured"`
	Skipped  []string  `json:"skipped"`
}

func cmdExport(args []string) error {
	fs_ := flag.NewFlagSet("export", flag.ExitOnError)
	out := fs_.String("out", "", "bundle to write (default: jat-export-<host>-<date>.tar.gz)")
	withSecrets := fs_.Bool("include-secrets", false, "also export keys, tokens and credentials")
	show := fs_.Bool("show", false, "list what would be exported without writing anything")
	fs_.Parse(flagsFirst(args))

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	man := Manifest{Created: time.Now(), Host: cfg.Host, Profile: cfg.Profile}
	type entry struct{ name, rel string }
	var pending []entry

	for _, name := range slices.Sorted(maps(configs)) {
		item := configs[name]
		if item.Secret && !*withSecrets {
			man.Skipped = append(man.Skipped, name+" (secret; --include-secrets to add)")
			continue
		}
		found := false
		for _, rel := range item.Paths {
			if _, err := os.Lstat(filepath.Join(home, rel)); err != nil {
				continue
			}
			pending = append(pending, entry{name, rel})
			found = true
		}
		if !found {
			man.Skipped = append(man.Skipped, name+" (not present on this machine)")
		} else {
			man.Captured = append(man.Captured, name)
		}
	}

	if *show {
		fmt.Printf("would export from %s (host %s):\n", home, cfg.Host)
		for _, e := range pending {
			fmt.Printf("  %-18s %s\n", e.name, e.rel)
		}
		fmt.Println("\nskipped:")
		for _, s := range man.Skipped {
			fmt.Printf("  %s\n", s)
		}
		return nil
	}

	dest := *out
	if dest == "" {
		dest = fmt.Sprintf("jat-export-%s-%s.tar.gz", cfg.Host, time.Now().Format("2006-01-02"))
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("could not create %s (it may already exist): %w", dest, err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, e := range pending {
		if err := addPath(tw, home, e.rel, &man); err != nil {
			return fmt.Errorf("%s: %w", e.rel, err)
		}
	}
	if err := writeManifest(tw, man); err != nil {
		return err
	}

	fmt.Printf("wrote %s\n  captured: %s\n", dest, strings.Join(man.Captured, ", "))
	if !*withSecrets {
		fmt.Println("  secrets were excluded — pass --include-secrets if you want them")
	}
	return nil
}

// addPath walks rel (a file or a directory) and writes every regular file
// under it into the bundle.
func addPath(tw *tar.Writer, home, rel string, man *Manifest) error {
	abs := filepath.Join(home, rel)
	return filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// ponytail: regular files only. A symlink's target may sit outside the
		// set we curated, so following one would smuggle in content nobody
		// listed; recreating it as a link can dangle on the far machine.
		if !d.Type().IsRegular() {
			if !d.IsDir() {
				man.Skipped = append(man.Skipped, relOf(home, p)+" (not a regular file)")
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = bundlePrefix + relOf(home, p)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(p)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tw, src)
		return err
	})
}

func relOf(home, p string) string {
	r, err := filepath.Rel(home, p)
	if err != nil {
		return filepath.Base(p)
	}
	return filepath.ToSlash(r)
}

func writeManifest(tw *tar.Writer, man Manifest) error {
	b, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: "manifest.json", Mode: 0o600, Size: int64(len(b)), ModTime: time.Now(),
	}); err != nil {
		return err
	}
	_, err = tw.Write(b)
	return err
}

func cmdImport(args []string) error {
	fs_ := flag.NewFlagSet("import", flag.ExitOnError)
	show := fs_.Bool("show", false, "list what would be written without changing anything")
	force := fs_.Bool("force", false, "overwrite files that already exist")
	fs_.Parse(flagsFirst(args))

	if fs_.NArg() != 1 {
		return fmt.Errorf("usage: jat import <bundle.tar.gz> [--show] [--force]")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	f, err := os.Open(fs_.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s is not a gzip bundle: %w", fs_.Arg(0), err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var written, skipped int
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Name == "manifest.json" {
			var man Manifest
			if json.NewDecoder(tr).Decode(&man) == nil {
				fmt.Printf("bundle from host %q (%s), created %s\n",
					man.Host, man.Profile, man.Created.Format(time.RFC3339))
			}
			continue
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}

		rel, err := bundleRel(hdr.Name)
		if err != nil {
			return fmt.Errorf("refusing bundle: %w", err)
		}
		dest := filepath.Join(home, rel)

		if _, err := os.Lstat(dest); err == nil && !*force {
			fmt.Printf("  skip   %s (exists; --force to overwrite)\n", rel)
			skipped++
			continue
		}
		if *show {
			fmt.Printf("  write  %s\n", rel)
			written++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, hdr.FileInfo().Mode())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
		fmt.Printf("  write  %s\n", rel)
		written++
	}

	verb := "wrote"
	if *show {
		verb = "would write"
	}
	fmt.Printf("%s %d file(s), skipped %d\n", verb, written, skipped)
	return nil
}

// bundleRel validates a tar entry name and returns its path relative to $HOME.
// A bundle is an untrusted input — an entry named "home/../../.ssh/authorized_keys"
// would otherwise write outside the home directory entirely.
func bundleRel(name string) (string, error) {
	if !strings.HasPrefix(name, bundlePrefix) {
		return "", fmt.Errorf("entry %q is outside %s", name, bundlePrefix)
	}
	rel := path.Clean(strings.TrimPrefix(name, bundlePrefix))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return "", fmt.Errorf("entry %q escapes the home directory", name)
	}
	return rel, nil
}
