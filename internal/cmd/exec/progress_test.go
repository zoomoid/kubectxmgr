package exec

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/stretchr/testify/require"
)

func TestProgressDoesNotPolluteRedirectedOutput(t *testing.T) {
	var out bytes.Buffer
	progress := &refreshProgress{out: &out}
	require.NoError(t, progress.start())
	require.NoError(t, progress.finish())
	require.Empty(t, out.String())
}

func TestProgressPreservesWarnings(t *testing.T) {
	var out bytes.Buffer
	progress := &refreshProgress{out: &out, active: true}
	_, err := progress.Write([]byte("warning\n"))
	require.NoError(t, err)
	require.NoError(t, progress.finish())
	require.Equal(t, "\r\x1b[2Kwarning\n", out.String(), "finishing must not erase a warning that replaced progress")
}

func TestProgressAnimatesSpinnerAndStopsBeforeWarnings(t *testing.T) {
	var out bytes.Buffer
	progress := &refreshProgress{
		out: &out, active: true, width: 80,
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot)),
	}
	require.NoError(t, progress.draw())
	initial := out.String()
	out.Reset()
	require.True(t, progress.advance())
	require.NotEqual(t, initial, out.String(), "the spinner frame must change")
	require.Contains(t, out.String(), " Refreshing state")

	_, err := progress.Write([]byte("warning\n"))
	require.NoError(t, err)
	before := out.String()
	require.False(t, progress.advance())
	require.NoError(t, progress.finish())
	require.Equal(t, before, out.String(), "animation and cleanup must not overwrite warnings")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestProgressReportsAnimationErrors(t *testing.T) {
	progress := &refreshProgress{out: failingWriter{}, active: true, width: 80, spinner: spinner.New()}
	require.False(t, progress.advance())
	require.True(t, errors.Is(progress.finish(), io.ErrClosedPipe))
}
