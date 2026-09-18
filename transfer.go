package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Everything a bundle carries lives under this prefix, which keeps the
// manifest from colliding with a real dotfile and gives receive one thing to
// check every entry against.
const bundlePrefix = "home/"

type Manifest struct {
	Created time.Time `json:"created"`
	Host    string    `json:"host"`
	Profile string    `json:"profile"`
	// Key disambiguates migrations in flight; it is an identifier, never a
	// cryptographic key. User is the OS account, so receive can show who and
	// where a bundle came from before you open it.
	Key      string   `json:"key,omitempty"`
	User     string   `json:"user,omitempty"`
	Captured []string `json:"captured"`
	Skipped  []string `json:"skipped"`
}

// expandPaths resolves a config item's patterns to paths that exist under
// home, relative to it. A pattern may be a literal or a glob.
func expandPaths(home string, patterns []string) []string {
	var out []string
	for _, pat := range patterns {
		if !strings.ContainsAny(pat, "*?[") {
			if _, err := os.Lstat(filepath.Join(home, pat)); err == nil {
				out = append(out, pat)
			}
			continue
		}
		matches, err := filepath.Glob(filepath.Join(home, pat))
		if err != nil {
			continue
		}
		for _, m := range matches {
			out = append(out, relOf(home, m))
		}
	}
	return out
}

// writeBundle tars the named config items into dest and appends the manifest.
// Every transport sends this same bundle, so there is exactly one format.
func writeBundle(dest, home string, names []string, man *Manifest) error {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("could not create %s (it may already exist): %w", dest, err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, name := range names {
		paths := expandPaths(home, configs[name].Paths)
		if len(paths) == 0 {
			man.Skipped = append(man.Skipped, name+" (not present on this machine)")
			continue
		}
		for _, rel := range paths {
			if err := addPath(tw, home, rel, man); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
		}
		man.Captured = append(man.Captured, name)
	}
	return writeManifest(tw, *man)
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
		// A backup receive left behind is this machine's history, not config:
		// .gitconfig-personal.pre-jat-7f3a matches the git glob, and one inside
		// ~/.config/karabiner would ride along with the directory.
		if isBackupName(p) {
			man.Skipped = append(man.Skipped, relOf(home, p)+" (a jat backup)")
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
