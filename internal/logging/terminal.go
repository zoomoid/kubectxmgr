package logging

import (
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// IsTerminal reports whether a writer exposes an interactive terminal descriptor.
func IsTerminal(w io.Writer) bool {
	file, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(file.Fd()))
}

// Muted styles terminal diagnostics without adding escape codes to redirected output.
func Muted(w io.Writer, text string) string {
	if !IsTerminal(w) || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return text
	}

	return lipgloss.NewStyle().Faint(true).Render(text)
}
