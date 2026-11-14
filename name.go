package netsec

import "strings"

// Canonical returns the canonical form of a DNS name: lower case and
// absolute (fully qualified, trailing dot). "" and "." both denote the
// root. All cache keys, scope comparisons and hint deduplication operate
// on canonical names so that case variations and different encodings of
// the same name (e.g. via compression pointers on the wire) collapse
// into a single form.
func Canonical(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return "."
	}
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	return strings.ToLower(name)
}

// IsSubdomain reports whether name is at or below apex. Names need not be
// canonical; both are canonicalized first. The root is an ancestor of
// every name.
func IsSubdomain(apex, name string) bool {
	apex, name = Canonical(apex), Canonical(name)
	if apex == "." {
		return true
	}
	if name == apex {
		return true
	}
	return strings.HasSuffix(name, "."+apex)
}

// strictlyBelow reports whether name is a proper subdomain of apex.
func strictlyBelow(apex, name string) bool {
	return Canonical(name) != Canonical(apex) && IsSubdomain(apex, name)
}

// rewriteSuffix replaces the oldApex suffix of name with newApex. It
// implements the name substitution a DNAME record performs.
func rewriteSuffix(name, oldApex, newApex string) string {
	oldApex, newApex = Canonical(oldApex), Canonical(newApex)
	prefix := strings.TrimSuffix(Canonical(name), oldApex)
	return Canonical(prefix + newApex)
}
