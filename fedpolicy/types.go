package fedpolicy

import (
	"fmt"
	"strings"
)

// Label is a public tag carried by a signed principal.
//
// The target host only accepts labels listed in its small public label set;
// any other label may exist on old principals but must never appear in a
// projected allow rule.
type Label string

// Name is a fully qualified federated principal: a signed issuer domain, a
// tenant name within that domain, and an unbounded path of segments.
//
// The tuple (Issuer, Tenant) is the naming authority key. Two tenants with
// the same name but different issuers are distinct principals; this is why
// every rule and transducer is scoped by both fields.
type Name struct {
	Issuer string
	Tenant string
	Path   []string
	Labels []Label
}

// HeaderKey identifies a naming authority (issuer domain + tenant).
type HeaderKey struct {
	Issuer string
	Tenant string
}

// Header returns the authority key of the name.
func (n Name) Header() HeaderKey {
	return HeaderKey{Issuer: n.Issuer, Tenant: n.Tenant}
}

// PathEquals reports whether two path segment sequences are identical.
func PathEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// HasPrefix reports whether path starts with prefix.
// The empty prefix matches every path, including the empty path.
func HasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

// NamePrefix is a target- or old-side rule prefix bound to one authority.
type NamePrefix struct {
	Issuer string
	Tenant string
	Path   []string
}

// Header returns the authority key of the prefix.
func (p NamePrefix) Header() HeaderKey {
	return HeaderKey{Issuer: p.Issuer, Tenant: p.Tenant}
}

// Matches reports whether n falls under the prefix p (same authority and
// path prefix).
func (p NamePrefix) Matches(n Name) bool {
	return p.Issuer == n.Issuer && p.Tenant == n.Tenant && HasPrefix(n.Path, p.Path)
}

// String renders the prefix as issuer/tenant followed by the slash-joined
// path; an empty path renders with a trailing slash so it is distinguishable
// from a single-segment path.
func (p NamePrefix) String() string {
	return fmt.Sprintf("%s/%s/%s", p.Issuer, p.Tenant, strings.Join(p.Path, "/"))
}

// labelSet is a small finite set of labels backed by a map.
type labelSet map[Label]struct{}

func newLabelSet(labels []Label) labelSet {
	s := make(labelSet, len(labels))
	for _, l := range labels {
		s[l] = struct{}{}
	}
	return s
}

func (s labelSet) contains(l Label) bool {
	_, ok := s[l]
	return ok
}

func (s labelSet) slice() []Label {
	out := make([]Label, 0, len(s))
	for l := range s {
		out = append(out, l)
	}
	return out
}

// isSubset reports whether every label in s is present in other.
func (s labelSet) isSubset(other labelSet) bool {
	for l := range s {
		if !other.contains(l) {
			return false
		}
	}
	return true
}

// clonePath returns an independent copy of a path slice. Nil maps to nil so
// that the empty path stays empty and round-trips through the algorithms.
func clonePath(p []string) []string {
	if p == nil {
		return nil
	}
	out := make([]string, len(p))
	copy(out, p)
	return out
}
