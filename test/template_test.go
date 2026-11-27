package test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A machine's config saved as a template, then applied to a fresh one: the
// same classify / back up / write path as a bundle, with no bundle file.
func TestTemplateRoundTrip(t *testing.T) {
	from := oldHome(t)
	tpl := filepath.Join(t.TempDir(), "my-mac")

	out, err := run(t, from, nil, "migrate", "send", "--to-template", tpl, "--all", "--show")
	if err != nil {
		t.Fatalf("send --show: %v\n%s", err, out)
	}
	if _, err := os.Stat(tpl); err == nil {
		t.Fatal("--show wrote the template")
	}
	out, err = run(t, from, nil, "migrate", "send", "--to-template", tpl, "--all")
	if err != nil {
		t.Fatalf("send --to-template: %v\n%s", err, out)
	}
	if readFile(t, filepath.Join(tpl, "home", ".zshrc")) != "export A=1\n" {
		t.Fatalf("template lacks .zshrc:\n%s", out)
	}
	if !strings.Contains(readFile(t, filepath.Join(tpl, "template.json")), `"description"`) {
		t.Error("no template.json written for a new template")
	}

	to := t.TempDir()
	writeFiles(t, to, map[string]string{".zshrc": "export LOCAL=1\n"})

	out, err = run(t, to, nil, "migrate", "receive", "--template", tpl, "--all", "--show")
	if err != nil {
		t.Fatalf("receive --show: %v\n%s", err, out)
	}
	if readFile(t, filepath.Join(to, ".zshrc")) != "export LOCAL=1\n" || fileExists(filepath.Join(to, ".gitconfig")) {
		t.Fatal("--show changed files")
	}

	out, err = run(t, to, nil, "migrate", "receive", "--template", tpl, "--all")
	if err != nil {
		t.Fatalf("receive --template: %v\n%s", err, out)
	}
	if readFile(t, filepath.Join(to, ".zshrc")) != "export A=1\n" {
		t.Errorf(".zshrc not applied:\n%s", out)
	}
	if !strings.Contains(readFile(t, filepath.Join(to, ".config/ghostty/config")), "JetBrains") {
		t.Errorf("ghostty config not applied:\n%s", out)
	}
	m := regexp.MustCompile(`\.pre-jat-([0-9a-f]{4})`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no backup suffix in output:\n%s", out)
	}
	if readFile(t, filepath.Join(to, ".zshrc.pre-jat-"+m[1])) != "export LOCAL=1\n" {
		t.Error("the overwritten .zshrc was not kept")
	}

	out, err = run(t, to, nil, "migrate", "receive", "--template", tpl, "--all")
	if err != nil || strings.Contains(out, "write  ") || !strings.Contains(out, "identical") {
		t.Errorf("a second apply should write nothing: %v\n%s", err, out)
	}
}

func TestInitAppliesATemplate(t *testing.T) {
	home := t.TempDir()
	tpl := filepath.Join(t.TempDir(), "base")
	writeFiles(t, tpl, map[string]string{
		"template.json":   `{"description":"test base"}`,
		"home/.gitconfig": "[init]\n  defaultBranch = main\n",
	})
	out, err := run(t, home, nil, "init", "--profile", "macos", "--host", "t", "--template", tpl, "--all")
	if err != nil {
		t.Fatalf("init --template: %v\n%s", err, out)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, ".jat/config.json")), `"host": "t"`) {
		t.Error("init did not save its config")
	}
	if !strings.Contains(readFile(t, filepath.Join(home, ".gitconfig")), "defaultBranch") {
		t.Errorf("template not applied:\n%s", out)
	}

	// A bare init, with no terminal, applies nothing and asks nothing.
	bare := t.TempDir()
	if out, err := run(t, bare, nil, "init", "--profile", "macos"); err != nil || fileExists(filepath.Join(bare, ".gitconfig")) {
		t.Errorf("bare init should only save config: %v\n%s", err, out)
	}
}

func TestTemplatesNeverCarrySecrets(t *testing.T) {
	home := oldHome(t)
	writeFiles(t, home, map[string]string{".ssh/id_ed25519": "PRIVATE"})
	tpl := filepath.Join(t.TempDir(), "t")
	if out, err := run(t, home, nil, "migrate", "send", "--to-template", tpl, "--all", "--include-secrets"); err == nil {
		t.Errorf("--include-secrets with --to-template must refuse:\n%s", out)
	}
	if _, err := run(t, home, nil, "migrate", "send", "--to-template", tpl, "--all"); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(tpl, "home", ".ssh", "id_ed25519")) {
		t.Fatal("a private key reached a template")
	}

	// And a hand-made template holding one is refused before anything lands.
	writeFiles(t, tpl, map[string]string{"home/.ssh/id_ed25519": "PRIVATE"})
	to := t.TempDir()
	out, err := run(t, to, nil, "migrate", "receive", "--template", tpl, "--all")
	if err == nil || !strings.Contains(out, "never carry secrets") {
		t.Errorf("template with a key must be refused: %v\n%s", err, out)
	}
	if entries, _ := os.ReadDir(to); len(entries) != 0 {
		t.Errorf("refused template still wrote: %v", entries)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
