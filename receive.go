package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
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

// What a bundled file would do to this machine. Worked out by hashing against
// disk every time, never by marking the bundle: one bundle may feed two
// machines, and the first to finish must not mark it done for the second.
const (
	stateAbsent    = "absent"
	stateIdentical = "identical"
	stateDiffers   = "differs"
)

// Files in a bundle that match no config item this jat knows. A bundle is
// untrusted input and may come from a newer jat, so these are shown but never
// ticked by default.
const unlistedItem = "unlisted"

type bundleFile struct {
	Rel   string
	Size  int64
	State string
}

type bundleItem struct {
	Name   string
	Desc   string
	Secret bool
	Files  []*bundleFile
}

// State folds the files' states into the one that matters most: anything that
// would overwrite a local change, then anything new, then nothing to do.
func (it *bundleItem) State() string {
	state := stateIdentical
	for _, f := range it.Files {
		switch f.State {
		case stateDiffers:
			return stateDiffers
		case stateAbsent:
			state = stateAbsent
		}
	}
	return state
}

func (it *bundleItem) counts() (changed, added, same int) {
	for _, f := range it.Files {
		switch f.State {
		case stateDiffers:
			changed++
		case stateAbsent:
			added++
		default:
			same++
		}
	}
	return
}

func (it *bundleItem) size() (n int64) {
	for _, f := range it.Files {
		n += f.Size
	}
	return
}

// walkBundle is the one place a bundle is opened. It decodes the manifest,
// skips anything that is not a regular file, and validates every entry name
// before fn sees it — so neither the classifying pass nor the writing pass can
// meet a path that escapes $HOME.
func walkBundle(bundlePath string, fn func(rel string, hdr *tar.Header, r io.Reader) error) (Manifest, error) {
	var man Manifest
	f, err := os.Open(bundlePath)
	if err != nil {
		return man, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return man, fmt.Errorf("%s is not a gzip bundle: %w", bundlePath, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return man, nil
		}
		if err != nil {
			return man, err
		}
		if hdr.Name == "manifest.json" {
			if err := json.NewDecoder(tr).Decode(&man); err != nil {
				return man, fmt.Errorf("unreadable manifest: %w", err)
			}
			continue
		}
		if !hdr.FileInfo().Mode().IsRegular() {
			continue
		}
		rel, err := bundleRel(hdr.Name)
		if err != nil {
			return man, fmt.Errorf("refusing bundle: %w", err)
		}
		if err := fn(rel, hdr, tr); err != nil {
			return man, err
		}
	}
}

// readBundle classifies every file in the bundle against home and groups the
// files into config items. It writes nothing.
func readBundle(bundlePath, home string) (Manifest, []*bundleItem, error) {
	var files []*bundleFile
	seen := map[string]*bundleFile{}
	man, err := walkBundle(bundlePath, func(rel string, hdr *tar.Header, r io.Reader) error {
		h := sha256.New()
		if _, err := io.Copy(h, r); err != nil {
			return err
		}
		bf := &bundleFile{Rel: rel, Size: hdr.Size, State: diskState(filepath.Join(home, rel), h.Sum(nil))}
		// ssh-config and ssh-keys both capture .ssh/config, so a path can
		// appear twice. The later entry is the one extraction leaves behind.
		if prev, ok := seen[rel]; ok {
			*prev = *bf
			return nil
		}
		seen[rel] = bf
		files = append(files, bf)
		return nil
	})
	if err != nil {
		return man, nil, err
	}
	return man, groupFiles(files, man.Captured), nil
}

// diskState follows symlinks on purpose: a ~/.zshrc linked into a dotfiles
// repo with the same content is identical, not a conflict.
func diskState(dest string, sum []byte) string {
	f, err := os.Open(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return stateAbsent
	}
	if err != nil {
		return stateDiffers
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return stateDiffers // a directory where a file should go, or unreadable
	}
	if bytes.Equal(h.Sum(nil), sum) {
		return stateIdentical
	}
	return stateDiffers
}

// groupFiles assigns each file to the config items whose paths cover it. Only
// items the manifest says were captured are candidates, so a bundle holding
// ssh-config does not grow an ssh-keys row just because ~/.ssh covers the same
// file. A bundle with no captured list falls back to every known item.
func groupFiles(files []*bundleFile, captured []string) []*bundleItem {
	var names []string
	for _, n := range captured {
		if _, ok := configs[n]; ok && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		names = slices.Sorted(maps(configs))
	}

	byName := map[string]*bundleItem{}
	var items []*bundleItem
	add := func(name string, f *bundleFile) {
		it, ok := byName[name]
		if !ok {
			it = &bundleItem{Name: name, Desc: configs[name].Desc, Secret: configs[name].Secret}
			if name == unlistedItem {
				it.Desc = "files this jat does not recognise"
			}
			byName[name] = it
			items = append(items, it)
		}
		it.Files = append(it.Files, f)
	}

	var unlisted []*bundleFile
	for _, f := range files {
		owned := false
		for _, n := range names {
			if coveredBy(f.Rel, configs[n].Paths) {
				add(n, f)
				owned = true
			}
		}
		if !owned {
			unlisted = append(unlisted, f)
		}
	}
	slices.SortStableFunc(items, func(a, b *bundleItem) int {
		return slices.Index(names, a.Name) - slices.Index(names, b.Name)
	})
	for _, f := range unlisted {
		add(unlistedItem, f)
	}
	return items
}

// coveredBy reports whether rel is one of the patterns or sits underneath one.
// Patterns are the same literals and globs configs.go uses on send.
func coveredBy(rel string, patterns []string) bool {
	for _, pat := range patterns {
		for p := rel; p != "." && p != "/"; p = path.Dir(p) {
			if ok, _ := path.Match(pat, p); ok || p == pat {
				return true
			}
		}
	}
	return false
}

func receiveRows(items []*bundleItem) []PickRow {
	rows := make([]PickRow, 0, len(items))
	for _, it := range items {
		state := it.State()
		note := state + " — " + describeCounts(it)
		colour := map[string]string{stateDiffers: "11", stateAbsent: "10"}[state]
		if it.Secret {
			note = "SECRET · " + note
			colour = "9"
		}
		rows = append(rows, PickRow{
			ID: it.Name, Label: it.Name, Note: note, NoteColor: colour,
			Selected: state != stateIdentical && it.Name != unlistedItem,
		})
	}
	return rows
}

func describeCounts(it *bundleItem) string {
	changed, added, same := it.counts()
	var parts []string
	if changed > 0 {
		parts = append(parts, fmt.Sprintf("%d would be overwritten", changed))
	}
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d new", added))
	}
	if same > 0 && len(parts) > 0 {
		parts = append(parts, fmt.Sprintf("%d identical", same))
	} else if same > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s)", same))
	}
	return strings.Join(parts, ", ")
}

// applyBundle writes the wanted paths and nothing else. A file it is about to
// overwrite is copied beside itself first (see cleanup.go). With dry set it
// prints exactly the lines a real run would and touches nothing.
func applyBundle(bundlePath, home, key string, want map[string]string, dry bool, out io.Writer) (int, error) {
	written := 0
	_, err := walkBundle(bundlePath, func(rel string, hdr *tar.Header, r io.Reader) error {
		state, ok := want[rel]
		if !ok {
			return nil
		}
		suffix := ""
		if state == stateDiffers {
			suffix = "  (overwrites; keeps " + path.Base(rel) + backupSuffix(key) + ")"
		}
		fmt.Fprintf(out, "  write  %s%s\n", rel, suffix)
		written++
		if dry {
			return nil
		}

		dest := filepath.Join(home, rel)
		if state == stateDiffers {
			if err := backupBeforeOverwrite(home, rel, key); err != nil {
				return fmt.Errorf("could not back up %s, so it was not overwritten: %w", rel, err)
			}
		}
		mode := hdr.FileInfo().Mode().Perm()
		// The bundle carries no directory entries, so a parent's mode is
		// inferred: a file nobody else may read does not get a world-listable
		// directory made for it (~/.ssh).
		dirMode := os.FileMode(0o755)
		if mode&0o077 == 0 {
			dirMode = 0o700
		}
		if err := os.MkdirAll(filepath.Dir(dest), dirMode); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, r); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	})
	return written, err
}

func migrateReceive(args []string) error {
	fs_ := flag.NewFlagSet("migrate receive", flag.ExitOnError)
	transport := fs_.String("transport", "", "how the bundle arrived: "+strings.Join(transports, ", "))
	all := fs_.Bool("all", false, "skip the picker and apply everything that is not already identical")
	show := fs_.Bool("show", false, "print what would be written without changing anything")
	key := fs_.String("key", "", "for --transport 1password: which migration, when more than one is waiting")
	fs_.Parse(flagsFirst(fs_, args))

	if fs_.NArg() > 1 {
		return fmt.Errorf("usage: jat migrate receive [<bundle.tar.gz> | --transport 1password [--key <key>]] [--all] [--show]")
	}
	// A key only means something to the vault, so it says how the bundle travelled.
	if *key != "" && *transport == "" && fs_.NArg() == 0 {
		*transport = "1password"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// A bundle path on the command line already says how it travelled.
	if *transport == "" && fs_.NArg() == 1 {
		*transport = "file"
	}
	if *transport == "" {
		if !stdinIsTerminal() {
			return fmt.Errorf("usage: jat migrate receive <bundle.tar.gz> [--all] [--show]")
		}
		choice, ok, err := runPickerOne("How did it travel?", "",
			[]PickRow{
				{ID: "file", Label: "File", Note: "a bundle on disk — here, or in ~/Downloads"},
				{ID: "1password", Label: "1Password", Note: "a document in your private vault"},
			})
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "Cancelled.")
			return nil
		}
		*transport = choice
	}
	if !slices.Contains(transports, *transport) {
		return fmt.Errorf("unknown transport %q (want %s)", *transport, strings.Join(transports, " or "))
	}
	bundlePath := fs_.Arg(0)
	if *transport == "1password" {
		if bundlePath != "" {
			return fmt.Errorf("a bundle path and --transport 1password are two different sources — pick one")
		}
		path, cleanup, err := bundleFromVault(*key)
		if err != nil || path == "" {
			return err
		}
		defer cleanup()
		bundlePath = path
	}
	if bundlePath == "" {
		bundlePath, err = chooseBundle([]string{".", filepath.Join(home, "Downloads")})
		if err != nil || bundlePath == "" {
			return err
		}
	}

	man, items, err := readBundle(bundlePath, home)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("%s holds no files", bundlePath)
	}
	header := describeManifest(man)

	rows := receiveRows(items)
	var chosen []string
	switch {
	case *all || *show && !stdinIsTerminal():
		for _, r := range rows {
			if r.Selected {
				chosen = append(chosen, r.ID)
			}
		}
	default:
		// Short enough for 80 columns: the picker truncates, it does not wrap.
		picked, ok, err := runPicker("What should land on this machine?",
			fmt.Sprintf("%s from %s as %s · %s", orUnknown(man.Key), orUnknown(man.Host),
				orUnknown(man.User), man.Created.Format("2 Jan 2006 15:04")), rows)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "Cancelled — nothing written.")
			return nil
		}
		chosen = picked
	}

	fmt.Println(header)
	identical := 0
	for _, it := range items {
		fmt.Printf("  %-10s %-16s %s\n", it.State(), it.Name, describeCounts(it))
		if it.State() == stateIdentical {
			identical++
		}
	}

	// Identical files inside a chosen item are left alone: rewriting them
	// changes nothing but their mtime.
	want := map[string]string{}
	overwrites := false
	for _, it := range items {
		if !slices.Contains(chosen, it.Name) {
			continue
		}
		for _, f := range it.Files {
			if f.State != stateIdentical {
				want[f.Rel] = f.State
				overwrites = overwrites || f.State == stateDiffers
			}
		}
	}
	if len(want) == 0 {
		fmt.Println("nothing to write — everything chosen is already identical")
		return nil
	}

	verb := "wrote"
	if *show {
		verb = "would write"
	}
	n, err := applyBundle(bundlePath, home, man.Key, want, *show, os.Stdout)
	if err != nil {
		return fmt.Errorf("stopped after %d file(s): %w", n, err)
	}
	fmt.Printf("%s %d file(s) across %d item(s); %d item(s) already identical\n", verb, n, len(chosen), identical)
	if !*show && overwrites {
		fmt.Printf("overwritten files are kept as *%s — when you are happy: jat migrate cleanup %s\n",
			backupSuffix(man.Key), backupKey(man.Key))
	}
	return nil
}

// bundleFromVault is the vault transport's whole job on receive: put the same
// bytes on disk that the file transport would have been handed. An empty path
// with no error means the person cancelled.
func bundleFromVault(key string) (string, func(), error) {
	vault, err := configuredVault()
	if err != nil {
		return "", nil, err
	}
	vb, ok, err := pickVaultBundle(vault, key)
	if err != nil || !ok {
		return "", nil, err
	}
	return fetchVaultBundle(vault, vb)
}

func describeManifest(man Manifest) string {
	key := man.Key
	if key == "" {
		key = "(no key)"
	}
	return fmt.Sprintf("bundle %s from %s (%s) as %s, created %s",
		key, orUnknown(man.Host), orUnknown(man.Profile), orUnknown(man.User), man.Created.Format(time.RFC3339))
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// chooseBundle finds migrate bundles in the usual landing places and asks
// which one, showing where each came from — the file transport's answer to
// listing vault documents by tag.
func chooseBundle(dirs []string) (string, error) {
	var rows []PickRow
	for _, dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, "jat-migrate-*.tar.gz"))
		for _, m := range matches {
			man, err := walkBundle(m, func(string, *tar.Header, io.Reader) error { return nil })
			if err != nil {
				continue
			}
			rows = append(rows, PickRow{ID: m, Label: m, Note: describeManifest(man)})
		}
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("no jat-migrate-*.tar.gz in %s — pass the bundle path", strings.Join(dirs, " or "))
	}
	choice, ok, err := runPickerOne("Which bundle?", "", rows)
	if err != nil {
		return "", err
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "Cancelled.")
		return "", nil
	}
	return choice, nil
}
