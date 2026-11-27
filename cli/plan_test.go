package cli

import (
	"errors"
	"testing"
)

// A Check decides "installed" once Bin is found: bash on PATH is not enough
// when it is macOS's 3.2.
func TestInstalledHonoursCheck(t *testing.T) {
	prevLook, prevRun := lookPath, runCheck
	t.Cleanup(func() { lookPath, runCheck = prevLook, prevRun })
	tbl := map[string]Tool{"bash": {Bin: "bash", Check: []string{"bash", "-c", "true"}}}

	found := true
	lookPath = func(string) (string, error) {
		if found {
			return "/bin/bash", nil
		}
		return "", errors.New("not found")
	}
	ran := 0
	pass := false
	runCheck = func([]string) bool { ran++; return pass }

	if installed(tbl, "bash") {
		t.Error("bash with a failing Check counted as installed")
	}
	pass = true
	if !installed(tbl, "bash") {
		t.Error("bash with a passing Check counted as missing")
	}
	found, ran = false, 0
	if installed(tbl, "bash") || ran != 0 {
		t.Errorf("missing bash: installed or Check ran (%d times)", ran)
	}
}
