// Package preview wires the streaming unfold budget into the attachment
// preview service. It is a thin, importable adapter: the service hands over
// the untrusted attachment node tree and receives preview entries only when the
// whole tree was decoded within budget, with no partial state on failure.
package preview
