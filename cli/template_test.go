package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTpl lays out a template under home/.jat/templates/<name>.
func writeTpl(t *testing.T, home, name, spec string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(userTemplateDir(home), name)
	for rel, body := range files {
		p := filepath.Join(dir, "home", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "template.json"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
}

func noApps(t *testing.T, have ...string) {
	t.Helper()
	prev := appInstalled
	appInstalled = func(app string) bool { return slices.Contains(have, app) }
	t.Cleanup(func() { appInstalled = prev })
}

func fileRels(lt *loadedTemplate) map[string]string {
	out := map[string]string{}
	for _, f := range lt.Files {
		out[f.Rel] = string(f.Data)
	}
	return out
}

func TestTemplateExtendsAndOverrides(t *testing.T) {
	home := t.TempDir()
	noApps(t)
	writeTpl(t, home, "base", `{"description":"base","actions":[{"do":"install","tool":"bash"}]}`,
		map[string]string{".bashrc": "base\n", ".zshrc": "base zsh\n"})
	writeTpl(t, home, "top", `{"description":"top","extends":["base"],"actions":[{"do":"install","tool":"bash"},{"do":"install","tool":"tree"}]}`,
		map[string]string{".bashrc": "top\n"})

	lt, err := loadTemplate("top", home, "macos", tools)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(lt.Chain, ","); got != "base,top" {
		t.Errorf("chain = %s, want base,top", got)
	}
	files := fileRels(lt)
	if files[".bashrc"] != "top\n" || files[".zshrc"] != "base zsh\n" {
		t.Errorf("the extending template's file must win, the base's others stay: %v", files)
	}
	var ids []string
	for _, a := range lt.Actions {
		ids = append(ids, a.id())
	}
	if strings.Join(ids, ",") != "install bash,install tree" {
		t.Errorf("actions = %v: base first, a repeat applied once", ids)
	}
	if lt.Name != "top" || lt.Desc != "top" {
		t.Errorf("name/desc should be the top template's: %q %q", lt.Name, lt.Desc)
	}
}

// The condition is the whole "do nothing if iTerm2 isn't there" rule.
func TestTemplateWhenSkipsAndSaysWhy(t *testing.T) {
	home := t.TempDir()
	rel := "Library/Application Support/iTerm2/DynamicProfiles/p.json"
	writeTpl(t, home, "term", `{"description":"t","when":{"iterm2":{"app":"iTerm"}},
		"actions":[{"do":"mac-default","domain":"d","key":"k","value":"v","when":{"app":"iTerm"}}]}`,
		map[string]string{rel: "{}", ".zshrc": "z\n"})

	noApps(t) // no iTerm
	lt, err := loadTemplate("term", home, "macos", tools)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fileRels(lt)[rel]; ok || len(lt.Actions) != 0 {
		t.Errorf("without iTerm, its profile and default must be skipped: files %v actions %v", fileRels(lt), lt.Actions)
	}
	if _, ok := fileRels(lt)[".zshrc"]; !ok {
		t.Error("an ungated item must still apply")
	}
	if len(lt.Skipped) != 2 || !strings.Contains(lt.Skipped[0], "iTerm is not installed") {
		t.Errorf("skips must say why: %v", lt.Skipped)
	}

	noApps(t, "iTerm")
	lt, err = loadTemplate("term", home, "macos", tools)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fileRels(lt)[rel]; !ok || len(lt.Actions) != 1 || len(lt.Skipped) != 0 {
		t.Errorf("with iTerm, everything applies: files %v actions %v skipped %v", fileRels(lt), lt.Actions, lt.Skipped)
	}
}

func TestTemplateRefusals(t *testing.T) {
	noApps(t)
	cases := []struct {
		name, spec string
		files      map[string]string
		want       string
	}{
		{"secret", `{"description":"x"}`, map[string]string{".ssh/id_ed25519": "KEY"}, "never carry secrets"},
		{"unlisted", `{"description":"x"}`, map[string]string{"Documents/notes.txt": "x"}, "no config item"},
		{"typo", `{"description":"x","wen":{}}`, nil, "unknown field"},
		{"badaction", `{"description":"x","actions":[{"do":"reboot"}]}`, nil, "unknown action"},
		{"badtool", `{"description":"x","actions":[{"do":"install","tool":"nope"}]}`, nil, "does not know"},
		{"badgate", `{"description":"x","when":{"nope":{"app":"X"}}}`, nil, "not a config item"},
		{"cycle", `{"description":"x","extends":["cycle"]}`, nil, "extends itself"},
		{"profile", `{"description":"x","profiles":["debian"]}`, nil, "is for debian"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			writeTpl(t, home, c.name, c.spec, c.files)
			_, err := loadTemplate(c.name, home, "macos", tools)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want an error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestTemplateLookupOrder(t *testing.T) {
	home := t.TempDir()
	// A user template shadows the built-in of the same name.
	writeTpl(t, home, "ohmybash", `{"description":"mine"}`, nil)
	src, err := findTemplate("ohmybash", home)
	if err != nil || src.Origin == "built-in" {
		t.Errorf("user template should shadow the built-in: %+v %v", src, err)
	}
	// A path is read as a path, wherever it is.
	dir := filepath.Join(home, "elsewhere", "t1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "template.json"), []byte(`{"description":"p"}`), 0o644)
	if src, err := findTemplate(dir, home); err != nil || src.Origin != dir {
		t.Errorf("path lookup: %+v %v", src, err)
	}
	if _, err := findTemplate("../escape", home); err == nil {
		t.Error("a relative path without template.json must not resolve")
	}
	if _, err := findTemplate("nosuch", home); err == nil || !strings.Contains(err.Error(), "known:") {
		t.Errorf("unknown name should list what is known: %v", err)
	}
}

// Every built-in loads where it claims to apply, and refuses where it does
// not — so a broken built-in fails here, not on someone's new machine.
func TestBuiltinTemplatesLoad(t *testing.T) {
	home := t.TempDir()
	noApps(t, "iTerm")
	ts := listTemplates(home)
	if len(ts) < 2 {
		t.Fatalf("want at least the two built-ins, got %v", ts)
	}
	for _, ti := range ts {
		for _, profile := range knownProfiles() {
			src, _ := findTemplate(ti.Name, home)
			spec, err := readSpec(src.FS, "template.json")
			if err != nil {
				t.Fatalf("%s: %v", ti.Name, err)
			}
			_, err = loadTemplate(ti.Name, home, profile, tools)
			applies := len(spec.Profiles) == 0 || slices.Contains(spec.Profiles, profile)
			if applies && err != nil {
				t.Errorf("%s on %s: %v", ti.Name, profile, err)
			}
			if !applies && err == nil {
				t.Errorf("%s on %s should refuse (profiles %v)", ti.Name, profile, spec.Profiles)
			}
		}
		if strings.Contains(strings.ToLower(ti.Name), "james") {
			t.Errorf("built-in %q is personal; built-in names say what, not whose", ti.Name)
		}
	}
}

func TestLoginShellCommands(t *testing.T) {
	prevQ, prevR, prevE, prevL, prevC, prevD := shellQuery, shellRun, etcShells, lookPath, runCheck, jatDir
	t.Cleanup(func() {
		shellQuery, shellRun, etcShells, lookPath, runCheck, jatDir = prevQ, prevR, prevE, prevL, prevC, prevD
	})
	jatDir = func() string { return "/usr/bin" } // never touch the real ~/.bashrc
	lookPath = func(b string) (string, error) { return "/opt/homebrew/bin/" + b, nil }
	runCheck = func([]string) bool { return true }
	shellQuery = func(name string, args ...string) ([]byte, error) {
		if name == "getent" {
			return []byte("u:x:1:1::/home/u:/bin/zsh\n"), nil
		}
		return []byte("UserShell: /bin/zsh\n"), nil
	}
	var ran []string
	shellRun = func(name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}
	etcShells = filepath.Join(t.TempDir(), "shells")
	os.WriteFile(etcShells, []byte("/bin/bash\n/bin/zsh\n"), 0o644)

	if err := setLoginShell(tools, "bash", true); err != nil || len(ran) != 0 {
		t.Fatalf("--show must run nothing: %v %v", err, ran)
	}
	if err := setLoginShell(tools, "bash", false); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 2 || !strings.HasPrefix(ran[0], "sudo sh -c") || !strings.HasSuffix(ran[0], "/opt/homebrew/bin/bash") ||
		ran[1] != "chsh -s /opt/homebrew/bin/bash" {
		t.Errorf("want /etc/shells then chsh, got %q", ran)
	}

	// Already listed: only chsh. Already the login shell: nothing.
	ran = nil
	os.WriteFile(etcShells, []byte("/opt/homebrew/bin/bash\n"), 0o644)
	setLoginShell(tools, "bash", false)
	if len(ran) != 1 || !strings.HasPrefix(ran[0], "chsh") {
		t.Errorf("listed shell should only chsh: %q", ran)
	}
	ran = nil
	shellQuery = func(name string, _ ...string) ([]byte, error) {
		if name == "getent" {
			return []byte("u:x:1:1::/h:/opt/homebrew/bin/bash\n"), nil
		}
		return []byte("UserShell: /opt/homebrew/bin/bash\n"), nil
	}
	setLoginShell(tools, "bash", false)
	if len(ran) != 0 {
		t.Errorf("already the login shell, ran %q", ran)
	}

	// Never chsh to a bash jat does not count as installed (macOS's 3.2).
	runCheck = func([]string) bool { return false }
	if err := setLoginShell(tools, "bash", false); err == nil || !strings.Contains(err.Error(), "jat install bash") {
		t.Errorf("bash 3.2 must be refused with the fix: %v", err)
	}
}

// A template file can be a symlink to the real one elsewhere (a dotfiles
// repo's single copy); a dangling one is an error, not a silent skip.
func TestTemplateFollowsSymlinks(t *testing.T) {
	home := t.TempDir()
	noApps(t)
	real := filepath.Join(t.TempDir(), "gitconfig")
	os.WriteFile(real, []byte("[init]\n  defaultBranch = main\n"), 0o644)
	writeTpl(t, home, "linked", `{"description":"l"}`, nil)
	link := filepath.Join(userTemplateDir(home), "linked", "home", ".gitconfig")
	os.MkdirAll(filepath.Dir(link), 0o755)
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	lt, err := loadTemplate("linked", home, "macos", tools)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fileRels(lt)[".gitconfig"], "defaultBranch") {
		t.Errorf("symlinked file not carried: %v", fileRels(lt))
	}
	os.Remove(real)
	if _, err := loadTemplate("linked", home, "macos", tools); err == nil {
		t.Error("a dangling symlink must be an error")
	}
}

// Switching shell carries jat's own PATH line along; doing it twice adds it
// once; a jat in a system directory needs nothing.
func TestLoginShellCarriesJatPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	prevQ, prevR, prevE, prevL, prevC, prevD := shellQuery, shellRun, etcShells, lookPath, runCheck, jatDir
	t.Cleanup(func() {
		shellQuery, shellRun, etcShells, lookPath, runCheck, jatDir = prevQ, prevR, prevE, prevL, prevC, prevD
	})
	lookPath = func(b string) (string, error) { return "/opt/homebrew/bin/" + b, nil }
	runCheck = func([]string) bool { return true }
	shellQuery = func(name string, _ ...string) ([]byte, error) {
		if name == "getent" {
			return []byte("u:x:1:1::/h:/opt/homebrew/bin/bash\n"), nil
		}
		return []byte("UserShell: /opt/homebrew/bin/bash\n"), nil
	}
	shellRun = func(string, ...string) error { return nil }
	etcShells = filepath.Join(t.TempDir(), "shells")
	jatDir = func() string { return filepath.Join(home, ".local", "bin") }

	rc := filepath.Join(home, ".bashrc")
	if err := setLoginShell(tools, "bash", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rc); err == nil {
		t.Fatal("--show wrote .bashrc")
	}
	for range 2 {
		if err := setLoginShell(tools, "bash", false); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(rc)
	if n := strings.Count(string(b), `export PATH="$HOME/.local/bin:$PATH"`); n != 1 {
		t.Errorf("want the PATH line once in .bashrc, got %d:\n%s", n, b)
	}

	os.Remove(rc)
	jatDir = func() string { return "/usr/local/bin" }
	setLoginShell(tools, "bash", false)
	if _, err := os.Stat(rc); err == nil {
		t.Error("a jat in /usr/local/bin needs no PATH line")
	}
}
