package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

func plainView(m model) string {
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(strings.TrimPrefix(line, " "), " ")
	}

	return strings.Join(lines, "\n")
}

type terminalOutput struct {
	buffer   bytes.Buffer
	rendered chan struct{}
	once     sync.Once
}

func (w *terminalOutput) Write(data []byte) (int, error) {
	n, err := w.buffer.Write(data)
	if bytes.Contains(data, []byte("\x1b[?1049h")) {
		w.once.Do(func() { close(w.rendered) })
	}

	return n, err
}

func TestPickerUsesAlternateScreen(t *testing.T) {
	m := newModel(nil, false)
	require.True(t, m.View().AltScreen, "picker must not draw into shell scrollback")
}

func TestRegexPreservesRecentFirstOrder(t *testing.T) {
	refs := []state.Reference{{ID: "recent", Name: "recent"}, {ID: "alpha", Name: "alpha"}}
	m := newModel(refs, false)
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = next.(model)
	require.Equal(t, "regex", m.mode)
	require.Equal(t, refs, m.matches)
	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.Equal(t, "recent", next.(model).selected.ID)
}

func TestPickerRestoresTerminalOnExit(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		selected    bool
	}{
		{name: "selection", input: "\r", selected: true},
		{name: "escape", input: "\x1b"},
		{name: "interrupt", input: "\x03"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			m := newModel([]state.Reference{{ID: "one", Name: "cluster-one"}}, false)
			p := tea.NewProgram(m, tea.WithContext(t.Context()),
				tea.WithInput(strings.NewReader(tt.input)), tea.WithOutput(&output),
				tea.WithEnvironment([]string{"TERM=xterm-256color"}), tea.WithWindowSize(80, 10),
				tea.WithoutSignals())
			result, err := p.Run()
			require.NoError(t, err)
			require.Equal(t, tt.selected, result.(model).selected != nil)
			require.Empty(t, result.(model).View().Content)

			before, alternate, entered := strings.Cut(output.String(), "\x1b[?1049h")
			require.True(t, entered, "picker must enter the alternate buffer")
			_, after, exited := strings.Cut(alternate, "\x1b[?1049l")
			require.True(t, exited, "picker must restore the original buffer")
			require.NotContains(t, before+after, "cluster-one")
			require.NotContains(t, before+after, "\n", "picker must not add shell scrollback lines")
		})
	}
}

func TestPickerRestoresTerminalOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output := terminalOutput{rendered: make(chan struct{})}
	m := newModel([]state.Reference{{ID: "one", Name: "cluster-one"}}, false)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(&output),
		tea.WithWindowSize(80, 10), tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithoutSignals())
	go func() {
		select {
		case <-output.rendered:
			cancel()
		case <-ctx.Done():
		}
	}()
	_, err := p.Run()
	require.ErrorIs(t, err, tea.ErrProgramKilled)
	require.Contains(t, output.buffer.String(), "\x1b[?1049h")
	require.Contains(t, output.buffer.String(), "\x1b[?1049l")
}

func TestInputEditingAndTerminalBounds(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{
		{Width: 80, Height: 24}, {Width: 32, Height: 8},
		{Width: 9, Height: 3}, {Width: 1, Height: 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			m := newModel([]state.Reference{{ID: strings.Repeat("qualifier", 20), Name: "\u754c-cluster"}}, false)
			next, _ := m.Update(size)
			m = next.(model)
			require.LessOrEqual(t, lipgloss.Width(m.View().Content), size.Width)
			require.LessOrEqual(t, lipgloss.Height(m.View().Content), size.Height)

			for _, msg := range []tea.Msg{
				tea.KeyPressMsg(tea.Key{Code: 'a', Text: "ac"}),
				tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}),
				tea.KeyPressMsg(tea.Key{Code: 'b', Text: "b"}),
			} {
				next, _ = m.Update(msg)
				m = next.(model)
			}
			require.Equal(t, "abc", m.input.Value(), "input must edit at the cursor, not append every key")
			view := m.View()
			require.LessOrEqual(t, lipgloss.Width(view.Content), size.Width)
			require.LessOrEqual(t, lipgloss.Height(view.Content), size.Height)
			require.NotNil(t, view.Cursor)
			require.Less(t, view.Cursor.Position.X, size.Width)
			require.Less(t, view.Cursor.Position.Y, size.Height)
		})
	}
}

func TestRegexErrorsDoNotSelect(t *testing.T) {
	m := newModel([]state.Reference{{ID: "one", Name: "prod/one"}}, true)
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '[', Text: "["}))
	m = next.(model)
	require.Error(t, m.err)
	require.Contains(t, m.View().Content, "invalid regex")
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.Nil(t, next.(model).selected)
	require.Nil(t, cmd)
	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	m = next.(model)
	next, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	require.Equal(t, "one", next.(model).selected.ID)
}

func TestResizeAndControlCharacters(t *testing.T) {
	m := newModel([]state.Reference{{ID: "one\x1b", Name: "prod\x1b/one"}}, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 6})
	m = next.(model)
	require.Equal(t, 2, m.height)
	require.NotContains(t, m.View().Content, "prod\x1b/one")
	require.Contains(t, plainView(m), "prod?/one [one?]")
}

func TestBottomAnchoredResults(t *testing.T) {
	refs := []state.Reference{
		{ID: "gke.test:projects/dev/locations/region/clusters/dev", Name: "gke.test/gke_dev_region_dev", Context: "dev"},
		{ID: "rancher.test:c-m-prod", Name: "rancher.test/prod", Context: "prod"},
		{ID: "staging", Name: "staging"},
	}

	for _, tt := range []struct {
		name    string
		matches []state.Reference
		rows    []string
		status  string
	}{
		{
			name:    "full",
			matches: refs,
			rows: []string{
				"  staging [staging]",
				"  prod [rancher.test:c-m-prod]",
				"> dev [gke.test:projects/dev/locations/region/clusters/dev]",
			},
			status: "3 matches | mode: fuzzy | tab: fuzzy/regex",
		},
		{
			name:    "one",
			matches: refs[:1],
			rows:    []string{"", "", "> dev [gke.test:projects/dev/locations/region/clusters/dev]"},
			status:  "1 matches | mode: fuzzy | tab: fuzzy/regex",
		},
		{
			name:   "empty",
			rows:   []string{"", "", ""},
			status: "0 matches | mode: fuzzy | tab: fuzzy/regex",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(tt.matches, false)
			m.height = 3
			expected := strings.Join(append(tt.rows, "", ">", "", tt.status), "\n")
			require.Equal(t, expected, plainView(m))
		})
	}
}

func TestBottomUpNavigationAndScrolling(t *testing.T) {
	refs := []state.Reference{
		{ID: "one", Name: "one"},
		{ID: "two", Name: "two"},
		{ID: "three", Name: "three"},
	}
	m := newModel(refs, false)
	m.height = 2
	require.True(t, strings.HasPrefix(plainView(m), "  two [two]\n> one [one]\n"))

	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = next.(model)
	require.Equal(t, 1, m.cursor)
	require.True(t, strings.HasPrefix(plainView(m), "> two [two]\n  one [one]\n"))

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = next.(model)
	require.Equal(t, 2, m.cursor)
	require.True(t, strings.HasPrefix(plainView(m), "> three [three]\n  two [two]\n"))

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = next.(model)
	require.Equal(t, 1, m.cursor)
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	require.NotNil(t, cmd)
	require.Equal(t, "two", next.(model).selected.ID)
}

func TestFooterStaysAnchoredWhenFiltering(t *testing.T) {
	refs := []state.Reference{{ID: "one", Name: "prod/one", Context: "one"}}
	m := newModel(refs, true)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = next.(model)
	lines := strings.Split(plainView(m), "\n")
	require.Len(t, lines, 8)
	require.Equal(t, ">", lines[5])
	require.Empty(t, lines[6])
	require.Equal(t, "1 matches | mode: regex | tab: fuzzy/regex", lines[7])

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'z', Text: "z"}))
	m = next.(model)
	lines = strings.Split(plainView(m), "\n")
	require.Len(t, lines, 8)
	require.Equal(t, "> z", lines[5])
	require.Empty(t, lines[6])
	require.Equal(t, "0 matches | mode: regex | tab: fuzzy/regex", lines[7])

	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '[', Text: "["}))
	m = next.(model)
	lines = strings.Split(plainView(m), "\n")
	require.Len(t, lines, 8)
	require.Equal(t, "> z[", lines[5])
	require.Empty(t, lines[6])
	require.Contains(t, lines[7], "invalid regex")
	require.Contains(t, lines[7], "mode: regex")
}

func TestCachedProviderLabelsPreserveConnectionData(t *testing.T) {
	for _, tt := range []struct {
		name  string
		ref   state.Reference
		label string
	}{
		{
			name: "rancher",
			ref: state.Reference{
				ID: "rancher.test:c-m-opaque", Store: "rancher.test",
				Name: "production", Context: "c-m-opaque",
				Metadata: map[string]string{"cluster": "c-m-opaque"},
			},
			label: "production",
		},
		{
			name: "prefixed rancher",
			ref: state.Reference{
				ID: "rancher.test:c-m-opaque", Store: "rancher.test",
				Name: "rancher.test/production", Context: "c-m-opaque",
				Metadata: map[string]string{"cluster": "c-m-opaque"},
			},
			label: "production",
		},
		{
			name: "gke",
			ref: state.Reference{
				ID:    "gke.test:projects/project/locations/region/clusters/cluster-dev",
				Store: "gke.test", Name: "gke.test/gke_project_region_cluster-dev",
				Context:  "gke_project_region_cluster-dev",
				Metadata: map[string]string{"resource": "projects/project/locations/region/clusters/cluster-dev"},
			},
			label: "cluster-dev",
		},
		{
			name: "gke resource ID without metadata",
			ref: state.Reference{
				ID:    "gke.test:projects/project/locations/region/clusters/cluster-dev",
				Store: "gke.test", Name: "gke.test/gke_project_region_cluster-dev",
				Context: "gke_project_region_cluster-dev",
			},
			label: "cluster-dev",
		},
		{
			name: "filesystem preserves source context",
			ref: state.Reference{
				ID: "filesystem.test:source", Store: "filesystem.test",
				Name:    "filesystem.test/gke_project_region_cluster-dev",
				Context: "gke_project_region_cluster-dev",
			},
			label: "gke_project_region_cluster-dev",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			refs := []state.Reference{tt.ref}
			m := newModel(refs, false)
			m.height, m.width = 1, 160
			require.Contains(t, plainView(m), "> "+tt.label+" ["+tt.ref.ID+"]\n")

			next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
			require.NotNil(t, cmd)
			require.Equal(t, tt.ref, *next.(model).selected)
		})
	}
}

func TestSearchingProviderLabelsAndQualifiers(t *testing.T) {
	refs := []state.Reference{
		{
			ID: "gke.test:projects/project-a/locations/region/clusters/cluster-dev", Store: "gke.test",
			Name: "gke.test/gke_project-a_region_cluster-dev", Context: "gke_project-a_region_cluster-dev",
			Metadata: map[string]string{"resource": "projects/project-a/locations/region/clusters/cluster-dev"},
		},
		{
			ID: "gke.test:projects/project-b/locations/region/clusters/cluster-dev", Store: "gke.test",
			Name: "gke.test/gke_project-b_region_cluster-dev", Context: "gke_project-b_region_cluster-dev",
			Metadata: map[string]string{"resource": "projects/project-b/locations/region/clusters/cluster-dev"},
		},
	}
	for _, tt := range []struct {
		query, mode string
		want        []state.Reference
	}{
		{query: "^cluster-dev$", mode: "regex", want: refs},
		{query: "project-b", mode: "fuzzy", want: refs[1:]},
		{query: "clusters/cluster-dev$", mode: "regex", want: refs},
		{query: "^gke.test/gke_project-a_region_cluster-dev$", mode: "regex", want: refs[:1]},
	} {
		t.Run(tt.mode+"/"+tt.query, func(t *testing.T) {
			m := newModel(refs, tt.mode == "regex")
			m.height = 2
			for _, r := range tt.query {
				next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
				m = next.(model)
			}

			require.NoError(t, m.err)
			require.Equal(t, tt.want, m.matches)
		})
	}
}
