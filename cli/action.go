package cli

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// templateAction is one non-file step in a template. Every kind is a jat
// subcommand that also runs on its own — the template only chooses it — and
// each can say whether it is already done, which is what the picker shows.
//
//	{"do": "install",        "tool": "bash"}                 jat install bash
//	{"do": "login-shell",    "tool": "bash"}                 jat shell bash
//	{"do": "mac-dock-clean"}                                 jat mac dock-clean
//	{"do": "mac-default", "domain": "…", "key": "…",
//	                      "type": "string", "value": "…"}   jat mac default …
type templateAction struct {
	Do     string `json:"do"`
	Tool   string `json:"tool,omitempty"`
	Domain string `json:"domain,omitempty"`
	Key    string `json:"key,omitempty"`
	Type   string `json:"type,omitempty"`
	Value  string `json:"value,omitempty"`
	// Note says why, in the picker; optional.
	Note string `json:"note,omitempty"`
	When *when  `json:"when,omitempty"`
}

var actionKinds = []string{"install", "login-shell", "mac-dock-clean", "mac-default"}

func (a templateAction) validate(tbl map[string]Tool) error {
	switch a.Do {
	case "install":
		if _, ok := tbl[a.Tool]; !ok {
			return fmt.Errorf("install %q: jat does not know that tool", a.Tool)
		}
	case "login-shell":
		if a.Tool == "" {
			return errors.New("login-shell needs a tool")
		}
	case "mac-dock-clean":
	case "mac-default":
		if a.Domain == "" || a.Key == "" || a.Value == "" {
			return errors.New("mac-default needs domain, key and value")
		}
		if _, err := defaultsTypeFlag(a.Type); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown action %q (known: %s)", a.Do, strings.Join(actionKinds, ", "))
	}
	return nil
}

// id names the action in the picker and in output; two actions with the same
// id are the same step, so extends can repeat one harmlessly.
func (a templateAction) id() string {
	switch a.Do {
	case "install":
		return "install " + a.Tool
	case "login-shell":
		return "login shell " + a.Tool
	case "mac-dock-clean":
		return "dock-clean"
	case "mac-default":
		return "default " + a.Domain + " " + a.Key
	}
	return a.Do
}

// command is the jat command line that does the same thing on its own.
func (a templateAction) command() string {
	switch a.Do {
	case "install":
		return "jat install " + a.Tool
	case "login-shell":
		return "jat shell " + a.Tool
	case "mac-dock-clean":
		return "jat mac dock-clean"
	case "mac-default":
		t, _ := defaultsTypeFlag(a.Type)
		return fmt.Sprintf("jat mac default %s %q %s %q", a.Domain, a.Key, t, a.Value)
	}
	return ""
}

func (a templateAction) done(tbl map[string]Tool, profile string) (bool, error) {
	switch a.Do {
	case "install":
		return installed(tbl, a.Tool), nil
	case "login-shell":
		want, err := loginShellPath(tbl, a.Tool)
		if err != nil {
			return false, nil // not installed yet: an install earlier in the template may fix that
		}
		cur, err := currentLoginShell()
		return cur == want, err
	case "mac-dock-clean":
		if runtime.GOOS != "darwin" {
			return false, errors.New("macOS only")
		}
		n, err := dockBundledCount()
		return n == 0, err
	case "mac-default":
		return macDefaultIs(a.Domain, a.Key, a.Type, a.Value)
	}
	return false, nil
}

func (a templateAction) run(tbl map[string]Tool, profile string, dry bool) error {
	switch a.Do {
	case "install":
		steps, err := buildPlan(tbl, []string{a.Tool}, profile)
		if err != nil {
			return err
		}
		if dry {
			printPlan(steps, profile)
			return nil
		}
		return runPlan(tbl, steps, profile)
	case "login-shell":
		return setLoginShell(tbl, a.Tool, dry)
	case "mac-dock-clean":
		if dry {
			return macDockClean([]string{"--show"})
		}
		return macDockClean(nil)
	case "mac-default":
		return macDefaultWrite(a.Domain, a.Key, a.Type, a.Value, dry)
	}
	return fmt.Errorf("unknown action %q", a.Do)
}

func actionRowID(a templateAction) string { return "do:" + a.id() }

func actionRow(a templateAction, done bool, checkErr error, tbl map[string]Tool, profile string) PickRow {
	note, short, colour := "to do — "+a.command(), "to do", "10"
	switch {
	case checkErr != nil:
		note, short, colour = "could not check: "+checkErr.Error(), "unchecked", "11"
	case done:
		note, short, colour = "done", "done", ""
	}
	if a.Note != "" && !done {
		note += " · " + a.Note
	}
	return PickRow{
		ID: actionRowID(a), Label: a.id(), Note: note, ShortNote: short, NoteColor: colour,
		Selected: !done && checkErr == nil,
		Preview: func() Preview {
			p := Preview{Title: a.id()}
			p.Lines = append(p.Lines, head("runs"), pl(' ', a.command()))
			if a.Do == "install" {
				if _, cmd, err := resolve(tbl[a.Tool], profile, ""); err == nil {
					p.Lines = append(p.Lines, dim(""), head("which is"), pl(' ', cmd))
				}
			}
			if a.Note != "" {
				p.Lines = append(p.Lines, dim(""), dim(a.Note))
			}
			return p
		},
	}
}
