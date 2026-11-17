// Package logging configures structured diagnostics independently of command output.
package logging

import (
	"io"
	"log/slog"
)

// New writes text or JSON diagnostics to w, with debug verbosity enabled on request.
func New(w io.Writer, debug, json bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	options := &slog.HandlerOptions{Level: level}
	if json {
		return slog.New(slog.NewJSONHandler(w, options))
	}

	return slog.New(slog.NewTextHandler(w, options))
}
