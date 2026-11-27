package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// `jat shell <tool>` makes a shell the login shell: it must be one jat counts
// as installed (so never macOS's bash 3.2 when bash means 5+), listed in
// /etc/shells, and then chsh'd to. Both steps prompt for a password, so they
// run with the terminal attached; --show prints them instead.

func cmdShell(args []string) error {
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	show := fs.Bool("show", false, "print the commands, change nothing")
	fs.Parse(flagsFirst(fs, args))
	if fs.NArg() != 1 {
		return errors.New("usage: jat shell <tool> [--show]   e.g. jat shell bash")
	}
	cfg, _ := loadConfig()
	return setLoginShell(effectiveTools(cfg), fs.Arg(0), *show)
}

// Swapped in tests so they never read the real account or change it.
var (
	etcShells  = "/etc/shells"
	shellQuery = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).Output()
	}
	shellRun = func(name string, args ...string) error {
		c := exec.Command(name, args...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	}
)

// loginShellPath is where the tool's executable is, as PATH finds it — not
// symlink-resolved: Homebrew's /opt/homebrew/bin/bash survives upgrades, the
// Cellar path it points at does not.
func loginShellPath(tbl map[string]Tool, tool string) (string, error) {
	bin := tool
	if t, ok := tbl[tool]; ok {
		if !installed(tbl, tool) {
			return "", fmt.Errorf("%s is not installed (or not the version jat wants) — run: jat install %s", tool, tool)
		}
		if t.Bin != "" {
			bin = t.Bin
		}
	}
	p, err := lookPath(bin)
	if err != nil {
		return "", fmt.Errorf("%s is not on PATH", bin)
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s resolved to %s, not an absolute path", bin, p)
	}
	return p, nil
}

func currentLoginShell() (string, error) {
	user := osUsername()
	switch runtime.GOOS {
	case "darwin":
		out, err := shellQuery("dscl", ".", "-read", "/Users/"+user, "UserShell")
		if err != nil {
			return "", fmt.Errorf("dscl: %w", err)
		}
		return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "UserShell:")), nil
	default:
		out, err := shellQuery("getent", "passwd", user)
		if err != nil {
			return "", fmt.Errorf("getent: %w", err)
		}
		f := strings.Split(strings.TrimSpace(string(out)), ":")
		if len(f) < 7 {
			return "", fmt.Errorf("unexpected passwd entry for %s", user)
		}
		return f[6], nil
	}
}

func inEtcShells(p string) bool {
	f, err := os.Open(etcShells)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == p {
			return true
		}
	}
	return false
}

func setLoginShell(tbl map[string]Tool, tool string, show bool) error {
	p, err := loginShellPath(tbl, tool)
	if err != nil {
		if show {
			fmt.Printf("  %s — after that, would add it to %s and chsh to it\n", err, etcShells)
			return nil
		}
		return err
	}
	// Carried even when nothing else needs doing, so re-running `jat shell`
	// repairs a machine whose PATH line was left behind in the old shell.
	if err := carryJatPath(p, show); err != nil {
		return err
	}
	if cur, err := currentLoginShell(); err == nil && cur == p {
		fmt.Printf("login shell is already %s\n", p)
		return nil
	}
	var cmds [][]string
	if !inEtcShells(p) {
		cmds = append(cmds, []string{"sudo", "sh", "-c", `printf '%s\n' "$1" >> ` + etcShells, "sh", p})
	}
	cmds = append(cmds, []string{"chsh", "-s", p})
	for _, c := range cmds {
		fmt.Printf("  %s %s\n", map[bool]string{true: "would run", false: "→"}[show], shellLine(c))
		if show {
			continue
		}
		if err := shellRun(c[0], c[1:]...); err != nil {
			return fmt.Errorf("%s: %w", c[0], err)
		}
	}
	if !show {
		fmt.Printf("login shell is now %s — it takes effect in new terminal windows\n", p)
	}
	return nil
}

func shellLine(c []string) string {
	out := make([]string, len(c))
	for i, a := range c {
		if strings.ContainsAny(a, ` '"$>`) {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// jatDir is the directory this jat binary runs from; a variable for tests.
var jatDir = func() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return ""
	}
	return filepath.Dir(self)
}

// carryJatPath gives the new shell the PATH line jat's self-install gave the
// old one. Self-install writes it only to the startup file of the shell in use
// at the time (~/.zshrc on a stock Mac), so without this, switching shells
// with jat makes jat — and anything else in ~/.local/bin — vanish. Only a
// directory under $HOME needs it; system directories are on every PATH.
func carryJatPath(shell string, show bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := jatDir()
	if dir == "" || !strings.HasPrefix(dir, home+string(filepath.Separator)) {
		return nil
	}
	rc := rcFileFor(home, shell)
	line := pathLine(dir, rc)
	if b, err := os.ReadFile(rc); err == nil && strings.Contains(string(b), line) {
		return nil
	}
	if show {
		fmt.Printf("  would add to %s: %s\n", rc, line)
		return nil
	}
	if err := appendLine(rc, line); err != nil {
		return err
	}
	fmt.Printf("  added jat's PATH line to %s, so %s finds jat too\n", rc, filepath.Base(shell))
	return nil
}
