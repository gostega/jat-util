// Package test drives the built jat binary the way a person does: real
// arguments, real files in a scratch $HOME, and — for the menus — a real
// pseudo-terminal. Unit tests of the package internals live beside the code
// in ../cli; these are the checks that only mean something end to end.
package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var jat string // path to the binary built by TestMain

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "jat-blackbox-")
	if err != nil {
		panic(err)
	}
	jat = filepath.Join(dir, "jat")
	build := exec.Command("go", "build", "-o", jat, "..")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("build failed: " + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// run executes jat with HOME pointed at home and stdin closed (no terminal),
// returning combined output. Extra env entries are appended.
func run(t *testing.T, home string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(jat, args...)
	cmd.Env = append([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "TERM=dumb"}, env...)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

// oldHome is a machine with a few known config files on it.
func oldHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeFiles(t, home, map[string]string{
		".gitconfig":             "[user]\n  name = James\n  email = old@example.test\n",
		".gitconfig-personal":    "[user]\n  email = me@example.test\n",
		".zshrc":                 "export A=1\n",
		".config/ghostty/config": "font-family = JetBrains Mono\n",
	})
	return home
}

func TestFileTransportRoundTrip(t *testing.T) {
	old, new := oldHome(t), t.TempDir()
	writeFiles(t, new, map[string]string{".gitconfig": "local edit\n", ".zshrc": "export A=1\n"})
	bundle := filepath.Join(t.TempDir(), "b.tar.gz")

	out, err := run(t, old, nil, "migrate", "send", "--transport", "file", "--all", "--out", bundle)
	if err != nil {
		t.Fatalf("send: %v\n%s", err, out)
	}

	out, err = run(t, new, nil, "migrate", "receive", bundle, "--show")
	if err != nil {
		t.Fatalf("receive --show: %v\n%s", err, out)
	}
	for _, want := range []string{"differs    git", "absent     ghostty", "identical  zsh", "would write", "(overwrites; keeps .gitconfig.pre-jat-"} {
		if !strings.Contains(out, want) {
			t.Errorf("--show output missing %q:\n%s", want, out)
		}
	}
	if readFile(t, filepath.Join(new, ".gitconfig")) != "local edit\n" {
		t.Fatal("--show changed a file")
	}

	out, err = run(t, new, nil, "migrate", "receive", bundle, "--all")
	if err != nil {
		t.Fatalf("receive --all: %v\n%s", err, out)
	}
	if readFile(t, filepath.Join(new, ".gitconfig")) != readFile(t, filepath.Join(old, ".gitconfig")) {
		t.Error(".gitconfig not replaced")
	}
	if readFile(t, filepath.Join(new, ".gitconfig.pre-jat-"+keyFrom(out))) != "local edit\n" {
		t.Error("backup of the overwritten file missing or wrong")
	}
	if readFile(t, filepath.Join(new, ".config/ghostty/config")) == "" {
		t.Error("new directory item not written")
	}

	out, _ = run(t, new, nil, "migrate", "receive", bundle, "--all")
	if !strings.Contains(out, "nothing to write") {
		t.Errorf("second receive should find everything identical:\n%s", out)
	}

	out, err = run(t, new, nil, "migrate", "inspect", bundle, "--files")
	if err != nil || !strings.Contains(out, ".gitconfig-personal") || strings.Contains(out, "me@example.test") {
		t.Errorf("inspect --files should list paths and never contents: %v\n%s", err, out)
	}
}

// keyFrom pulls the migration key out of receive's "pre-jat-<key>" hint.
func keyFrom(out string) string {
	i := strings.Index(out, "pre-jat-")
	if i < 0 {
		return ""
	}
	return out[i+8 : i+12]
}

func TestHostileBundleIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	old := oldHome(t)
	bundle := filepath.Join(t.TempDir(), "b.tar.gz")
	if out, err := run(t, old, nil, "migrate", "send", "--transport", "file", "--all", "--out", bundle); err != nil {
		t.Fatal(out)
	}
	// Splice a traversing entry into the bundle with tar itself.
	dir := t.TempDir()
	if out, err := exec.Command("sh", "-c", "cd "+dir+" && tar xzf "+bundle+" && mkdir -p home/.. && printf evil > evil && tar czf "+bundle+" home/../evil home manifest.json").CombinedOutput(); err != nil {
		t.Skipf("could not build a hostile bundle with this tar: %v %s", err, out)
	}
	root := t.TempDir()
	new := filepath.Join(root, "home")
	os.MkdirAll(new, 0o755)
	out, err := run(t, new, nil, "migrate", "receive", bundle, "--all")
	if err == nil || !strings.Contains(out, "refusing bundle") {
		t.Errorf("hostile bundle accepted: %v\n%s", err, out)
	}
	if entries, _ := os.ReadDir(new); len(entries) != 0 {
		t.Errorf("files written despite refusal: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, "evil")); err == nil {
		t.Error("traversal escaped $HOME")
	}
}

func TestVaultTransportThroughStandInOp(t *testing.T) {
	store := t.TempDir()
	testdata, _ := filepath.Abs("testdata")
	env := []string{"PATH=" + testdata + ":" + os.Getenv("PATH"), "FAKE_OP_STORE=" + store}
	old, new := oldHome(t), t.TempDir()

	if out, err := run(t, old, env, "vault", "set", "--manager", "1password", "Shared"); err == nil || !strings.Contains(out, "not your private vault") {
		t.Fatalf("a shared vault was accepted: %v\n%s", err, out)
	}
	if out, err := run(t, old, env, "vault", "set", "--manager", "1password", "Private"); err != nil {
		t.Fatalf("vault set: %v\n%s", err, out)
	}
	out, err := run(t, old, env, "migrate", "send", "--transport", "vault", "--all")
	if err != nil || !strings.Contains(out, "filed jat/migrate/") {
		t.Fatalf("send: %v\n%s", err, out)
	}
	title := strings.TrimSpace(readFile(t, filepath.Join(store, "title")))
	if !strings.HasPrefix(title, "jat/migrate/") || readFile(t, filepath.Join(store, "tags")) == "" {
		t.Errorf("document filed without jat's marks: %q", title)
	}

	// The receiving machine has the same vault configured.
	writeFiles(t, new, map[string]string{".jat/config.json": readFile(t, filepath.Join(old, ".jat/config.json"))})
	out, err = run(t, new, env, "migrate", "receive", "--transport", "vault", "--all")
	if err != nil {
		t.Fatalf("receive: %v\n%s", err, out)
	}
	if readFile(t, filepath.Join(new, ".zshrc")) != "export A=1\n" {
		t.Error("bundle did not arrive intact through the vault")
	}
	if matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "jat-receive-*")); len(matches) != 0 {
		t.Errorf("temp bundle left behind: %v", matches)
	}

	// cleanup <key> --show lists the vault copy and deletes nothing.
	key := strings.Split(strings.TrimPrefix(title, "jat/migrate/"), "/")[0]
	out, err = run(t, new, env, "migrate", "cleanup", key, "--show")
	if err != nil || !strings.Contains(out, "would delete") || !strings.Contains(out, title) {
		t.Errorf("cleanup --show: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(store, "doc")); err != nil {
		t.Fatal("cleanup --show deleted the vault document")
	}
	if out, err := run(t, new, env, "migrate", "cleanup", key, "--yes"); err != nil {
		t.Fatalf("cleanup --yes: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(store, "doc")); err == nil {
		t.Error("cleanup --yes left the vault document")
	}
}

func TestNoTerminalNeverPrompts(t *testing.T) {
	old := oldHome(t)
	bundle := filepath.Join(t.TempDir(), "b.tar.gz")
	run(t, old, nil, "migrate", "send", "--transport", "file", "--all", "--out", bundle)
	for _, args := range [][]string{
		{"migrate"},
		{"migrate", "send"},
		{"migrate", "receive", bundle},
		{"migrate", "cleanup", "0000"},
	} {
		out, err := run(t, t.TempDir(), nil, args...)
		if err == nil && !strings.Contains(out, "nothing") {
			t.Errorf("%v succeeded without a terminal:\n%s", args, out)
		}
		if strings.Contains(out, "\x1b[?1049h") {
			t.Errorf("%v took the alternate screen without a terminal", args)
		}
	}
}
