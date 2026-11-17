// Package tui provides interactive fuzzy/regex selection over cached references.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"
	"github.com/zoomoid/kubectxmgr/internal/search"
	"github.com/zoomoid/kubectxmgr/internal/state"
	"golang.org/x/term"
)

type model struct {
	all, matches   []state.Reference
	query, mode    string
	cursor, height int
	selected       *state.Reference
	err            error
}

// Init requires no asynchronous work: selection operates on an existing inventory.
func (m model) Init() tea.Cmd { return nil }

// Update handles navigation and recomputes matches without making provider calls.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch key := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = max(1, key.Height-5)
	case tea.KeyPressMsg:
		switch key.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			if m.err == nil && len(m.matches) > 0 {
				r := m.matches[m.cursor]
				m.selected = &r

				return m, tea.Quit
			}
		case "up", "ctrl+k":
			m.cursor = max(0, m.cursor-1)
		case "down", "ctrl+j":
			m.cursor = min(max(0, len(m.matches)-1), m.cursor+1)
		case "tab":
			if m.mode == "fuzzy" {
				m.mode = "regex"
			} else {
				m.mode = "fuzzy"
			}

			m.cursor = 0
		case "backspace":
			runes := []rune(m.query)
			if len(runes) > 0 {
				m.query = string(runes[:len(runes)-1])
			}

			m.cursor = 0
		default:
			m.query += key.Key().Text
			m.cursor = 0
		}
	}

	m.matches, m.err = search.Match(m.all, m.query, m.mode)

	return m, nil
}

// View renders a bounded results window with terminal control characters removed
// from provider-controlled labels and IDs before adding local highlighting.
func (m model) View() tea.View {
	var b strings.Builder
	fmt.Fprintf(&b, "%s > %s\n", m.mode, m.query)
	if m.err != nil {
		fmt.Fprintf(&b, "%s\n", m.err)
	}

	start := max(0, m.cursor-m.height+1)
	for i := start; i < min(len(m.matches), start+m.height); i++ {
		r := m.matches[i]
		label := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return '?'
			}

			return r
		}, r.Name)

		if m.mode == "fuzzy" && m.query != "" {
			match := fuzzy.Find(m.query, []string{label})
			if len(match) > 0 {
				indexes := map[int]bool{}
				for _, index := range match[0].MatchedIndexes {
					indexes[index] = true
				}

				var highlighted strings.Builder
				for j, c := range []rune(label) {
					if indexes[j] {
						highlighted.WriteString("\x1b[1;36m")
					}

					highlighted.WriteRune(c)
					if indexes[j] {
						highlighted.WriteString("\x1b[0m")
					}
				}

				label = highlighted.String()
			}
		}

		prefix := "  "
		if i == m.cursor {
			prefix = "> "
		}

		id := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return '?'
			}

			return r
		}, r.ID)

		fmt.Fprintf(&b, "%s%s [%s]\n", prefix, label, id)
	}

	fmt.Fprintf(&b, "\n%d matches | tab: fuzzy/regex | enter: select | esc: cancel", len(m.matches))

	return tea.NewView(b.String())
}

// Pick selects a reference on a TTY, starting in fuzzy or explicit regex mode.
// The UI writes to stderr so command stdout remains available for shell integration.
func Pick(ctx context.Context, refs []state.Reference, regex bool) (state.Reference, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return state.Reference{}, errors.New("interactive selection needs a TTY; use list-contexts and set-context")
	}

	mode := "fuzzy"
	if regex {
		mode = "regex"
	}

	m := model{all: refs, matches: refs, mode: mode, height: 15}
	result, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(os.Stderr)).Run()
	if err != nil {
		return state.Reference{}, err
	}

	final, ok := result.(model)
	if !ok || final.selected == nil {
		return state.Reference{}, errors.New("selection cancelled")
	}

	return *final.selected, nil
}
