package metatx

// Action decides what happens to the source attributes of one namespace
// when content is promoted from the upload area to the publish area.
type Action int

const (
	// Drop removes the namespace's attributes. It is the zero value so
	// that namespaces without an explicit policy entry are never
	// inherited silently.
	Drop Action = iota
	// Preserve copies the namespace's attributes unchanged.
	Preserve
	// Resign re-issues the namespace's attributes through the Signer,
	// binding each value to the final content digest.
	Resign
)

// Policy maps an attribute namespace to its Action. Namespaces without an
// entry default to Drop.
type Policy map[string]Action

// action returns the configured action for ns, defaulting to Drop.
func (p Policy) action(ns string) Action {
	if a, ok := p[ns]; ok {
		return a
	}
	return Drop
}
