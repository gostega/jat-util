package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// `jat install --self` (or `jat install jat`): the freshly downloaded binary
// puts itself somewhere on PATH and gets out of the way. The README's
// install is then one curl, one chmod, and this — the PATH, rc-file and
// quarantine snags a first install hits on a Mac are handled here rather
// than in a pasted block (James, 2026-09-25).

// selfInstallDirs are tried in order; the first that is on PATH and
// writable wins. None on PATH → the first, with an offer to put it there.
func selfInstallDirs(home string) []string {
	dirs := []string{filepath.Join(home, ".local", "bin")}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("brew", "--prefix").Output(); err == nil {
			dirs = append(dirs, filepath.Join(strings.TrimSpace(string(out)), "bin"))
		}
	}
	return append(dirs, "/usr/local/bin")
}

func onPath(dir string) bool {
	return slices.Contains(filepath.SplitList(os.Getenv("PATH")), dir)
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".jat-probe-")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// rcFile is where a PATH line goes for the shell the person is using.
func rcFile(home string) string {
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish")
	default:
		return filepath.Join(home, ".bashrc")
	}
}

func pathLine(dir, rc string) string {
	if strings.HasSuffix(rc, "config.fish") {
		return "fish_add_path " + dir
	}
	return fmt.Sprintf(`export PATH="%s:$PATH"`, strings.Replace(dir, os.Getenv("HOME"), "$HOME", 1))
}

func selfInstall(show, yes bool) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dirs := selfInstallDirs(home)
	dest := ""
	for _, d := range dirs {
		if onPath(d) && (writable(d) || show) {
			dest = d
			break
		}
	}
	addPath := dest == ""
	if addPath {
		dest = dirs[0]
	}
	target := filepath.Join(dest, "jat")

	if filepath.Dir(self) == dest {
		fmt.Printf("jat is already installed at %s\n", target)
		return nil
	}

	fmt.Printf("install %s → %s\n", self, target)
	if addPath {
		fmt.Printf("  %s is not on your PATH; would add to %s:\n    %s\n", dest, rcFile(home), pathLine(dest, rcFile(home)))
	}
	if show {
		return nil
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if !writable(dest) {
		return fmt.Errorf("%s is not writable — rerun with sudo, or: sudo install -m 0755 %s %s", dest, self, target)
	}
	// Copy into the destination directory, then rename over the target: a
	// running jat there keeps its inode, and the new file appears whole.
	staged, err := os.CreateTemp(dest, ".jat-new-")
	if err != nil {
		return err
	}
	src, err := os.Open(self)
	if err != nil {
		return err
	}
	_, err = io.Copy(staged, src)
	src.Close()
	staged.Close()
	if err != nil {
		os.Remove(staged.Name())
		return err
	}
	if err := os.Chmod(staged.Name(), 0o755); err != nil {
		return err
	}
	if err := os.Rename(staged.Name(), target); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		// A browser- or curl-downloaded binary may carry the quarantine
		// flag; Gatekeeper then refuses it. Not an error if it is absent.
		exec.Command("xattr", "-d", "com.apple.quarantine", target).Run()
	}
	fmt.Printf("installed %s (%s)\n", target, Version)

	if addPath {
		rc := rcFile(home)
		line := pathLine(dest, rc)
		ok := yes
		if !ok && stdinIsTerminal() {
			fmt.Printf("add it to %s? [Y/n] ", rc)
			answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			a := strings.ToLower(strings.TrimSpace(answer))
			ok = a == "" || a == "y" || a == "yes"
		}
		if ok {
			if err := appendLine(rc, line); err != nil {
				return err
			}
			fmt.Printf("added to %s — open a new shell, or: exec %s\n", rc, filepath.Base(os.Getenv("SHELL")))
		} else {
			fmt.Printf("not on PATH until you add it:  %s\n", line)
		}
	}

	// The downloaded copy has done its job. Only removed when it is exactly
	// what the README creates — ./jat in the directory the command was run
	// from — never a binary that merely lives somewhere off PATH.
	if cwd, err := os.Getwd(); err == nil && self == filepath.Join(cwd, "jat") {
		if err := os.Remove(self); err == nil {
			fmt.Printf("removed %s\n", self)
		}
	}

	if _, err := os.Stat(configPath()); errors.Is(err, os.ErrNotExist) {
		fmt.Println()
		return cmdInit(nil)
	}
	return nil
}

func appendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), line) {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n# added by jat install --self\n%s\n", line)
	return err
}
