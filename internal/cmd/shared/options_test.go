package shared

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

func TestSelectionReporting(t *testing.T) {
	const summary = "context=cluster namespace=development\nKUBECONFIG=/session/config\n"

	for _, tt := range []struct {
		name    string
		flags   []string
		wrapped bool
		want    string
	}{
		{name: "silent"},
		{name: "verbose", flags: []string{"--verbose"}, want: summary},
		{name: "short verbose", flags: []string{"-v"}, want: summary},
		{name: "machine", flags: []string{"--machine"}, want: "/session/config\n"},
		{name: "machine verbose", flags: []string{"--machine", "-v"}, want: "/session/config\n"},
		{name: "shell", wrapped: true},
		{name: "shell verbose", wrapped: true, flags: []string{"-v"}, want: summary},
	} {
		t.Run(tt.name, func(t *testing.T) {
			options := &Options{}
			command := &cobra.Command{
				Use: "test",
				RunE: func(cmd *cobra.Command, _ []string) error {
					return options.Report(cmd, "/session/config", state.Selection{
						Context: "cluster", Namespace: "development",
					})
				},
			}
			options.Bind(command)
			args := append([]string(nil), tt.flags...)
			response := filepath.Join(t.TempDir(), "response")
			if tt.wrapped {
				args = append(args, "--shell-result", response)
			}

			var output bytes.Buffer
			command.SetOut(&output)
			command.SetArgs(args)
			require.NoError(t, command.ExecuteContext(t.Context()))
			require.Equal(t, tt.want, output.String())

			if tt.wrapped {
				data, err := os.ReadFile(response)
				require.NoError(t, err)
				require.Equal(t, "set:/session/config", string(data))
			}
		})
	}
}
