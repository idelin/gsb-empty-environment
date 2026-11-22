// Package takeover implements a small, importable control-taking protocol for
// a Go session host.
//
// A grant is fenced by a process start identity, a session root, an allowed
// signal set, a set of terminal dimensions, and a one-shot sequence set.
// Grants form a tree: a holder may delegate disjoint terminal dimensions to
// several children, and a child may delegate again. Returning a grant requires
// positive evidence for every active descendant in the returned scope, or a
// seal that covers every unconsumed sequence in that scope. A direct child's
// assertion about its own subtree is not sufficient.
//
// Sequence sets are normalized ranges. They never expand a large range into
// individual sequence values.
package takeover
