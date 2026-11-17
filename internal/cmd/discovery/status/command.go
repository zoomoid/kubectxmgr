// Package status reports complete store snapshots and their last refresh attempts.
package status

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/shared"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

type entry struct {
	Store    string          `json:"store"`
	Snapshot *state.Snapshot `json:"snapshot,omitempty"`
	Attempt  *state.Status   `json:"attempt,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// New builds the JSON discovery status command without initiating refreshes.
func New(options *shared.Options) *cobra.Command {
	return &cobra.Command{
		Use:  "status",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			engine, err := options.Engine(cmd)
			if err != nil {
				return err
			}

			var entries []entry
			for _, store := range engine.Config.Stores {
				row := entry{Store: store.Key()}
				snapshot, err := engine.Disk.Snapshot(store.Key(), store.Fingerprint())
				if err != nil {
					row.Error = err.Error()
				} else {
					row.Snapshot = &snapshot
				}

				attempt, err := state.Read[state.Status](engine.Disk.Path("status", store.Key()))
				if err == nil {
					row.Attempt = &attempt
				} else if !errors.Is(err, os.ErrNotExist) {
					row.Error += "; " + err.Error()
				}

				entries = append(entries, row)
			}

			return json.NewEncoder(cmd.OutOrStdout()).Encode(entries)
		},
	}
}
