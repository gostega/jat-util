package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// What `jat install` knows about the tool table as a whole: which tool a
// method leans on, whether a tool is already here, and the order a set of
// tools has to be installed in so parents land before children.

// methodNeeds is the tool each install method depends on. A tool's own
// Needs field adds to this; it never replaces it.
var methodNeeds = map[string]string{
	"brew": "homebrew",
	"cask": "homebrew",
	"mise": "mise",
	"npm":  "node",
}

// toolDeps is what name must have before it can install on profile: the
// method's implied tool (only where the method resolves to one that exists
// in the table and is not the tool itself) plus its Needs.
func toolDeps(name, profile string) []string {
	t := tools[name]
	var deps []string
	if method, _, err := resolve(t, profile, ""); err == nil {
		if d, ok := methodNeeds[method]; ok && d != name {
			if _, exists := tools[d]; exists {
				deps = append(deps, d)
			}
		}
	}
	for _, d := range t.Needs {
		if _, exists := tools[d]; exists && d != name && !slices.Contains(deps, d) {
			deps = append(deps, d)
		}
	}
	slices.Sort(deps)
	return deps
}

// installed reports whether a tool is already on this machine. An
// unanswerable check (no Bin, no App, wrong OS) is "not installed", which
// errs towards offering it.
func installed(name string) bool {
	t := tools[name]
	if t.App != "" {
		if runtime.GOOS != "darwin" {
			return false
		}
		home, _ := os.UserHomeDir()
		for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
			if _, err := os.Stat(filepath.Join(dir, t.App+".app")); err == nil {
				return true
			}
		}
		return false
	}
	bin := t.Bin
	if bin == "" {
		bin = name
	}
	_, err := lookPath(bin)
	return err == nil
}

// lookPath is a variable so tests can decide what is "installed".
var lookPath = exec.LookPath

// installOrder returns want plus every dependency it pulls in, parents
// before children, in a stable order. Installed dependencies are included
// (so the plan can say "already installed") but flagged by the caller.
func installOrder(want []string, profile string) ([]string, error) {
	var order []string
	state := map[string]int{} // 0 unseen, 1 visiting, 2 done
	var visit func(n string, path []string) error
	visit = func(n string, path []string) error {
		switch state[n] {
		case 2:
			return nil
		case 1:
			return fmt.Errorf("dependency cycle: %s → %s", strings.Join(path, " → "), n)
		}
		state[n] = 1
		for _, d := range toolDeps(n, profile) {
			if err := visit(d, append(path, n)); err != nil {
				return err
			}
		}
		state[n] = 2
		order = append(order, n)
		return nil
	}
	for _, n := range slices.Sorted(slices.Values(want)) {
		if err := visit(n, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// depTree is the table as a forest for the picker: every tool appears once,
// under its first dependency (alphabetically), roots first. rootsFirst
// keeps package managers at the top where the eye expects them.
type treeRow struct {
	Name  string
	Depth int
	Last  bool // last sibling, for the └─ glyph
}

func depTree(profile string) []treeRow {
	children := map[string][]string{}
	var roots []string
	for _, n := range slices.Sorted(maps(tools)) {
		deps := toolDeps(n, profile)
		if len(deps) == 0 {
			roots = append(roots, n)
			continue
		}
		children[deps[0]] = append(children[deps[0]], n)
	}
	// A cycle would leave a tool with a parent but never reached from a
	// root; installOrder reports cycles, and TestNoDependencyCycles fails
	// the build, so nothing is lost here in practice.
	var out []treeRow
	var walk func(n string, depth int, last bool)
	walk = func(n string, depth int, last bool) {
		out = append(out, treeRow{Name: n, Depth: depth, Last: last})
		kids := children[n]
		for i, k := range kids {
			walk(k, depth+1, i == len(kids)-1)
		}
	}
	for i, r := range roots {
		walk(r, 0, i == len(roots)-1)
	}
	return out
}

// An install plan: what runs, what is skipped, what a person fetches.
type planStep struct {
	Name    string
	Method  string
	Cmd     string
	Docs    string
	Skip    string // non-empty: why it will not run ("already installed")
	Manual  bool
	Because string // "needed by X" for a dependency that was added
}

func buildPlan(chosen []string, profile string) ([]planStep, error) {
	order, err := installOrder(chosen, profile)
	if err != nil {
		return nil, err
	}
	var steps []planStep
	for _, n := range order {
		t := tools[n]
		method, cmd, err := resolve(t, profile, "")
		st := planStep{Name: n, Method: method, Cmd: cmd, Docs: t.Docs, Manual: cmd == ""}
		if err != nil {
			st.Skip = "no install method on " + profile
		}
		if !slices.Contains(chosen, n) {
			for _, c := range order {
				if slices.Contains(toolDeps(c, profile), n) {
					st.Because = "needed by " + c
					break
				}
			}
		}
		if installed(n) {
			st.Skip = "already installed"
		}
		if st.Skip != "" {
			st.Manual = false
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// printPlan is the dry run: exactly what runPlan would do, and nothing.
func printPlan(steps []planStep, profile string) {
	fmt.Printf("install %d tool(s) on %s, in dependency order:\n", len(steps), profile)
	for _, st := range steps {
		switch {
		case st.Skip != "":
			fmt.Printf("  %-3s %-14s %s\n", "✔", st.Name, st.Skip)
		case st.Manual:
			fmt.Printf("  %-3s %-14s %-9s %s\n", "·", st.Name, st.Method, orUnknown(st.Docs))
		default:
			line := fmt.Sprintf("  %-3s %-14s %-9s %s", "→", st.Name, st.Method, st.Cmd)
			if st.Because != "" {
				line += "   (" + st.Because + ")"
			}
			fmt.Println(line)
		}
	}
}

// runPlan installs in order, streaming each command's output, continuing
// past a failure but skipping anything that needed the failed tool. Manual
// tools are listed at the end for a person to fetch.
func runPlan(steps []planStep, profile string) error {
	failed := map[string]bool{}
	ok, bad, skipped := 0, 0, 0
	var manual []planStep
	fmt.Printf("installing on %s:\n", profile)
	for _, st := range steps {
		switch {
		case st.Skip != "":
			skipped++
			fmt.Printf("  ✔ %-14s %s\n", st.Name, st.Skip)
			continue
		case st.Manual:
			manual = append(manual, st)
			continue
		}
		var blockedBy string
		for _, d := range toolDeps(st.Name, profile) {
			if failed[d] {
				blockedBy = d
			}
		}
		if blockedBy != "" {
			failed[st.Name] = true
			bad++
			fmt.Printf("  ✗ %-14s skipped — %s failed\n", st.Name, blockedBy)
			continue
		}
		fmt.Printf("\n  → %-14s %s\n", st.Name, st.Cmd)
		start := time.Now()
		cmd := exec.Command("sh", "-c", st.Cmd)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			failed[st.Name] = true
			bad++
			fmt.Printf("  ✗ %-14s %v\n", st.Name, err)
			continue
		}
		ok++
		fmt.Printf("  ✓ %-14s %s\n", st.Name, time.Since(start).Round(time.Second))
	}
	fmt.Printf("\ninstalled %d, failed %d, already there %d.\n", ok, bad, skipped)
	if len(manual) > 0 {
		fmt.Printf("\n%d need a manual download:\n", len(manual))
		for _, st := range manual {
			fmt.Printf("  %-14s %s\n", st.Name, orUnknown(st.Docs))
		}
	}
	if bad > 0 {
		return fmt.Errorf("%d install(s) failed", bad)
	}
	return nil
}
