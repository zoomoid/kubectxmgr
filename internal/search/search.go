// Package search matches inventory display names and resolves unambiguous selections.
package search

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/sahilm/fuzzy"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

// Match supports ranked fuzzy matching, RE2 regex, and full-name glob matching.
// Globs match across path separators; regex/glob results are sorted by stable ID.
func Match(refs []state.Reference, pattern, mode string) ([]state.Reference, error) {
	if mode == "fuzzy" {
		if pattern == "" {
			return append([]state.Reference(nil), refs...), nil
		}

		names := make([]string, len(refs))
		for i, r := range refs {
			names[i] = r.Name
		}

		results := fuzzy.Find(pattern, names)
		var found []state.Reference
		for _, match := range results {
			found = append(found, refs[match.Index])
		}

		return found, nil
	}

	expression := pattern
	if mode == "glob" {
		var b strings.Builder
		b.WriteString("(?s)^")
		runes := []rune(pattern)
		for i := 0; i < len(runes); i++ {
			switch runes[i] {
			case '*':
				b.WriteString(".*")
			case '?':
				b.WriteString(".")
			case '\\':
				i++
				if i >= len(runes) {
					return nil, fmt.Errorf("invalid glob: trailing escape")
				}

				b.WriteString(regexp.QuoteMeta(string(runes[i])))
			case '[':
				end := i + 1
				for end < len(runes) && runes[end] != ']' {
					end++
				}

				if end == len(runes) || end == i+1 {
					return nil, fmt.Errorf("invalid glob character class")
				}

				class := string(runes[i+1 : end])
				if strings.HasPrefix(class, "!") {
					class = "^" + class[1:]
				}

				b.WriteString("[" + class + "]")
				i = end
			default:
				b.WriteString(regexp.QuoteMeta(string(runes[i])))
			}
		}

		b.WriteString("$")
		expression = b.String()
	} else if mode != "regex" {
		return nil, fmt.Errorf("unsupported search mode %q", mode)
	}

	re, err := regexp.Compile(expression)
	if err != nil {
		return nil, fmt.Errorf("invalid %s pattern: %w", mode, err)
	}

	var result []state.Reference
	for _, r := range refs {
		if re.MatchString(r.Name) {
			result = append(result, r)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })

	return result, nil
}

// Exact prefers a stable ID, otherwise requiring one display-name or context match.
// Ambiguous short names must be replaced with a unique ID from list-contexts.
func Exact(refs []state.Reference, name string) (state.Reference, error) {
	var matches []state.Reference
	for _, r := range refs {
		if r.ID == name {
			return r, nil
		}

		if r.Name == name || r.Context == name {
			matches = append(matches, r)
		}
	}

	if len(matches) != 1 {
		return state.Reference{}, fmt.Errorf("context %q has %d matches; use the full unique ID from list-contexts", name, len(matches))
	}

	return matches[0], nil
}
