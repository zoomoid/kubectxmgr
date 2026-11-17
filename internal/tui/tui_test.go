package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

func TestRegexErrorsDoNotSelect(t *testing.T) {
	m := model{all: []state.Reference{{ID: "one", Name: "prod/one"}}, mode: "regex", height: 5}
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
	m := model{all: []state.Reference{{ID: "one\x1b", Name: "prod\x1b/one"}}, mode: "fuzzy", height: 5}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 2})
	m = next.(model)
	require.Equal(t, 1, m.height)
	require.NotContains(t, m.View().Content, "\x1b")
	require.Contains(t, m.View().Content, "prod?/one")
}
