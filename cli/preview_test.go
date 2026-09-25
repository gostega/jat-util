package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sized(m *pickModel, w, h int) *pickModel {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(*pickModel)
}

func previewRows() []PickRow {
	long := make([]PreviewLine, 60)
	for i := range long {
		long[i] = pl(' ', fmt.Sprintf("line %d", i+1))
	}
	return []PickRow{
		{ID: "git", Label: "git", Note: "differs — 1 would be overwritten, 1 new", ShortNote: "differs · 1 over, 1 new", Selected: true,
			Preview: func() Preview { return Preview{Title: "git · what would change here", Lines: long} }},
		{ID: "zsh", Label: "zsh", Note: "identical — 2 file(s)",
			Preview: func() Preview { return Preview{Title: "zsh", Lines: []PreviewLine{dim("identical")}} }},
		{ID: "plain", Label: "plain", Note: "no preview here"},
	}
}

func TestPaneOpensWithRightClosesWithLeftAndLettersStillFilter(t *testing.T) {
	m := sized(newPickModel("t", "", previewRows(), false), 110, 30)
	if m.paneOpen {
		t.Fatal("pane open before → was pressed")
	}
	m = send(m, "right")
	if !m.paneOpen || m.fullscreen() {
		t.Fatalf("→ at 120 columns: open=%v fullscreen=%v, want open side pane", m.paneOpen, m.fullscreen())
	}
	if !strings.Contains(m.View(), "what would change here") {
		t.Error("pane content not rendered")
	}
	if !strings.Contains(m.View(), "differs · 1 over") || strings.Contains(m.View(), "would be overwritten, 1 new") {
		t.Error("list did not switch to the short note while the pane is open")
	}
	m = send(m, "g")
	if m.filter != "g" {
		t.Errorf("a letter with the pane open went somewhere other than the filter: %q", m.filter)
	}
	m = send(m, " ")
	if m.selected["git"] {
		t.Error("space with the pane open did not toggle the row")
	}
	m = send(m, "left")
	if m.paneOpen {
		t.Error("← did not close the pane")
	}
	m = send(m, "backspace", "down", "down", "right")
	if m.paneOpen {
		t.Error("→ opened a pane for a row with nothing to preview")
	}
}

func TestPaneScrollsWithPgKeysAndShiftArrows(t *testing.T) {
	m := sized(newPickModel("t", "", previewRows(), false), 120, 24)
	m = send(m, "right")
	if m.paneScroll != 0 {
		t.Fatal("pane did not start at the top")
	}
	m = send(m, "shift+down")
	if m.paneScroll != 1 {
		t.Errorf("shift+↓ scrolled %d, want 1", m.paneScroll)
	}
	m = send(m, "pgdown")
	if m.paneScroll <= 1 {
		t.Errorf("pgdn scrolled to %d, want a page further", m.paneScroll)
	}
	before := m.paneScroll
	if m.cursor != 0 {
		t.Fatal("scrolling the pane moved the list cursor")
	}
	m = send(m, "pgup", "shift+up")
	if m.paneScroll >= before {
		t.Error("pgup/shift+↑ did not scroll back")
	}
	for range 20 {
		m = send(m, "pgdown")
	}
	if strings.Contains(m.View(), "more lines · pgdn") {
		t.Error("scrolled to the end but the pane still promises more")
	}
	// Moving the cursor resets the scroll position.
	m = send(m, "down")
	if m.paneScroll != 0 {
		t.Error("cursor move did not reset the pane scroll")
	}
}

func TestNarrowTerminalPreviewsFullscreen(t *testing.T) {
	m := sized(newPickModel("t", "", previewRows(), false), 80, 24)
	m = send(m, "right")
	if !m.paneOpen || !m.fullscreen() {
		t.Fatalf("→ at 80 columns: open=%v fullscreen=%v, want full-screen", m.paneOpen, m.fullscreen())
	}
	v := m.View()
	if !strings.Contains(v, "what would change here") || strings.Contains(v, "zsh") {
		t.Error("full-screen view should show the preview and hide the list")
	}
	// Letters are not a filter here; ↑/↓ scroll; space still toggles.
	m = send(m, "g")
	if m.filter != "" {
		t.Error("a letter in full-screen went to the filter")
	}
	m = send(m, "down")
	if m.paneScroll != 1 || m.cursor != 0 {
		t.Errorf("↓ in full-screen: scroll=%d cursor=%d, want 1 and 0", m.paneScroll, m.cursor)
	}
	m = send(m, " ")
	if m.selected["git"] {
		t.Error("space in full-screen did not toggle")
	}
	for _, back := range []string{"q", "esc", "left"} {
		m = send(m, "right", back)
		if m.paneOpen {
			t.Errorf("%s did not leave the full-screen preview", back)
		}
	}
	if m.confirmed {
		t.Error("leaving the preview confirmed the picker")
	}
}

// listRows() assumes one screen line per row. With the pane open the list
// column is cut, so no rendered line may exceed the terminal width.
func TestPaneViewNeverWraps(t *testing.T) {
	rows := previewRows()
	rows[0].Note = strings.Repeat("a very long note ", 10)
	rows[0].ShortNote = strings.Repeat("still too long ", 10)
	rows[0].Label = "a-label-that-is-itself-quite-long"
	for _, w := range []int{100, 120} {
		m := sized(newPickModel("t", strings.Repeat("subtitle ", 30), rows, false), w, 24)
		for _, line := range strings.Split(send(m, "right").View(), "\n") {
			if got := visibleWidth(line); got > w {
				t.Errorf("at %d columns a line is %d wide: %q", w, got, line)
			}
		}
	}
}

func visibleWidth(s string) int {
	// Strip ANSI, count runes.
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return len([]rune(b.String()))
}

// The guard that matters: a Secret item's bytes never reach a Preview.
func TestSecretItemsAreNeverPreviewed(t *testing.T) {
	home := t.TempDir()
	const secret = "-----BEGIN OPENSSH PRIVATE KEY-----"
	writeHome(t, home, map[string]string{".ssh/id_test": "local " + secret})
	b := testBundle(t, []string{"ssh-keys"}, map[string]string{
		"home/.ssh/id_test":     "bundle " + secret,
		"home/.ssh/id_test.pub": "ssh-ed25519 AAAA",
	})
	_, items, err := readBundle(b, home)
	if err != nil {
		t.Fatal(err)
	}
	it := items[0]
	if !it.Secret || it.State() != stateDiffers {
		t.Fatalf("test setup: %+v", it)
	}

	for _, hide := range []bool{false, true} {
		p := receivePreview(b, home, it, hide)
		text := p.Title
		for _, l := range p.Lines {
			text += "\n" + l.Text
		}
		if strings.Contains(text, secret) || strings.Contains(text, "AAAA") || strings.Contains(text, "@@") {
			t.Fatalf("hideNames=%v: secret content reached the preview:\n%s", hide, text)
		}
		if !strings.Contains(text, "hidden — this item holds credentials") {
			t.Errorf("hideNames=%v: missing the hidden line", hide)
		}
		hasName := strings.Contains(text, ".ssh/id_test")
		if hasName == hide {
			t.Errorf("hideNames=%v: names shown=%v", hide, hasName)
		}
		if !hide && !strings.Contains(text, "would be overwritten") {
			t.Error("a differing secret file should be named as overwritten")
		}
	}
	// Send side too.
	sp := sendPreview(home, "ssh-keys")
	for _, l := range sp.Lines {
		if strings.Contains(l.Text, secret) {
			t.Fatal("send preview leaked a secret")
		}
	}
}

func TestReceivePreviewDiffsWhatDiffersAndShowsWhatIsNew(t *testing.T) {
	home := t.TempDir()
	writeHome(t, home, map[string]string{".gitconfig": "[user]\n  name = James\n  email = old@example\n[core]\n  autocrlf = input\n"})
	b := testBundle(t, []string{"git"}, map[string]string{
		"home/.gitconfig":          "[user]\n  name = James\n  email = new@example\n[core]\n  editor = hx\n  autocrlf = input\n",
		"home/.gitconfig-personal": "[user]\n  email = me@example\n",
	})
	_, items, _ := readBundle(b, home)
	p := receivePreview(b, home, items[0], false)
	var kinds, text []string
	for _, l := range p.Lines {
		kinds = append(kinds, string(l.Kind))
		text = append(text, l.Text)
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{"-   email = old@example", "+   email = new@example", "+   editor = hx", ".gitconfig-personal  new", "email = me@example"} {
		if !strings.Contains(joined, want) {
			t.Errorf("preview missing %q:\n%s", want, joined)
		}
	}
	if strings.Index(joined, ".gitconfig  differs") > strings.Index(joined, ".gitconfig-personal  new") {
		t.Error("differing file should come before the new one")
	}
	if !strings.Contains(strings.Join(kinds, ""), "@") {
		t.Error("no hunk header")
	}
}

func TestPreviewTreeAndBinary(t *testing.T) {
	home := t.TempDir()
	b := testBundle(t, []string{"karabiner"}, map[string]string{
		"home/.config/karabiner/karabiner.json":           `{"a":1}`,
		"home/.config/karabiner/assets/complex/caps.json": `{}`,
		"home/.config/karabiner/automatic_backups/x.json": `{}`,
	})
	_, items, _ := readBundle(b, home)
	p := receivePreview(b, home, items[0], false)
	joined := ""
	for _, l := range p.Lines {
		joined += l.Text + "\n"
	}
	if !strings.Contains(joined, ".config/karabiner/") || !strings.Contains(joined, "caps.json") || strings.Contains(joined, `{"a":1}`) {
		t.Errorf("directory item should render as a tree, not contents:\n%s", joined)
	}

	bin := previewFile("x", []byte("abc\x00def"), 7, false)
	if len(bin) != 1 || !strings.Contains(bin[0].Text, "binary") {
		t.Errorf("binary file rendered: %v", bin)
	}
	big := previewFile("x", nil, 3<<20, true)
	if !strings.Contains(big[0].Text, "large text file") {
		t.Errorf("oversize file rendered: %v", big)
	}
	if _, err := os.Stat(filepath.Join(home, "nothing")); err == nil {
		t.Fatal("setup")
	}
}

func TestLineDiff(t *testing.T) {
	ops := lineDiff([]string{"a", "b", "c", "d"}, []string{"a", "x", "c", "d", "e"})
	var got string
	for _, op := range ops {
		got += string(op.kind) + op.text + " "
	}
	if got != " a -b +x  c  d +e " {
		t.Errorf("lineDiff = %q", got)
	}
	if lines := previewDiff([]byte("same\n"), []byte("same\r\n")); !strings.Contains(lines[0].Text, "no line changes") && !strings.Contains(lines[0].Text, "-") {
		t.Errorf("unexpected: %v", lines)
	}
}

func TestListColumnGrowsWithTheTerminal(t *testing.T) {
	if got := listColsFor(100); got != paneListMin {
		t.Errorf("at 100 columns list = %d, want %d", got, paneListMin)
	}
	if got := listColsFor(160); got <= paneListMin || got > paneListMax {
		t.Errorf("at 160 columns list = %d, want between %d and %d", got, paneListMin, paneListMax)
	}
	if got := listColsFor(400); got != paneListMax {
		t.Errorf("at 400 columns list = %d, want the cap %d", got, paneListMax)
	}
	// On a wide terminal the full note is shown; on a narrow one the short.
	wide := send(sized(newPickModel("t", "", previewRows(), false), 200, 30), "right").View()
	if !strings.Contains(wide, "would be overwritten, 1 new") {
		t.Error("wide terminal still shows the short note")
	}
	narrow := send(sized(newPickModel("t", "", previewRows(), false), 100, 30), "right").View()
	if !strings.Contains(narrow, "differs · 1 over") {
		t.Error("100-column terminal did not switch to the short note")
	}
}

func TestIdenticalItemsStillShowContents(t *testing.T) {
	home := t.TempDir()
	writeHome(t, home, map[string]string{".zshrc": "export A=1\n"})
	b := testBundle(t, []string{"zsh"}, map[string]string{"home/.zshrc": "export A=1\n"})
	_, items, _ := readBundle(b, home)
	if items[0].State() != stateIdentical {
		t.Fatal("setup")
	}
	p := receivePreview(b, home, items[0], false)
	joined := p.Title
	for _, l := range p.Lines {
		joined += "\n" + l.Text
	}
	if !strings.Contains(joined, "export A=1") || !strings.Contains(joined, "identical") {
		t.Errorf("identical item should show its contents and say it is identical:\n%s", joined)
	}
}
