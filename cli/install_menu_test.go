package cli

import (
	"errors"
	"strings"
	"testing"
)

// Every tool must be reachable from a root with no cycles, or the tree and
// the install order both break.
func TestNoDependencyCycles(t *testing.T) {
	for profile := range profileOrder {
		all := make([]string, 0, len(tools))
		for n := range tools {
			all = append(all, n)
		}
		order, err := installOrder(tools, all, profile)
		if err != nil {
			t.Fatalf("%s: %v", profile, err)
		}
		if len(order) != len(tools) {
			t.Errorf("%s: order has %d tools, table has %d", profile, len(order), len(tools))
		}
		seen := map[string]bool{}
		for _, n := range order {
			for _, d := range toolDeps(tools, n, profile) {
				if !seen[d] {
					t.Errorf("%s: %s ordered before its dependency %s", profile, n, d)
				}
			}
			seen[n] = true
		}
		if rows := depTree(tools, profile); len(rows) != len(tools) {
			t.Errorf("%s: tree shows %d tools, table has %d", profile, len(rows), len(tools))
		}
	}
}

func TestDepsComeFromMethodAndNeeds(t *testing.T) {
	if got := toolDeps(tools, "ruby", "macos"); len(got) != 1 || got[0] != "mise" {
		t.Errorf("ruby on macos needs %v, want [mise]", got)
	}
	if got := toolDeps(tools, "ghostty", "macos"); len(got) != 1 || got[0] != "homebrew" {
		t.Errorf("ghostty on macos needs %v, want [homebrew]", got)
	}
	if got := toolDeps(tools, "nmap", "debian"); len(got) != 0 {
		t.Errorf("nmap via apt needs %v, want nothing", got)
	}
	if got := toolDeps(tools, "homebrew", "macos"); len(got) != 0 {
		t.Errorf("homebrew needs %v, want nothing (no self-dependency)", got)
	}
}

func fakeInstalled(t *testing.T, present ...string) {
	t.Helper()
	prev := lookPath
	lookPath = func(bin string) (string, error) {
		for _, p := range present {
			if p == bin {
				return "/usr/bin/" + bin, nil
			}
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { lookPath = prev })
}

func TestInstallMenuTabsTreeAndDependencies(t *testing.T) {
	fakeInstalled(t, "gh") // nothing else "installed"
	m := sized(newPickModel("t", "", installRows(tools, "macos"), false), 140, 50)

	if len(m.tabs) == 0 || m.tabs[0] != "All" || m.tabs[1] != "Apps" {
		t.Fatalf("tabs = %v", m.tabs)
	}
	if got := len(m.visible); got != len(tools) {
		t.Errorf("All tab shows %d rows, want %d", got, len(tools))
	}
	v := m.View()
	if !strings.Contains(v, "[ All") || !strings.Contains(v, "tab/shift+tab group") || !strings.Contains(v, "ctrl+t invert") {
		t.Error("tab bar or help line missing")
	}
	// The tree: mise hangs off homebrew, ruby off mise.
	var mise, ruby PickRow
	for _, r := range m.rows {
		switch r.ID {
		case "mise":
			mise = r
		case "ruby":
			ruby = r
		}
	}
	if !strings.Contains(mise.Tree, "─") || !strings.HasPrefix(ruby.Tree, "│") && !strings.HasPrefix(ruby.Tree, " ") {
		t.Errorf("tree glyphs: mise=%q ruby=%q", mise.Tree, ruby.Tree)
	}
	if !m.byID["gh"].Dim || strings.Contains(m.byID["gh"].Note, "brew install") {
		t.Error("installed gh should be dimmed and say installed")
	}

	// tab → Apps, shift+tab → back.
	m = send(m, "tab")
	if m.tab != 1 {
		t.Fatalf("after tab: tab=%d", m.tab)
	}
	for _, r := range m.visible {
		if r.Group != "Apps" && r.ID != "homebrew" {
			t.Errorf("Apps tab shows %s (%s)", r.ID, r.Group)
		}
	}
	m = send(m, "shift+tab")
	if m.tab != 0 {
		t.Errorf("after shift+tab: tab=%d", m.tab)
	}

	// Ticking ruby auto-ticks mise and homebrew; unticking ruby drops them.
	for i, r := range m.visible {
		if r.ID == "ruby" {
			m.cursor = i
		}
	}
	m = send(m, " ")
	for _, want := range []string{"ruby", "mise", "homebrew"} {
		if !m.selected[want] {
			t.Errorf("after ticking ruby, %s not selected", want)
		}
	}
	if !m.auto["mise"] || m.auto["ruby"] {
		t.Error("auto flags wrong")
	}
	if !strings.Contains(m.View(), "needed by a ticked row") {
		t.Error("auto-ticked row not explained")
	}
	m = send(m, " ")
	if len(m.selected) != 0 {
		t.Errorf("unticking ruby left %v selected", m.selected)
	}

	// ctrl+a skips installed (gh); ctrl+t inverts; ctrl+n clears.
	m = send(m, "ctrl+a")
	if m.selected["gh"] {
		t.Error("select-all ticked an installed tool")
	}
	n := len(m.selected)
	if n == 0 {
		t.Fatal("select-all ticked nothing")
	}
	m = send(m, "ctrl+t")
	if len(m.selected) != 0 {
		t.Errorf("invert after select-all left %d selected", len(m.selected))
	}
	m = send(m, "ctrl+t", "ctrl+n")
	if len(m.selected) != 0 {
		t.Error("ctrl+n did not clear")
	}
}

func TestBuildPlanOrdersAndSkips(t *testing.T) {
	fakeInstalled(t, "brew")
	steps, err := buildPlan(tools, []string{"ruby", "docker"}, "macos")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, st := range steps {
		names = append(names, st.Name)
	}
	got := strings.Join(names, " ")
	if got != "docker homebrew mise ruby" && got != "homebrew mise ruby docker" {
		t.Errorf("plan order %q", got)
	}
	for _, st := range steps {
		switch st.Name {
		case "homebrew":
			if st.Skip != "already installed" {
				t.Errorf("homebrew: %+v", st)
			}
		case "mise":
			if st.Because == "" {
				t.Error("mise should say what needed it")
			}
		case "docker":
			if !st.Manual {
				t.Error("docker should be manual")
			}
		}
	}
	if _, err := buildPlan(tools, []string{"act"}, "debian"); err != nil {
		t.Errorf("a tool with no method here should be a skipped step, not an error: %v", err)
	}
}

func TestPrunedTreeClosesItsBranches(t *testing.T) {
	fakeInstalled(t)
	m := sized(newPickModel("t", "", installRows(tools, "macos"), false), 140, 50)
	m = send(m, "tab") // Apps: homebrew's casks only
	var lastChild string
	for _, r := range m.visible {
		if strings.HasPrefix(r.Tree, "├─") || strings.HasPrefix(r.Tree, "└─") {
			lastChild = r.Tree
		}
	}
	if !strings.HasPrefix(lastChild, "└─") {
		t.Errorf("last visible child on a pruned tab draws %q, want └─", lastChild)
	}
	p := installPreview(tools, "ruby", "macos")
	joined := p.Title
	for _, l := range p.Lines {
		joined += "\n" + l.Text
	}
	for _, want := range []string{"mise use -g ruby", "needs", "mise"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ruby preview missing %q:\n%s", want, joined)
		}
	}
}
