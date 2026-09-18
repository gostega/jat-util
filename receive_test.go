package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// testBundle writes a bundle by hand rather than through writeBundle, so a
// test can put entries in it that send would never produce.
func testBundle(t *testing.T, captured []string, entries map[string]string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "b.tar.gz")
	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		body := entries[n]
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		io.WriteString(tw, body)
	}
	if err := writeManifest(tw, Manifest{Created: time.Now(), Host: "old", Profile: "macos", Key: "7f3a", User: "u", Captured: captured}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	f.Close()
	return dest
}

func writeHome(t *testing.T, home string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadBundleClassifiesAgainstDisk(t *testing.T) {
	home := t.TempDir()
	writeHome(t, home, map[string]string{
		".gitconfig": "local edit",
		".zshrc":     "same",
		".zprofile":  "same too",
	})
	b := testBundle(t, []string{"git", "zsh", "ghostty"}, map[string]string{
		"home/.gitconfig":             "from the old machine",
		"home/.gitconfig-personal":    "overlay",
		"home/.zshrc":                 "same",
		"home/.zprofile":              "same too",
		"home/.config/ghostty/config": "font = x",
		"home/.mystery":               "from a newer jat",
	})

	man, items, err := readBundle(b, home)
	if err != nil {
		t.Fatal(err)
	}
	if man.Key != "7f3a" {
		t.Errorf("manifest key = %q", man.Key)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.Name] = it.State()
	}
	want := map[string]string{"git": stateDiffers, "zsh": stateIdentical, "ghostty": stateAbsent, unlistedItem: stateAbsent}
	for name, state := range want {
		if got[name] != state {
			t.Errorf("%s is %q, want %q (all: %v)", name, got[name], state, got)
		}
	}
	if items[len(items)-1].Name != unlistedItem {
		t.Errorf("unlisted should come last, got order %v", got)
	}

	// Identical arrives unticked; so does anything this jat does not recognise.
	ticked := map[string]bool{}
	for _, r := range receiveRows(items) {
		ticked[r.ID] = r.Selected
	}
	for name, wantTick := range map[string]bool{"git": true, "ghostty": true, "zsh": false, unlistedItem: false} {
		if ticked[name] != wantTick {
			t.Errorf("%s ticked = %v, want %v", name, ticked[name], wantTick)
		}
	}
}

func TestGroupFilesOnlyUsesCapturedItems(t *testing.T) {
	// ~/.ssh (ssh-keys) covers .ssh/config too. A bundle that captured only
	// ssh-config must not grow a SECRET row for it.
	items := groupFiles([]*bundleFile{{Rel: ".ssh/config"}}, []string{"ssh-config"})
	if len(items) != 1 || items[0].Name != "ssh-config" || items[0].Secret {
		t.Errorf("got %+v", items)
	}
}

func TestDiskStateFollowsSymlinks(t *testing.T) {
	home := t.TempDir()
	writeHome(t, home, map[string]string{"dotfiles/zshrc": "same"})
	if err := os.Symlink(filepath.Join(home, "dotfiles/zshrc"), filepath.Join(home, ".zshrc")); err != nil {
		t.Skip(err)
	}
	b := testBundle(t, []string{"zsh"}, map[string]string{"home/.zshrc": "same"})
	_, items, err := readBundle(b, home)
	if err != nil {
		t.Fatal(err)
	}
	if s := items[0].State(); s != stateIdentical {
		t.Errorf("linked dotfile with the same content is %q, want identical", s)
	}
}

func TestApplyBundleWritesOnlyWhatWasAskedFor(t *testing.T) {
	home := t.TempDir()
	writeHome(t, home, map[string]string{".zshrc": "keep me"})
	b := testBundle(t, []string{"git", "zsh", "ssh-keys"}, map[string]string{
		"home/.gitconfig": "new",
		"home/.zshrc":     "would overwrite",
	})

	// --show first: same lines, nothing on disk.
	n, err := applyBundle(b, home, map[string]string{".gitconfig": stateAbsent}, true, io.Discard)
	if err != nil || n != 1 {
		t.Fatalf("dry run: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gitconfig")); err == nil {
		t.Fatal("--show wrote a file")
	}

	if _, err := applyBundle(b, home, map[string]string{".gitconfig": stateAbsent}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".gitconfig")); string(got) != "new" {
		t.Errorf(".gitconfig = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".zshrc")); string(got) != "keep me" {
		t.Errorf("unticked .zshrc was overwritten: %q", got)
	}
}

func TestApplyBundlePrivateFilesGetPrivateDirs(t *testing.T) {
	home := t.TempDir()
	dest := filepath.Join(t.TempDir(), "b.tar.gz")
	f, _ := os.Create(dest)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "home/.ssh/id_test", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
	io.WriteString(tw, "k")
	tw.Close()
	gz.Close()
	f.Close()

	if _, err := applyBundle(dest, home, map[string]string{".ssh/id_test": stateAbsent}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(home, ".ssh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("~/.ssh created %v, want 0700", info.Mode().Perm())
	}
}

// A bundle is untrusted input. Receive classifies before it writes, so one
// entry that escapes $HOME refuses the whole bundle with nothing written. The
// writing pass validates again on its own rather than trusting the first.
func TestHostileBundleWritesNothing(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	os.MkdirAll(home, 0o755)
	b := testBundle(t, []string{"zsh"}, map[string]string{
		"home/.aaa-honest":                "fine",
		"home/../evil":                    "outside",
		"home/../../../tmp/jat-evil-test": "outside",
	})

	if _, _, err := readBundle(b, home); err == nil {
		t.Error("readBundle accepted a traversing entry")
	}
	want := map[string]string{".aaa-honest": stateAbsent, "../evil": stateAbsent, "evil": stateAbsent}
	if _, err := applyBundle(b, home, want, false, io.Discard); err == nil {
		t.Error("applyBundle accepted a traversing entry")
	}
	for _, p := range []string{filepath.Join(root, "evil"), "/tmp/jat-evil-test"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written outside $HOME", p)
		}
	}
}
