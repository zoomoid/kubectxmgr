// Package tui provides interactive fuzzy/regex selection over cached references.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
	"github.com/zoomoid/kubectxmgr/internal/provider"
	"github.com/zoomoid/kubectxmgr/internal/search"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"golang.org/x/term"
)

// entryTemplate puts the provider's readable name before its stable inventory ID.
var entryTemplate = "%s%s [%s]"

// statusTemplate keeps the current mode and switching hint below the search prompt.
var statusTemplate = "%d matches | mode: %s | tab: fuzzy/regex"

// errorStatusTemplate preserves the mode while showing invalid-pattern errors.
var errorStatusTemplate = "mode: %s | %s"

// footerTemplate separates results, the bare prompt, and the bottom status line.
var footerTemplate = "\n\n%s\n\n%s"

// ErrCancelled distinguishes user cancellation from a picker or provider failure.
var ErrCancelled = errors.New("selection cancelled")

// Picker styles use terminal palette colors and preserve a text selection marker.
var (
	selectedStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	highlightStyle = lipgloss.NewStyle().Bold(true).Underline(true)
	qualifierStyle = lipgloss.NewStyle().Faint(true)
	statusStyle    = lipgloss.NewStyle().Faint(true)
	errorStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
)

type model struct {
	all, matches          []state.Reference
	mode                  string
	cursor, height, width int
	screenHeight          int
	input                 textinput.Model
	results               viewport.Model
	selected              *state.Reference
	quitting              bool
	err                   error
}

// newModel configures component input without cursor ticks or provider work.
func newModel(refs []state.Reference, regex bool) model {
	input := textinput.New()
	input.Prompt = "> "
	input.SetVirtualCursor(false)
	input.SetStyles(textinput.Styles{
		Focused: textinput.StyleState{Prompt: selectedStyle},
		Cursor:  textinput.CursorStyle{Shape: tea.CursorBar},
	})
	input.SetWidth(75)
	input.Focus()

	mode := "fuzzy"
	if regex {
		mode = "regex"
	}

	return model{
		all: refs, matches: refs, mode: mode, height: 15, width: 80, screenHeight: 19,
		input: input, results: viewport.New(),
	}
}

// Init requires no asynchronous work: selection operates on an existing inventory.
func (m model) Init() tea.Cmd { return nil }

// Update handles navigation and recomputes matches without making provider API calls.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch key := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, key.Width)
		m.screenHeight = max(1, key.Height)
		m.height = max(0, key.Height-4)
		m.input.SetWidth(max(1, m.width-5))

		return m, nil
	case tea.KeyPressMsg:
		switch key.String() {
		case "ctrl+c", "esc":
			m.quitting = true

			return m, tea.Quit
		case "enter":
			if m.err == nil && len(m.matches) > 0 {
				r := m.matches[m.cursor]
				m.selected = &r
				m.quitting = true

				return m, tea.Quit
			}

			return m, nil
		case "up", "ctrl+k":
			m.cursor = min(max(0, len(m.matches)-1), m.cursor+1)

			return m, nil
		case "down", "ctrl+j":
			m.cursor = max(0, m.cursor-1)

			return m, nil
		case "tab":
			if m.mode == "fuzzy" {
				m.mode = "regex"
			} else {
				m.mode = "fuzzy"
			}

			m.cursor = 0
			m.matches, m.err = matchEntries(m.all, m.input.Value(), m.mode)

			return m, nil
		}
	}

	previous := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if previous != m.input.Value() {
		m.cursor = 0
		m.matches, m.err = matchEntries(m.all, m.input.Value(), m.mode)
	}

	return m, cmd
}

// View grows results upward from a fixed footer, with the best match nearest
// the prompt. Provider-controlled labels and IDs are sanitized before highlighting.
func (m model) View() tea.View {
	view := tea.NewView("")
	view.AltScreen = true
	if m.quitting {
		return view
	}

	gutter := 0
	if m.width >= 4 {
		gutter = 1
	}

	width := max(1, m.width-2*gutter)
	start := max(0, m.cursor-m.height+1)
	end := min(len(m.matches), start+m.height)
	rows := make([]string, 0, max(0, end-start))
	for i := end - 1; i >= start; i-- {
		rows = append(rows, m.renderEntry(m.matches[i], i == m.cursor, width))
	}

	// Render only the visible window; the viewport owns cell clipping and sizing.
	m.results.SetWidth(width)
	m.results.SetHeight(m.height)
	m.results.SoftWrap = false
	m.results.SetContent(strings.Repeat("\n", max(0, m.height-len(rows))) + strings.Join(rows, "\n"))

	status := statusStyle.Render(fmt.Sprintf(statusTemplate, len(m.matches), m.mode))
	if m.err != nil {
		status = errorStyle.Render(fmt.Sprintf(errorStatusTemplate, m.mode, m.err))
	}

	status = ansi.Truncate(status, width, "...")
	prompt := ansi.Truncate(m.input.View(), width, "")
	content := m.results.View() + fmt.Sprintf(footerTemplate, prompt, status)
	promptRow := m.height + 1
	if m.height == 0 {
		content = prompt + "\n\n" + status
		promptRow = 0
		if m.screenHeight < 3 {
			content = prompt + "\n" + status
		}
	}

	view.Content = lipgloss.NewStyle().Padding(0, gutter).MaxWidth(m.width).MaxHeight(m.screenHeight).Render(content)
	view.Cursor = m.input.Cursor()
	if view.Cursor != nil {
		view.Cursor.Position.X = min(m.width-1, view.Cursor.Position.X+gutter)
		view.Cursor.Position.Y = promptRow
	}

	return view
}

// renderEntry styles one visible row, truncating by terminal cells instead of bytes.
func (m model) renderEntry(ref state.Reference, selected bool, width int) string {
	entry := provider.NewEntry(ref)
	label := sanitize(entry.Label())
	if m.mode == "fuzzy" && m.input.Value() != "" {
		if matches := fuzzy.Find(m.input.Value(), []string{label}); len(matches) > 0 {
			label = lipgloss.StyleRunes(label, matches[0].MatchedIndexes, highlightStyle, lipgloss.NewStyle())
		}
	}

	prefix := "  "
	if selected {
		prefix = selectedStyle.Render("> ")
		label = selectedStyle.Render(label)
	}

	row := fmt.Sprintf(entryTemplate, prefix, label, qualifierStyle.Render(sanitize(entry.Reference().ID)))

	return ansi.Truncate(row, width, "...")
}

// sanitize prevents provider-controlled text from becoming terminal commands.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}

		return r
	}, s)
}

// matchEntries searches visible labels and qualified names without changing the
// references returned to callers or the pattern semantics used by command execution.
func matchEntries(refs []state.Reference, query, mode string) ([]state.Reference, error) {
	candidates := make([]state.Reference, 0, len(refs)*3)
	originals := make(map[string]state.Reference, len(refs))
	for _, ref := range refs {
		entry := provider.NewEntry(ref)
		for _, name := range []string{entry.Label(), ref.Name, ref.ID} {
			candidates = append(candidates, state.Reference{ID: ref.ID, Name: name})
		}

		originals[ref.ID] = ref
	}

	matches, err := search.Match(candidates, query, mode)
	if err != nil {
		return nil, err
	}

	// Several searchable names can match one cluster; only offer its original once.
	seen := make(map[string]bool, len(matches))
	var result []state.Reference
	for _, match := range matches {
		if !seen[match.ID] {
			result = append(result, originals[match.ID])
			seen[match.ID] = true
		}
	}

	if mode == "regex" {
		// Regex filters retain the caller's ordering, including namespace recency.
		result = nil
		for _, ref := range refs {
			if seen[ref.ID] {
				result = append(result, ref)
			}
		}
	}

	return result, nil
}

// Pick selects a reference on a TTY, starting in fuzzy or explicit regex mode.
// The UI writes to stderr so command stdout remains available for shell integration.
func Pick(ctx context.Context, refs []state.Reference, regex bool) (state.Reference, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return state.Reference{}, errors.New("interactive selection needs a TTY; use list-contexts and set-context")
	}

	m := newModel(refs, regex)
	result, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		if errors.Is(err, tea.ErrProgramKilled) && errors.Is(ctx.Err(), context.Canceled) {
			return state.Reference{}, ErrCancelled
		}

		return state.Reference{}, err
	}

	final, ok := result.(model)
	if !ok {
		return state.Reference{}, errors.New("picker returned an unexpected model")
	}

	if final.selected == nil {
		return state.Reference{}, ErrCancelled
	}

	return *final.selected, nil
}
