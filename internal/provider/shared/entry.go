package shared

import "github.com/zoomoid/kubectxmgr/internal/state"

// Entry separates provider-specific presentation from the stored connection data.
type Entry interface {
	// Label returns the provider's readable name, independent of context keys.
	Label() string

	// Reference retains the stable identity and metadata needed for selection.
	Reference() state.Reference
}

// ReferenceEntry preserves an inventory reference and supplies the default label.
type ReferenceEntry struct {
	reference state.Reference
}

// NewEntry wraps a reference without changing its identity or connection fields.
func NewEntry(ref state.Reference) ReferenceEntry {
	return ReferenceEntry{reference: ref}
}

// Label uses the context name, or the inventory name for entries such as namespaces.
func (e ReferenceEntry) Label() string {
	if e.reference.Context != "" {
		return e.reference.Context
	}

	return e.reference.Name
}

// Reference returns the original inventory data used when selecting a connection.
func (e ReferenceEntry) Reference() state.Reference {
	return e.reference
}
