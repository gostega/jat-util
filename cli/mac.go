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
		return errors.New("usage: jat mac dock-clean [--show]")
	}
	switch args[0] {
	case "dock-clean":
		return macDockClean(args[1:])
	}
	return fmt.Errorf("unknown mac command %q (known: dock-clean)", args[0])
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
