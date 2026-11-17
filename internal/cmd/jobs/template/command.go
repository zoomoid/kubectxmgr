// Package template renders native discovery job files without activation.
package template

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/zoomoid/kubectxmgr/internal/cmd/jobs/options"
	"github.com/zoomoid/kubectxmgr/internal/jobs"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

// fileTemplate labels the separate systemd sections when both are printed to stdout.
var fileTemplate = "# %s\n%s\n"

// New builds job rendering with optional standalone output files.
func New(options *options.Options) *cobra.Command {
	var outputDirectory string
	cmd := &cobra.Command{
		Use:  "template",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := options.Resolve(cmd, true)
			if err != nil {
				return err
			}

			files, err := jobs.Templates(o)
			if err != nil {
				return err
			}

			for _, file := range files {
				if outputDirectory != "" {
					if err := writeTemplate(outputDirectory, file); err != nil {
						return err
					}

					continue
				}

				if len(files) == 1 {
					_, err := io.WriteString(cmd.OutOrStdout(), file.Content)
					return err
				}

				if _, err := fmt.Fprintf(cmd.OutOrStdout(), fileTemplate, file.Path, file.Content); err != nil {
					return err
				}
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&outputDirectory, "output-directory", "", "Write separate native template files into a directory without activating jobs")

	return cmd
}

// writeTemplate writes one native definition without replacing an existing path.
func writeTemplate(directory string, file jobs.File) error {
	path := filepath.Join(directory, filepath.Base(file.Path))
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("template output file already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return state.Write(path, []byte(file.Content))
}
