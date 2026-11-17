// Package shared provides inventory identity construction for provider implementations.
package shared

import (
	"github.com/zoomoid/kubectxmgr/internal/config"
	"github.com/zoomoid/kubectxmgr/internal/state"
)

// Reference namespaces stable provider identity by store, independently of display prefixes.
func Reference(s config.Store, id, name string, metadata map[string]string) state.Reference {
	display := name
	if s.ShowPrefix == nil || *s.ShowPrefix {
		display = s.Key() + "/" + name
	}

	return state.Reference{ID: s.Key() + ":" + id, Store: s.Key(), Name: display, Context: name, Metadata: metadata}
}
