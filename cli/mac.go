package cli

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// `jat mac` is macOS housekeeping for a fresh machine. Each subcommand is one
// change to system settings, with --show printing exactly what it would run.

func cmdMac(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jat mac dock-clean [--show] | jat mac default <domain> <key> -string|-bool|-int <value> [--show]")
	}
	switch args[0] {
	case "dock-clean":
		return macDockClean(args[1:])
	case "default":
		return macDefault(args[1:])
	}
	return fmt.Errorf("unknown mac command %q (known: dock-clean, default)", args[0])
}

// macDefault is `defaults write` with a dry run and a no-op when the value is
// already set: jat mac default <domain> <key> -string|-bool|-int <value>.
func macDefault(args []string) error {
	fs := flag.NewFlagSet("mac default", flag.ExitOnError)
	show := fs.Bool("show", false, "print the command, change nothing")
	var rest []string
	for _, a := range args { // -string/-bool/-int are positional, not flags
		if a == "--show" || a == "-show" {
			*show = true
			continue
		}
		rest = append(rest, a)
	}
	fs.Parse(nil)
	if len(rest) != 4 {
		return errors.New("usage: jat mac default <domain> <key> -string|-bool|-int <value> [--show]")
	}
	return macDefaultWrite(rest[0], rest[1], strings.TrimPrefix(rest[2], "-"), rest[3], *show)
}

// defaultsTypeFlag maps a template's type to defaults' own flag.
func defaultsTypeFlag(t string) (string, error) {
	switch t {
	case "", "string":
		return "-string", nil
	case "bool":
		return "-bool", nil
	case "int":
		return "-int", nil
	}
	return "", fmt.Errorf("mac-default type %q (want string, bool or int)", t)
}

// macDefaultIs compares what `defaults read` prints. Booleans read back as
// 1/0, so the wanted value is spelled the same way before comparing.
func macDefaultIs(domain, key, typ, value string) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, errors.New("macOS only")
	}
	out, err := macExec("defaults", "read", domain, key)
	if err != nil {
		return false, nil // unset
	}
	got := strings.TrimSpace(string(out))
	if typ == "bool" {
		if v, ok := map[string]string{"true": "1", "yes": "1", "false": "0", "no": "0"}[strings.ToLower(value)]; ok {
			value = v
		}
	}
	return got == value, nil
}

func macDefaultWrite(domain, key, typ, value string, show bool) error {
	if runtime.GOOS != "darwin" {
		return errors.New("mac default only runs on macOS")
	}
	t, err := defaultsTypeFlag(typ)
	if err != nil {
		return err
	}
	if ok, _ := macDefaultIs(domain, key, typ, value); ok {
		fmt.Printf("  %s %s is already %s\n", domain, key, value)
		return nil
	}
	line := fmt.Sprintf("defaults write %s %q %s %q", domain, key, t, value)
	if show {
		fmt.Println("  would run " + line)
		return nil
	}
	fmt.Println("  → " + line)
	if _, err := macExec("defaults", "write", domain, key, t, value); err != nil {
		return fmt.Errorf("defaults write: %w", err)
	}
	return nil
}

// dockBundledCount is how many of Apple's bundled apps are pinned in the
// Dock: zero means dock-clean has nothing to do. Reads only.
func dockBundledCount() (int, error) {
	tmp, err := os.MkdirTemp("", "jat-dock-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	work := filepath.Join(tmp, "dock.plist")
	if _, err := macExec("defaults", "export", "com.apple.dock", work); err != nil {
		return 0, fmt.Errorf("defaults export com.apple.dock: %w", err)
	}
	apps, err := dockApps(work)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range apps {
		if isBundledApp(a.Path) {
			n++
		}
	}
	return n, nil
}

// macExec runs one command and returns its stdout; swapped out in tests so
// they never touch the real Dock.
var macExec = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// Apple's bundled apps live on the sealed system volume; Safari moved into a
// cryptex in macOS 13, and sat in /Applications before that. Anything else in
// the Dock was pinned by someone and stays.
var bundledAppDirs = []string{
	"/System/Applications/",
	"/System/Volumes/Preboot/Cryptexes/App/System/Applications/",
}

func isBundledApp(path string) bool {
	if path == "/Applications/Safari.app/" {
		return true
	}
	for _, d := range bundledAppDirs {
		if strings.HasPrefix(path, d) {
			return true
		}
	}
	return false
}

type dockApp struct {
	Index int
	Label string
	Path  string
}

// dockApps reads the pinned apps out of an exported Dock plist. plutil has no
// "length" query, so it walks indices until one is missing.
func dockApps(plist string) ([]dockApp, error) {
	var apps []dockApp
	for i := 0; ; i++ {
		key := "persistent-apps." + strconv.Itoa(i) + ".tile-data"
		raw, err := macExec("plutil", "-extract", key+".file-data._CFURLString", "raw", "-o", "-", plist)
		if err != nil {
			return apps, nil
		}
		label, _ := macExec("plutil", "-extract", key+".file-label", "raw", "-o", "-", plist)
		u, err := url.Parse(strings.TrimSpace(string(raw)))
		if err != nil {
			return nil, fmt.Errorf("dock entry %d: %w", i, err)
		}
		apps = append(apps, dockApp{Index: i, Label: strings.TrimSpace(string(label)), Path: u.Path})
	}
}

func macDockClean(args []string) error {
	fs := flag.NewFlagSet("mac dock-clean", flag.ExitOnError)
	show := fs.Bool("show", false, "list what would be removed and the commands, change nothing")
	fs.Parse(args)
	if runtime.GOOS != "darwin" {
		return errors.New("mac dock-clean only runs on macOS")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// The export is both the working copy and the backup: a pristine copy is
	// kept before the working copy is edited.
	tmp, err := os.MkdirTemp("", "jat-dock-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	work := filepath.Join(tmp, "dock.plist")
	dir := filepath.Join(home, ".jat", "mac")
	backup := filepath.Join(dir, "dock-"+time.Now().Format("20060102-150405")+".plist")

	if _, err := macExec("defaults", "export", "com.apple.dock", work); err != nil {
		return fmt.Errorf("defaults export com.apple.dock: %w", err)
	}
	apps, err := dockApps(work)
	if err != nil {
		return err
	}

	var remove []dockApp
	for _, a := range apps {
		if isBundledApp(a.Path) {
			remove = append(remove, a)
		}
	}
	if len(remove) == 0 {
		fmt.Println("no bundled apps in the Dock; nothing to do")
		return nil
	}

	// Highest index first, so earlier removals don't shift later ones.
	var cmds [][]string
	for i := len(remove) - 1; i >= 0; i-- {
		cmds = append(cmds, []string{"plutil", "-remove", "persistent-apps." + strconv.Itoa(remove[i].Index), work})
	}
	cmds = append(cmds, []string{"defaults", "import", "com.apple.dock", work}, []string{"killall", "Dock"})

	for _, a := range remove {
		fmt.Printf("remove  %s\n", a.Label)
	}
	for _, a := range apps {
		if !isBundledApp(a.Path) {
			fmt.Printf("keep    %s\n", a.Label)
		}
	}

	if *show {
		fmt.Printf("\nwould back up the Dock to %s, then run:\n", backup)
		for _, c := range cmds {
			fmt.Println("  " + strings.Join(c, " "))
		}
		return nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if _, err := macExec("cp", work, backup); err != nil {
		return fmt.Errorf("backing up the Dock: %w", err)
	}
	for _, c := range cmds {
		if _, err := macExec(c[0], c[1:]...); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(c, " "), err)
		}
	}
	fmt.Printf("\nremoved %d app(s). To undo:\n  defaults import com.apple.dock %s && killall Dock\n", len(remove), backup)
	return nil
}
