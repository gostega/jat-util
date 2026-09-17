package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+a":
		return tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func send(m *pickModel, keys ...string) *pickModel {
	for _, k := range keys {
		next, _ := m.Update(key(k))
		m = next.(*pickModel)
	}
	return m
}

func testRows() []PickRow {
	return []PickRow{
		{ID: "bash", Label: "bash", Note: "absent"},
		{ID: "ghostty", Label: "ghostty", Note: "differs", Selected: true},
		{ID: "git", Label: "git", Note: "identical"},
		{ID: "mise", Label: "mise", Note: "absent"},
	}
}

func selectedIDs(m *pickModel) []string {
	var got []string
	for _, r := range m.rows {
		if m.selected[r.ID] {
			got = append(got, r.ID)
		}
	}
	return got
}

func TestPickerInitialSelection(t *testing.T) {
	m := newPickModel("t", "", testRows())
	if got := selectedIDs(m); len(got) != 1 || got[0] != "ghostty" {
		t.Errorf("initial selection = %v, want [ghostty]", got)
	}
}

func TestPickerToggleAndConfirm(t *testing.T) {
	m := newPickModel("t", "", testRows())
	m = send(m, " ")         // toggle bash on (cursor starts at row 0)
	m = send(m, "down", " ") // toggle ghostty off
	m = send(m, "enter")

	if !m.confirmed {
		t.Fatal("enter did not confirm")
	}
	got := strings.Join(selectedIDs(m), ",")
	if got != "bash" {
		t.Errorf("selected = %q, want \"bash\"", got)
	}
}

func TestPickerFilterThenToggle(t *testing.T) {
	// Space must still select while filtering — that's the whole point of
	// type-to-filter with no leading '/'.
	m := newPickModel("t", "", testRows())
	m = send(m, "m", "i")
	if len(m.visible) != 1 || m.visible[0].ID != "mise" {
		t.Fatalf("filter 'mi' gave %d rows, want just mise", len(m.visible))
	}
	m = send(m, " ")
	if !m.selected["mise"] {
		t.Error("space while filtering did not select")
	}

	// Esc clears the filter rather than quitting, keeping the selection.
	m = send(m, "esc")
	if m.filter != "" {
		t.Errorf("esc left filter = %q", m.filter)
	}
	if len(m.visible) != 4 {
		t.Errorf("after esc, %d rows visible, want 4", len(m.visible))
	}
	if !m.selected["mise"] {
		t.Error("esc lost the selection")
	}
}

func TestPickerFilterMatchesNote(t *testing.T) {
	// Filtering on the annotation is how you'd grab every 'differs' row.
	m := newPickModel("t", "", testRows())
	m = send(m, "d", "i", "f")
	if len(m.visible) != 1 || m.visible[0].ID != "ghostty" {
		t.Errorf("filter 'dif' gave %v, want [ghostty]", m.visible)
	}
}

func TestPickerSelectAllAppliesToVisibleOnly(t *testing.T) {
	// ctrl+a must not reach rows the filter is hiding, or it silently selects
	// things the user cannot see.
	m := newPickModel("t", "", testRows())
	m = send(m, "ctrl+n") // clear the preselected ghostty
	m = send(m, "z", "z")
	if len(m.visible) != 0 {
		t.Fatalf("filter 'zz' matched %d rows, expected none", len(m.visible))
	}
	m = send(m, "backspace", "backspace", "m", "i", "ctrl+a")
	if got := strings.Join(selectedIDs(m), ","); got != "mise" {
		t.Errorf("ctrl+a while filtered selected %q, want \"mise\"", got)
	}
}

func TestPickerCursorStaysInRange(t *testing.T) {
	// Filtering down to fewer rows than the cursor index must not leave the
	// cursor pointing off the end.
	m := newPickModel("t", "", testRows())
	m = send(m, "down", "down", "down") // cursor at 3
	m = send(m, "m", "i")               // one row left
	if m.cursor >= len(m.visible) {
		t.Errorf("cursor %d out of range for %d rows", m.cursor, len(m.visible))
	}
	m = send(m, " ")
	if !m.selected["mise"] {
		t.Error("toggle after refilter hit the wrong row")
	}
}

func TestPickerCancelReturnsNothing(t *testing.T) {
	m := newPickModel("t", "", testRows())
	m = send(m, " ", "esc")
	if m.confirmed {
		t.Error("esc on an empty filter should not confirm")
	}
}

func TestPickerViewRendersWithoutSize(t *testing.T) {
	// No WindowSizeMsg yet: listRows must not return 0 and hide every row.
	m := newPickModel("Which configs?", "sub", testRows())
	out := m.View()
	for _, want := range []string{"Which configs?", "sub", "bash", "differs", "1 of 4 selected"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}
