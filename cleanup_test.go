package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestReceiveKeepsWhatItOverwrites(t *testing.T) {
	home := t.TempDir()
	writeHome(t, home, map[string]string{".gitconfig": "the local original"})
	b := testBundle(t, []string{"git"}, map[string]string{"home/.gitconfig": "incoming", "home/.gitconfig-x": "new"})
	want := map[string]string{".gitconfig": stateDiffers, ".gitconfig-x": stateAbsent}

	// --show keeps nothing, because it overwrites nothing.
	if _, err := applyBundle(b, home, "7f3a", want, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gitconfig.pre-jat-7f3a")); err == nil {
		t.Fatal("--show made a backup")
	}

	if _, err := applyBundle(b, home, "7f3a", want, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".gitconfig.pre-jat-7f3a")); string(got) != "the local original" {
		t.Errorf("backup holds %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".gitconfig-x.pre-jat-7f3a")); err == nil {
		t.Error("a new file got a backup; there was nothing to keep")
	}

	// A second receive of the same migration must not replace the true
	// original with the first run's result.
	os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("edited after first receive"), 0o644)
	if _, err := applyBundle(b, home, "7f3a", want, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".gitconfig.pre-jat-7f3a")); string(got) != "the local original" {
		t.Errorf("second receive clobbered the backup: %q", got)
	}

	backups, err := recordedBackups(home, "7f3a")
	if err != nil || len(backups) != 1 || backups[0] != ".gitconfig.pre-jat-7f3a" {
		t.Errorf("recorded %v, %v", backups, err)
	}
}

// The log is a convenience, not an authority: a line only becomes a deletion
// if it carries this key's suffix and stays inside $HOME.
func TestCleanupOnlyDeletesItsOwnBackups(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	writeHome(t, home, map[string]string{
		".zshrc":                  "precious",
		".zshrc.pre-jat-7f3a":     "backup",
		".zshrc.pre-jat-0000":     "another migration's backup",
		".jat/backups/7f3a":       ".zshrc.pre-jat-7f3a\n.zshrc\n.zshrc.pre-jat-0000\n../outside.pre-jat-7f3a\n.gone.pre-jat-7f3a\n",
		"../outside.pre-jat-7f3a": "outside home",
	})

	backups, err := recordedBackups(home, "7f3a")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || backups[0] != ".zshrc.pre-jat-7f3a" {
		t.Fatalf("would delete %v, want only this key's backup", backups)
	}

	t.Setenv("HOME", home)
	if err := migrateCleanup([]string{"7f3a", "--show"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".zshrc.pre-jat-7f3a")); err != nil {
		t.Fatal("--show deleted a backup")
	}
	// No terminal and no --yes: refuse rather than guess.
	if err := migrateCleanup([]string{"7f3a"}); err == nil {
		t.Error("cleanup deleted without a terminal or --yes")
	}
	if err := migrateCleanup([]string{"7f3a", "--yes"}); err != nil {
		t.Fatal(err)
	}
	for rel, shouldExist := range map[string]bool{
		".zshrc": true, ".zshrc.pre-jat-0000": true, "../outside.pre-jat-7f3a": true, ".zshrc.pre-jat-7f3a": false,
	} {
		_, err := os.Stat(filepath.Join(home, rel))
		if (err == nil) != shouldExist {
			t.Errorf("%s exists=%v, want %v", rel, err == nil, shouldExist)
		}
	}
	for _, bad := range []string{"../x", "7F3A", "*"} {
		if err := migrateCleanup([]string{bad, "--yes"}); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
}

func TestSendLeavesBackupsBehind(t *testing.T) {
	home := t.TempDir()
	writeHome(t, home, map[string]string{
		".gitconfig":                           "a",
		".gitconfig-personal":                  "b",
		".gitconfig-personal.pre-jat-7f3a":     "old",
		".config/ghostty/config":               "c",
		".config/ghostty/config.pre-jat-nokey": "old",
	})
	dest := filepath.Join(t.TempDir(), "b.tar.gz")
	man := Manifest{Key: "0001"}
	if err := writeBundle(dest, home, []string{"git", "ghostty"}, &man); err != nil {
		t.Fatal(err)
	}
	_, items, err := readBundle(dest, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, it := range items {
		for _, f := range it.Files {
			n++
			if isBackupName(f.Rel) {
				t.Errorf("bundle carries a backup: %s", f.Rel)
			}
		}
	}
	if n != 3 {
		t.Errorf("bundle holds %d files, want the 3 real ones", n)
	}
}
