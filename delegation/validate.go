package delegation

import (
	"fmt"
	"strings"
	"time"
)

// ReasonCode identifies the root cause of an invalid verdict.
type ReasonCode int

const (
	ReasonOK ReasonCode = iota
	// ReasonGrantNotFound: the queried grant generation does not exist.
	ReasonGrantNotFound
	// ReasonParentMissing: a grant references a parent generation that
	// was never issued (broken delegation chain).
	ReasonParentMissing
	// ReasonRevoked: the grant (or an ancestor) was revoked at or
	// before the query time.
	ReasonRevoked
	// ReasonNotYetValid: query time is before the grant's NotBefore.
	ReasonNotYetValid
	// ReasonExpired: query time is after the grant's NotAfter.
	ReasonExpired
	// ReasonActionExceedsParent: child actions are not a subset of a parent.
	ReasonActionExceedsParent
	// ReasonResourceExceedsParent: child resources are not a subset of a parent.
	ReasonResourceExceedsParent
	// ReasonValidityExceedsParent: child validity window escapes a parent.
	ReasonValidityExceedsParent
	// ReasonCyclicDelegation: the delegation graph contains a cycle.
	ReasonCyclicDelegation
)

func (c ReasonCode) String() string {
	switch c {
	case ReasonOK:
		return "ok"
	case ReasonGrantNotFound:
		return "grant not found"
	case ReasonParentMissing:
		return "parent grant missing"
	case ReasonRevoked:
		return "grant revoked"
	case ReasonNotYetValid:
		return "grant not yet valid"
	case ReasonExpired:
		return "grant expired"
	case ReasonActionExceedsParent:
		return "actions exceed parent grant"
	case ReasonResourceExceedsParent:
		return "resources exceed parent grant"
	case ReasonValidityExceedsParent:
		return "validity window exceeds parent grant"
	case ReasonCyclicDelegation:
		return "cyclic delegation"
	}
	return "unknown"
}

// Verdict is the outcome of validating a grant at a point in time.
// When Valid is false, Chain is the shortest path of grant references
// from the queried grant up to the ancestor (or the grant itself)
// where Reason applies, and Detail is a human-readable explanation.
type Verdict struct {
	Valid  bool
	Reason ReasonCode
	Chain  []GrantRef
	Detail string
}

func (v Verdict) String() string {
	if v.Valid {
		return "valid"
	}
	parts := make([]string, len(v.Chain))
	for i, ref := range v.Chain {
		parts[i] = ref.String()
	}
	return fmt.Sprintf("invalid: %s (%s) via %s", v.Reason, v.Detail, strings.Join(parts, " -> "))
}

// path is a chain of refs from the queried grant toward a root.
type path []GrantRef

func (p path) last() GrantRef { return p[len(p)-1] }

func (p path) contains(ref GrantRef) bool {
	for _, r := range p {
		if r == ref {
			return true
		}
	}
	return false
}

// Validate checks whether ref is usable at time t and, if not, returns
// the shortest invalidation reason chain. A grant is usable iff:
//
//   - the exact generation exists and is neither revoked nor outside
//     its validity window at t;
//   - for every parent: the parent generation exists, the grant is a
//     strict attenuation of it (actions, resources, validity window),
//     and the parent is itself valid at t (recursively).
//
// Because a grant may delegate from several parents, invalidity can
// have several explanations; breadth-first search guarantees the
// reported chain is a shortest one.
func (s *Store) Validate(ref GrantRef, t time.Time) Verdict {
	type frame struct {
		p path
	}
	queue := []frame{{p: path{ref}}}
	visited := map[GrantRef]bool{}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		head := cur.p.last()
		if visited[head] {
			continue
		}
		visited[head] = true

		s.mu.RLock()
		g, ok := s.get(head)
		s.mu.RUnlock()

		if !ok {
			reason := ReasonGrantNotFound
			if len(cur.p) > 1 {
				reason = ReasonParentMissing
			}
			return Verdict{
				Valid:  false,
				Reason: reason,
				Chain:  append([]GrantRef(nil), cur.p...),
				Detail: fmt.Sprintf("grant %s was never issued", head),
			}
		}

		if v, bad := localFailure(g, cur.p, t); bad {
			return v
		}

		for _, parent := range g.Parents {
			s.mu.RLock()
			pg, parentKnown := s.get(parent)
			s.mu.RUnlock()
			if parentKnown {
				if v, bad := attenuationFailure(g, pg, cur.p); bad {
					return v
				}
			}
			if cur.p.contains(parent) {
				return Verdict{
					Valid:  false,
					Reason: ReasonCyclicDelegation,
					Chain:  append(append([]GrantRef(nil), cur.p...), parent),
					Detail: fmt.Sprintf("grant %s delegates in a cycle through %s", head, parent),
				}
			}
			next := append(append([]GrantRef(nil), cur.p...), parent)
			queue = append(queue, frame{p: next})
		}
	}
	return Verdict{Valid: true, Reason: ReasonOK}
}

// localFailure checks conditions that depend only on the grant itself.
func localFailure(g *Grant, p path, t time.Time) (Verdict, bool) {
	fail := func(reason ReasonCode, detail string) (Verdict, bool) {
		return Verdict{
			Valid:  false,
			Reason: reason,
			Chain:  append([]GrantRef(nil), p...),
			Detail: detail,
		}, true
	}
	if g.revokedAt(t) {
		return fail(ReasonRevoked,
			fmt.Sprintf("grant %s revoked at %s", g.Ref(), g.RevokedAt.UTC().Format(time.RFC3339)))
	}
	if t.Before(g.NotBefore) {
		return fail(ReasonNotYetValid,
			fmt.Sprintf("grant %s not valid before %s", g.Ref(), g.NotBefore.UTC().Format(time.RFC3339)))
	}
	if t.After(g.NotAfter) {
		return fail(ReasonExpired,
			fmt.Sprintf("grant %s expired at %s", g.Ref(), g.NotAfter.UTC().Format(time.RFC3339)))
	}
	return Verdict{}, false
}

// attenuationFailure checks that g stays within the bounds of parent.
// Missing parents are not handled here; the BFS reports them as
// ReasonParentMissing when it visits the parent reference.
func attenuationFailure(g *Grant, parent *Grant, p path) (Verdict, bool) {
	fail := func(reason ReasonCode, detail string) (Verdict, bool) {
		return Verdict{
			Valid:  false,
			Reason: reason,
			Chain:  append([]GrantRef(nil), p...),
			Detail: detail,
		}, true
	}
	if !subsetOf(g.Actions, parent.Actions) {
		return fail(ReasonActionExceedsParent,
			fmt.Sprintf("grant %s actions %v not covered by parent %s", g.Ref(), g.Actions, parent.Ref()))
	}
	if !subsetOf(g.Resources, parent.Resources) {
		return fail(ReasonResourceExceedsParent,
			fmt.Sprintf("grant %s resources %v not covered by parent %s", g.Ref(), g.Resources, parent.Ref()))
	}
	if g.NotBefore.Before(parent.NotBefore) || g.NotAfter.After(parent.NotAfter) {
		return fail(ReasonValidityExceedsParent,
			fmt.Sprintf("grant %s validity [%s, %s] escapes parent %s window [%s, %s]",
				g.Ref(),
				g.NotBefore.UTC().Format(time.RFC3339), g.NotAfter.UTC().Format(time.RFC3339),
				parent.Ref(), parent.NotBefore.UTC().Format(time.RFC3339), parent.NotAfter.UTC().Format(time.RFC3339)))
	}
	return Verdict{}, false
}

// subsetOf reports whether every child item is allowed by the parent
// set; "*" in the parent set is a wildcard.
func subsetOf(child, parent []string) bool {
	for _, c := range child {
		if !allowed(parent, c) {
			return false
		}
	}
	return true
}

func allowed(parent []string, v string) bool {
	for _, p := range parent {
		if p == "*" || p == v {
			return true
		}
	}
	return false
}
