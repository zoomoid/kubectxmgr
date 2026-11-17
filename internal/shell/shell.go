// Package shell renders integration that changes the invoking shell's environment.
package shell

import (
	"fmt"
	"strings"
)

// Init renders integration for bash, zsh, or fish using a safely quoted executable.
// It returns shell source without executing it or modifying startup files.
func Init(name, executable string) (string, error) {
	if strings.ContainsAny(executable, "\r\n\x00") {
		return "", fmt.Errorf("executable path contains control characters")
	}

	quotedExecutable := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	switch name {
	case "bash", "zsh":
		return fmt.Sprintf(posixTemplate, quotedExecutable), nil
	case "fish":
		return fmt.Sprintf(fishTemplate, quotedExecutable), nil
	default:
		return "", fmt.Errorf("unsupported shell %q; use bash, zsh, or fish", name)
	}
}
