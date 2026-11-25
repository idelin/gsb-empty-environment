// Package zoneauth implements an importable authenticated patch verification
// component for a name cache host.
//
// A zone is represented as a label-indexed trie whose root is committed with
// a real Ed25519 signature. Clients only retain the authorization answers and
// partial proofs they obtained in the previous round. A zone manager issues a
// finite Patch (added/removed delegations, wildcard template changes and/or an
// old-key handoff) that authenticates both the old and new roots. The verifier
// never treats an absent sibling as an empty tree: every negative conclusion
// must be backed by a committed node whose children map proves the edge is
// missing, and every missing piece of evidence surfaces as Pending instead.
package zoneauth

import "strings"

// Name is a zone-relative domain name expressed as labels in apex-first
// order. The zero-length Name denotes the zone apex. All ancestry checks in
// this package compare whole labels, never raw string prefixes.
type Name []string

const labelSep = "\x00"

// Key returns a binary-safe map key for the name.
func (n Name) Key() string { return strings.Join(n, labelSep) }

// Parent returns the name without its final label. The apex has no parent.
func (n Name) Parent() Name {
	if len(n) == 0 {
		return nil
	}
	return append(Name{}, n[:len(n)-1]...)
}

// With returns the child name n + label.
func (n Name) With(label string) Name {
	child := make(Name, 0, len(n)+1)
	child = append(child, n...)
	return append(child, label)
}

// Equal reports whether two names contain the same labels.
func (n Name) Equal(other Name) bool {
	if len(n) != len(other) {
		return false
	}
	for i := range n {
		if n[i] != other[i] {
			return false
		}
	}
	return true
}

// HasPrefix reports whether n is equal to ancestor or lives inside it. The
// comparison is label structural: Name{"alpha"} is not an ancestor of
// Name{"alphabet"}.
func (n Name) HasPrefix(ancestor Name) bool {
	if len(ancestor) > len(n) {
		return false
	}
	for i := range ancestor {
		if n[i] != ancestor[i] {
			return false
		}
	}
	return true
}

// Clone returns an independent copy of the name.
func (n Name) Clone() Name { return append(Name{}, n...) }
