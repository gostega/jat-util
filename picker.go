package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

// The multi-select used by both ends of `jat migrate`: on send to choose what
// leaves the machine, on receive to choose what lands. Type to filter with no
// leading '/', space toggles, and each row can carry a right-hand note — which
// is how receive shows absent / identical / differs against what's on disk.

// PickRow is one line in the picker. Note is an optional right-hand
// annotation; NoteColor styles it. Selected is the initial state, so receive
// can arrive with everything but the already-identical rows ticked.
type PickRow struct {
	ID        string
	Label     string
	Note      string
	NoteColor string
	Selected  bool
}

// Styles come from a renderer bound to stderr, not lipgloss's default one.
// lipgloss decides what the terminal supports by inspecting stdout — which is
// a pipe whenever jat's output is being captured — and then strips every colour
// below, even though the picker itself draws to a real terminal on stderr.
var pickStyle = lipgloss.NewRenderer(os.Stderr)

var (
	pickTitle = pickStyle.NewStyle().Bold(true)
	pickDim   = pickStyle.NewStyle().Foreground(lipgloss.Color("240"))
	pickMark  = pickStyle.NewStyle().Foreground(lipgloss.Color("212"))
	// An explicit grey, not Faint(true): faint renders as near-invisible in
	// some terminals, which loses the unselected rows entirely.
	pickOff = pickStyle.NewStyle().Foreground(lipgloss.Color("246"))
	pickOn  = pickStyle.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#111111", Dark: "#dddddd"}).Bold(true)
)

// runPickerOne is the single-choice variant, for wizard questions rather than
// selections. Returns the chosen row's ID.
func runPickerOne(title, subtitle string, rows []PickRow) (id string, ok bool, err error) {
	ids, ok, err := runPickerMode(title, subtitle, rows, true)
	if err != nil || !ok || len(ids) == 0 {
		return "", false, err
	}
	return ids[0], true, nil
}

// runPicker blocks until enter or abort. ok is false when the user cancelled;
// ids come back in the order the rows were passed in, not selection order.
func runPicker(title, subtitle string, rows []PickRow) (ids []string, ok bool, err error) {
	return runPickerMode(title, subtitle, rows, false)
}

func runPickerMode(title, subtitle string, rows []PickRow, single bool) (ids []string, ok bool, err error) {
	// Checked up front so a non-interactive run gets this rather than
	// bubbletea's "could not open a new TTY: /dev/tty: device not configured".
	if !stdinIsTerminal() {
		return nil, false, fmt.Errorf("%s needs a terminal — use --all, or pass flags, to choose non-interactively", title)
	}

	out, err := tea.NewProgram(
		newPickModel(title, subtitle, rows, single),
		tea.WithAltScreen(),
		// stderr, so stdout stays clean for anything being piped or captured.
		tea.WithOutput(os.Stderr),
	).Run()
	if err != nil {
		return nil, false, err
	}

	m := out.(*pickModel)
	if !m.confirmed {
		return nil, false, nil
	}
	for _, r := range rows {
		if m.selected[r.ID] {
			ids = append(ids, r.ID)
		}
	}
	return ids, true, nil
}

// stdinIsTerminal reports whether there is a real terminal to read from.
//
// A mode check for os.ModeCharDevice is not enough: /dev/null is itself a
// character device, so a command run with stdin redirected from it passes that
// test and then dies inside bubbletea instead. This is an ioctl check.
func stdinIsTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

type pickModel struct {
	title, subtitle string
	rows            []PickRow
	visible         []PickRow
	selected        map[string]bool
	filter          string
	cursor          int
	height          int
	single          bool
	confirmed       bool
}

func newPickModel(title, subtitle string, rows []PickRow, single bool) *pickModel {
	m := &pickModel{
		title:    title,
		subtitle: subtitle,
		rows:     rows,
		single:   single,
		selected: map[string]bool{},
	}
	for _, r := range rows {
		if r.Selected {
			m.selected[r.ID] = true
		}
	}
	m.recompute()
	return m
}

func (m *pickModel) recompute() {
	m.visible = m.visible[:0]
	f := strings.ToLower(m.filter)
	for _, r := range m.rows {
		if f != "" && !strings.Contains(strings.ToLower(r.Label+" "+r.Note), f) {
			continue
		}
		m.visible = append(m.visible, r)
	}
	if m.cursor >= len(m.visible) {
		m.cursor = max(0, len(m.visible)-1)
	}
}

func (m *pickModel) toggle(id string) {
	if m.selected[id] {
		delete(m.selected, id)
		return
	}
	if m.single {
		// Choosing replaces rather than refusing, so a mis-hit needs no
		// separate deselect step.
		m.selected = map[string]bool{}
	}
	m.selected[id] = true
}

func (m *pickModel) Init() tea.Cmd { return nil }

func (m *pickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			// Esc clears a filter first — quitting on the first press would
			// throw away the selection someone just spent time building.
			if m.filter != "" {
				m.filter = ""
				m.recompute()
				return m, nil
			}
			return m, tea.Quit
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down":
			if m.cursor < len(m.visible)-1 {
				m.cursor++
			}
		case " ":
			if len(m.visible) > 0 {
				m.toggle(m.visible[m.cursor].ID)
			}
		// Letters all go to the filter, so select-all/none need modifiers.
		case "ctrl+a":
			if !m.single {
				for _, r := range m.visible {
					m.selected[r.ID] = true
				}
			}
		case "ctrl+n":
			if !m.single {
				for _, r := range m.visible {
					delete(m.selected, r.ID)
				}
			}
		case "enter":
			// In single mode the cursor IS the choice, so enter takes the row
			// under it rather than demanding a space press first.
			if m.single && len(m.selected) == 0 && len(m.visible) > 0 {
				m.toggle(m.visible[m.cursor].ID)
			}
			m.confirmed = true
			return m, tea.Quit
		case "backspace":
			if m.filter != "" {
				m.filter = m.filter[:len(m.filter)-1]
				m.recompute()
			}
		default:
			// Any other single rune extends the filter — instantly, no '/'.
			if len(msg.Runes) == 1 {
				m.filter += string(msg.Runes)
				m.cursor = 0
				m.recompute()
			}
		}
	}
	return m, nil
}

// listRows is how many rows fit once the chrome is accounted for.
//
// Assumes one screen line per row, so the scroll maths drifts if a label wraps
// — only possible on a very narrow terminal, since labels run ~40 chars.
func (m *pickModel) listRows() int {
	if m.height <= 0 { // no size message yet — show everything
		return len(m.visible)
	}
	// Leading blank, title, blank, the count line, blank, two help lines, and
	// room for both scroll hints.
	chrome := 9
	if m.subtitle != "" {
		chrome++
	}
	if m.filter != "" {
		chrome += 2
	}
	return max(1, m.height-chrome)
}

func (m *pickModel) View() string {
	var b strings.Builder
	b.WriteString("\n  " + pickTitle.Render(m.title) + "\n")
	if m.subtitle != "" {
		b.WriteString("  " + pickDim.Render(m.subtitle) + "\n")
	}
	if m.single {
		b.WriteString("\n")
	} else {
		b.WriteString("\n  " + pickDim.Render(fmt.Sprintf("%d of %d selected", len(m.selected), len(m.rows))) + "\n\n")
	}

	if m.filter != "" {
		b.WriteString(fmt.Sprintf("  filter: %s\n\n", m.filter))
	}
	if len(m.visible) == 0 {
		b.WriteString(pickDim.Render("  (no matches)") + "\n")
	}

	// Scroll window: keep the cursor in view, say what's off-screen.
	rows := m.listRows()
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	end := min(start+rows, len(m.visible))
	if start > 0 {
		b.WriteString(pickDim.Render(fmt.Sprintf("    ↑ %d more", start)) + "\n")
	}

	width := 0
	for _, r := range m.visible {
		width = max(width, len(r.Label))
	}

	for i := start; i < end; i++ {
		r := m.visible[i]
		cursor := "  "
		if i == m.cursor {
			cursor = pickMark.Render("❯ ")
		}
		// Single-select has nothing to accumulate, so the cursor alone says
		// what is chosen — a checkbox would only imply a space press is needed.
		check := ""
		if !m.single {
			check = pickDim.Render("•") + " "
			if m.selected[r.ID] {
				check = pickMark.Render("✓") + " "
			}
		}
		// The row itself carries the selection, not just its tick: scanning a
		// long list for ticks is harder than scanning it for bright text.
		style := pickOff
		if m.selected[r.ID] || (m.single && i == m.cursor) {
			style = pickOn
		}
		if r.Note == "" {
			b.WriteString(fmt.Sprintf("  %s%s%s\n", cursor, check, style.Render(r.Label)))
			continue
		}
		// Pad before styling: ANSI codes would otherwise count toward the width.
		note := pickDim
		if r.NoteColor != "" {
			note = pickStyle.NewStyle().Foreground(lipgloss.Color(r.NoteColor))
		}
		b.WriteString(fmt.Sprintf("  %s%s%s  %s\n", cursor, check,
			style.Render(fmt.Sprintf("%-*s", width, r.Label)), note.Render(r.Note)))
	}
	if end < len(m.visible) {
		b.WriteString(pickDim.Render(fmt.Sprintf("    ↓ %d more", len(m.visible)-end)) + "\n")
	}

	// Two lines rather than one: the full set runs past 80 columns and wraps
	// into a ragged second line on a standard terminal.
	b.WriteString("\n" + pickDim.Render("  type to filter · ↑/↓ move") + "\n")
	if m.single {
		b.WriteString(pickDim.Render("  enter choose · esc/ctrl+c cancel") + "\n")
	} else {
		b.WriteString(pickDim.Render("  space toggle · ctrl+a all · ctrl+n none · enter confirm · esc/ctrl+c cancel") + "\n")
	}
	return b.String()
}
