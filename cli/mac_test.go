package cli

import (
	"errors"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeDock stands in for defaults, plutil, cp and killall: a Dock held as a
// slice, edited by plutil -remove, and a record of every call.
type fakeDock struct {
	apps     []dockApp
	calls    []string
	imported []string // labels left when defaults import ran
}

func (f *fakeDock) install(t *testing.T) {
	t.Helper()
	prev := macExec
	macExec = func(name string, args ...string) ([]byte, error) {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		switch {
		case name == "plutil" && args[0] == "-extract":
			parts := strings.Split(args[1], ".")
			i, _ := strconv.Atoi(parts[1])
			if i >= len(f.apps) {
				return nil, errors.New("no value at that key path")
			}
			if parts[len(parts)-1] == "file-label" {
				return []byte(f.apps[i].Label), nil
			}
			return []byte("file://" + f.apps[i].Path + "\n"), nil
		case name == "plutil" && args[0] == "-remove":
			i, _ := strconv.Atoi(strings.TrimPrefix(args[1], "persistent-apps."))
			f.apps = slices.Delete(f.apps, i, i+1)
		case name == "defaults" && args[0] == "import":
			for _, a := range f.apps {
				f.imported = append(f.imported, a.Label)
			}
		}
		return nil, nil
	}
	t.Cleanup(func() { macExec = prev })
}

func newFakeDock() *fakeDock {
	return &fakeDock{apps: []dockApp{
		{Label: "Safari", Path: "/System/Volumes/Preboot/Cryptexes/App/System/Applications/Safari.app/"},
		{Label: "Ghostty", Path: "/Applications/Ghostty.app/"},
		{Label: "Mail", Path: "/System/Applications/Mail.app/"},
		{Label: "Old Safari", Path: "/Applications/Safari.app/"},
		{Label: "Code", Path: "/Applications/Visual%20Studio%20Code.app/"},
	}}
}

func TestDockCleanKeepsWhatWasPinned(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("mac dock-clean refuses to run off macOS")
	}
	t.Setenv("HOME", t.TempDir())
	f := newFakeDock()
	f.install(t)
	if err := macDockClean(nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"Ghostty", "Code"}; !slices.Equal(f.imported, want) {
		t.Errorf("Dock after clean = %v, want %v", f.imported, want)
	}
	if last := f.calls[len(f.calls)-1]; last != "killall Dock" {
		t.Errorf("last call = %q, want killall Dock", last)
	}
	backedUp := slices.IndexFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "cp ") })
	edited := slices.IndexFunc(f.calls, func(c string) bool { return strings.HasPrefix(c, "plutil -remove") })
	if backedUp < 0 || backedUp > edited {
		t.Errorf("the Dock was edited before it was backed up: %v", f.calls)
	}
}

// --show reads the Dock but runs nothing that changes it.
func TestDockCleanShowChangesNothing(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("mac dock-clean refuses to run off macOS")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	f := newFakeDock()
	f.install(t)
	if err := macDockClean([]string{"--show"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "defaults export") && !strings.HasPrefix(c, "plutil -extract") {
			t.Errorf("--show ran %q", c)
		}
	}
	if _, err := os.Stat(home + "/.jat/mac"); err == nil {
		t.Error("--show created the backup directory")
	}
}

func TestIsBundledApp(t *testing.T) {
	for path, want := range map[string]bool{
		"/System/Applications/Mail.app/":                                        true,
		"/System/Applications/Utilities/Terminal.app/":                          true,
		"/System/Volumes/Preboot/Cryptexes/App/System/Applications/Safari.app/": true,
		"/Applications/Safari.app/":                                             true,
		"/Applications/Ghostty.app/":                                            false,
		"/Applications/Safari Technology Preview.app/":                          false,
	} {
		if got := isBundledApp(path); got != want {
			t.Errorf("isBundledApp(%q) = %v, want %v", path, got, want)
		}
	}
}
