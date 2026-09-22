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
	// ShortNote replaces Note while the preview pane is open and the list
	// column is narrow. Preview builds the pane's content on demand; nil
	// means the row has nothing to preview.
	ShortNote string
	Preview   func() Preview
}

// The pane needs room for a diff. Below paneMinWidth the preview takes the
// whole screen instead (James, 2026-09-18) rather than being squeezed into a
// sliver; at or above it the list keeps paneListCols and the pane the rest.
// Both are tunable in one place (James, 2026-09-22: "is it hard to increase
// the minimum later?" — no).
const (
	paneMinWidth = 100
	paneListCols = 40
)

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
	height, width   int
	single          bool
	confirmed       bool

	paneOpen   bool
	paneScroll int
	previews   map[string]Preview // built once per row, on first open
}

func newPickModel(title, subtitle string, rows []PickRow, single bool) *pickModel {
	m := &pickModel{
		title:    title,
		subtitle: subtitle,
		rows:     rows,
		single:   single,
		selected: map[string]bool{},
		previews: map[string]Preview{},
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
		m.height, m.width = msg.Height, msg.Width
		return m, nil

	case tea.KeyMsg:
		// Full-screen preview is its own little mode: the list is hidden, so
		// letters are not a filter and ↑/↓ scroll the pane. space still
		// toggles, so a decision can be made without leaving.
		if m.paneOpen && m.fullscreen() {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "left", "q", "esc":
				m.paneOpen = false
			case "up", "shift+up", "pgup":
				m.scrollPane(-m.paneStep(msg.String()))
			case "down", "shift+down", "pgdown":
				m.scrollPane(m.paneStep(msg.String()))
			case " ":
				if len(m.visible) > 0 {
					m.toggle(m.visible[m.cursor].ID)
				}
			case "enter":
				m.confirmed = true
				return m, tea.Quit
			}
			return m, nil
		}

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
		// ←/→ rather than a letter: every printable rune goes to the filter.
		case "right":
			if len(m.visible) > 0 && m.visible[m.cursor].Preview != nil {
				m.paneOpen = true
				m.paneScroll = 0
			}
		case "left":
			m.paneOpen = false
		case "pgup", "shift+up":
			m.scrollPane(-m.paneStep(msg.String()))
		case "pgdown", "shift+down":
			m.scrollPane(m.paneStep(msg.String()))
		case "up":
			if m.cursor > 0 {
				m.cursor--
				m.paneScroll = 0
			}
		case "down":
			if m.cursor < len(m.visible)-1 {
				m.cursor++
				m.paneScroll = 0
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

// fullscreen says whether an open pane takes the whole screen: below
// paneMinWidth, or before the terminal has said how wide it is.
func (m *pickModel) fullscreen() bool { return m.width > 0 && m.width < paneMinWidth }

// paneStep is how far one key scrolls: a page for pgup/pgdn, one line for
// the arrow forms.
func (m *pickModel) paneStep(key string) int {
	if strings.HasPrefix(key, "pg") {
		return max(1, m.paneRows()-1)
	}
	return 1
}

func (m *pickModel) scrollPane(by int) {
	if !m.paneOpen {
		return
	}
	p := m.currentPreview()
	m.paneScroll = max(0, min(m.paneScroll+by, len(p.Lines)-m.paneRows()))
}

// currentPreview builds the cursor row's preview the first time it is asked
// for and keeps it: a diff is not recomputed on every keystroke.
func (m *pickModel) currentPreview() Preview {
	if len(m.visible) == 0 {
		return Preview{}
	}
	r := m.visible[m.cursor]
	if p, ok := m.previews[r.ID]; ok {
		return p
	}
	var p Preview
	if r.Preview != nil {
		p = r.Preview()
	}
	m.previews[r.ID] = p
	return p
}

// paneRows is how many preview lines fit beside the list (or on the full
// screen): the same chrome as the list, less the pane's own title line.
func (m *pickModel) paneRows() int {
	if m.height <= 0 {
		return 20
	}
	if m.fullscreen() {
		return max(3, m.height-9) // title, blank, pane title, blank, blank, two help lines, margins
	}
	return max(3, m.listRows()-2)
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
	if m.paneOpen && m.fullscreen() {
		return m.viewFullscreen()
	}

	var b strings.Builder
	b.WriteString("\n  " + pickTitle.Render(m.title) + "\n")
	if m.subtitle != "" {
		b.WriteString("  " + pickDim.Render(truncate(m.subtitle, m.width-2)) + "\n")
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

	// The list column is the whole terminal, or paneListCols beside a pane.
	// Rows are cut to it rather than wrapped, so listRows() stays true.
	listCols := m.width
	if m.paneOpen {
		listCols = paneListCols
	}
	var list []string
	if start > 0 {
		list = append(list, pickDim.Render(fmt.Sprintf("    ↑ %d more", start)))
	}
	width := 0
	for _, r := range m.visible {
		width = max(width, len(r.Label))
	}
	for i := start; i < end; i++ {
		list = append(list, m.renderRow(m.visible[i], i == m.cursor, width, listCols))
	}
	if end < len(m.visible) {
		list = append(list, pickDim.Render(fmt.Sprintf("    ↓ %d more", len(m.visible)-end)))
	}

	if m.paneOpen {
		pane := m.renderPane(m.width-paneListCols-3, len(list))
		for i := 0; i < max(len(list), len(pane)); i++ {
			left := ""
			if i < len(list) {
				left = list[i]
			}
			right := ""
			if i < len(pane) {
				right = pane[i]
			}
			b.WriteString(padTo(left, paneListCols) + pickDim.Render("│") + " " + right + "\n")
		}
	} else {
		for _, l := range list {
			b.WriteString(l + "\n")
		}
	}

	// Two lines rather than one: the full set runs past 80 columns and wraps
	// into a ragged second line on a standard terminal.
	switch {
	case m.paneOpen:
		b.WriteString("\n" + pickDim.Render("  type to filter · ↑/↓ move · ← close preview · shift+↑/↓ or pgup/pgdn scroll preview") + "\n")
	case m.hasPreviews():
		b.WriteString("\n" + pickDim.Render("  type to filter · ↑/↓ move · → preview") + "\n")
	default:
		b.WriteString("\n" + pickDim.Render("  type to filter · ↑/↓ move") + "\n")
	}
	if m.single {
		b.WriteString(pickDim.Render("  enter choose · esc/ctrl+c cancel") + "\n")
	} else {
		b.WriteString(pickDim.Render("  space toggle · ctrl+a all · ctrl+n none · enter confirm · esc/ctrl+c cancel") + "\n")
	}
	return b.String()
}

// renderRow draws one list line, cut to cols with an ellipsis. Padding
// happens before styling: ANSI codes would otherwise count toward the width.
func (m *pickModel) renderRow(r PickRow, atCursor bool, labelWidth, cols int) string {
	cursor := "  "
	if atCursor {
		cursor = pickMark.Render("❯ ")
	}
	// Single-select has nothing to accumulate, so the cursor alone says
	// what is chosen — a checkbox would only imply a space press is needed.
	check := ""
	checkW := 0
	if !m.single {
		check = pickDim.Render("•") + " "
		if m.selected[r.ID] {
			check = pickMark.Render("✓") + " "
		}
		checkW = 2
	}
	// The row itself carries the selection, not just its tick: scanning a
	// long list for ticks is harder than scanning it for bright text.
	style := pickOff
	if m.selected[r.ID] || (m.single && atCursor) {
		style = pickOn
	}
	note := r.Note
	if m.paneOpen && r.ShortNote != "" {
		note = r.ShortNote
	}
	if note == "" {
		return fmt.Sprintf("  %s%s%s", cursor, check, style.Render(truncate(r.Label, cols-4-checkW)))
	}
	noteStyle := pickDim
	if r.NoteColor != "" {
		noteStyle = pickStyle.NewStyle().Foreground(lipgloss.Color(r.NoteColor))
	}
	label := fmt.Sprintf("%-*s", labelWidth, r.Label)
	if cols <= 0 { // no size message yet: show everything
		return fmt.Sprintf("  %s%s%s  %s", cursor, check, style.Render(label), noteStyle.Render(note))
	}
	room := cols - 4 - checkW - len([]rune(label)) - 2
	if room < 4 {
		// Not enough left for a note worth reading: the label gets the row.
		return fmt.Sprintf("  %s%s%s", cursor, check, style.Render(truncate(r.Label, cols-4-checkW)))
	}
	return fmt.Sprintf("  %s%s%s  %s", cursor, check, style.Render(label), noteStyle.Render(truncate(note, room)))
}

// renderPane draws the preview for the cursor row, at most rows lines wide
// cols, from the current scroll position.
func (m *pickModel) renderPane(cols, rows int) []string {
	p := m.currentPreview()
	out := []string{pickTitle.Render(truncate(p.Title, cols)), ""}
	// paneRows is the budget for lines; the same number scrollPane clamps
	// to, so "more" below is only ever said when there is more.
	end := min(len(p.Lines), m.paneScroll+m.paneRows())
	for _, l := range p.Lines[m.paneScroll:end] {
		out = append(out, renderPreviewLine(l, cols))
	}
	if end < len(p.Lines) {
		out[len(out)-1] = pickDim.Render(fmt.Sprintf("↓ %d more lines · pgdn", len(p.Lines)-end+1))
	}
	if m.paneScroll > 0 && len(out) > 1 {
		out[1] = pickDim.Render(fmt.Sprintf("↑ %d more lines · pgup", m.paneScroll))
	}
	return out
}

var (
	pickAdd  = pickStyle.NewStyle().Foreground(lipgloss.Color("10"))
	pickDel  = pickStyle.NewStyle().Foreground(lipgloss.Color("9"))
	pickHunk = pickStyle.NewStyle().Foreground(lipgloss.Color("14"))
	pickWarn = pickStyle.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
)

func renderPreviewLine(l PreviewLine, cols int) string {
	text := truncate(strings.ReplaceAll(l.Text, "\t", "    "), cols)
	switch l.Kind {
	case '+':
		return pickAdd.Render(text)
	case '-':
		return pickDel.Render(text)
	case '@':
		return pickHunk.Render(text)
	case '.':
		return pickDim.Render(text)
	case '!':
		return pickWarn.Render(text)
	case 'h':
		return pickTitle.Render(text)
	}
	return text
}

// viewFullscreen is the narrow-terminal preview: the same content as the
// pane, with the list hidden and its own help line.
func (m *pickModel) viewFullscreen() string {
	var b strings.Builder
	r := m.visible[m.cursor]
	mark := pickDim.Render("•")
	if m.selected[r.ID] {
		mark = pickMark.Render("✓")
	}
	b.WriteString("\n  " + mark + " " + pickTitle.Render(truncate(r.Label, m.width-6)) + "  " + pickDim.Render(truncate(r.Note, m.width-8-len([]rune(r.Label)))) + "\n\n")
	for _, l := range m.renderPane(m.width-4, m.paneRows()) {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("\n" + pickDim.Render("  ↑/↓ or pgup/pgdn scroll · space toggle · enter confirm") + "\n")
	b.WriteString(pickDim.Render("  ←, q or esc back to the list · ctrl+c cancel") + "\n")
	return b.String()
}

func (m *pickModel) hasPreviews() bool {
	for _, r := range m.rows {
		if r.Preview != nil {
			return true
		}
	}
	return false
}

// truncate cuts plain text to cols runes with an ellipsis. cols <= 0 means
// the terminal has not said its size yet, so nothing is cut.
func truncate(s string, cols int) string {
	if cols <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= cols {
		return s
	}
	if cols == 1 {
		return "…"
	}
	return string(r[:cols-1]) + "…"
}

// padTo right-pads a styled string to cols visible columns.
func padTo(s string, cols int) string {
	w := lipgloss.Width(s)
	if w >= cols {
		return s
	}
	return s + strings.Repeat(" ", cols-w)
}
