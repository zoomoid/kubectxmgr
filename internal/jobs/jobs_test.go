package jobs

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testOptions(t *testing.T, platform string) Options {
	t.Helper()
	home := t.TempDir()
	return Options{Platform: platform, Home: home, Executable: filepath.Join(home, `a space & "quote"/switcher`), Config: filepath.Join(home, "config.yaml"), State: filepath.Join(home, "state"), Path: "/usr/bin:/bin", Interval: 15 * time.Minute, UID: 501}
}

func TestTemplateEscapingAndInstallation(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			o := testOptions(t, platform)
			files, err := Templates(o)
			require.NoError(t, err)
			if platform == "darwin" {
				decoder := xml.NewDecoder(strings.NewReader(files[0].Content))
				for {
					_, err := decoder.Token()
					if err != nil {
						require.Equal(t, "EOF", err.Error())
						break
					}
				}
				require.Contains(t, files[0].Content, "&amp;")
			} else {
				require.Contains(t, files[0].Content, `\"quote\"`)
			}
			var calls []string
			run := func(_ context.Context, name string, args ...string) ([]byte, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				return []byte("active"), nil
			}
			require.NoError(t, Install(t.Context(), o, false, false, run))
			require.Empty(t, calls)
			require.NoError(t, Install(t.Context(), o, true, false, run))
			require.NotEmpty(t, calls)
			for _, f := range files {
				b, err := os.ReadFile(f.Path)
				require.NoError(t, err)
				require.Equal(t, f.Content, string(b))
			}
			require.NoError(t, Uninstall(t.Context(), o, run))
			for _, f := range files {
				_, err := os.Stat(f.Path)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestUnownedAndActivationFailure(t *testing.T) {
	o := testOptions(t, "linux")
	files, err := Templates(o)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(files[0].Path), 0700))
	require.NoError(t, os.WriteFile(files[0].Path, []byte("unrelated"), 0600))
	fail := func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("manager unavailable")
	}

	require.ErrorContains(t, Install(t.Context(), o, true, false, fail), "unrelated")
	require.ErrorContains(t, Uninstall(t.Context(), o, fail), "unrelated")
	require.ErrorContains(t, Install(t.Context(), o, true, true, fail), "reload failed")
	ours, err := owned(files[0].Path)
	require.NoError(t, err)
	require.True(t, ours)
}

func TestEnvironmentAndLaunchQueryFailures(t *testing.T) {
	o := testOptions(t, "darwin")
	o.Environment = map[string]string{"AZURE_CLIENT_SECRET": "must-not-write"}
	_, err := Templates(o)
	require.ErrorContains(t, err, "non-secret allowlist")
	o.Environment = map[string]string{"AWS_PROFILE": "test & dev"}
	files, err := Templates(o)
	require.NoError(t, err)
	require.Contains(t, files[0].Content, "test &amp; dev")
	fail := func(context.Context, string, ...string) ([]byte, error) {
		return []byte("permission denied"), errors.New("query failed")
	}
	require.ErrorContains(t, Install(t.Context(), o, true, false, fail), "query existing")
	_, err = os.Stat(files[0].Path)
	require.ErrorIs(t, err, os.ErrNotExist)
}
