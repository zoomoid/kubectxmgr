package search

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

func TestMatchingAndAmbiguity(t *testing.T) {
	refs := []state.Reference{{ID: "2", Name: "eks/prod-west", Context: "prod"}, {ID: "1", Name: "gke/prod-east", Context: "prod"}}
	matches, err := Match(refs, "*prod-?est", "glob")
	require.NoError(t, err)
	require.Equal(t, []state.Reference{refs[0]}, matches)
	matches, err = Match(refs, `^(gke|eks)/prod-`, "regex")
	require.NoError(t, err)
	require.Equal(t, "1", matches[0].ID)
	matches, err = Match(refs, "epw", "fuzzy")
	require.NoError(t, err)
	require.Equal(t, "2", matches[0].ID)
	for _, mode := range []string{"glob", "regex"} {
		_, err = Match(refs, "[", mode)
		require.Error(t, err)
	}
	_, err = Exact(refs, "prod")
	require.ErrorContains(t, err, "2 matches")
	match, err := Exact(refs, "1")
	require.NoError(t, err)
	require.Equal(t, "gke/prod-east", match.Name)
}
