package fd

import "fmt"

// ChangeKind identifies which normalization step produced a Change.
type ChangeKind string

const (
	// ChangeSplit records a multi-attribute RHS being decomposed.
	ChangeSplit ChangeKind = "split-rhs"
	// ChangeTrivial records a dependency dropped by reflexivity (RHS ⊆ LHS).
	ChangeTrivial ChangeKind = "drop-trivial"
	// ChangeExtraneous records a redundant left-hand attribute removal.
	ChangeExtraneous ChangeKind = "remove-extraneous-attribute"
	// ChangeRedundant records a redundant dependency removal.
	ChangeRedundant ChangeKind = "remove-redundant-dependency"
)

// Change is one auditable normalization action together with the
// derivation evidence justifying it.
type Change struct {
	Kind      ChangeKind
	Original  FD       // dependency before the change
	Result    FD       // dependency after the change (split/extraneous)
	Attribute string   // the attribute proved extraneous, if any
	Trace     []Step   // closure derivation witnessing the change
	TraceFrom []string // seed attributes of Trace
	Reason    string   // human-readable justification
}

// CoverResult pairs the canonical cover with the full audit trail.
type CoverResult struct {
	Cover   []FD
	Changes []Change
}

// CanonicalCover computes a minimal cover of fds:
//
//	1. split every RHS into single attributes (dropping trivial FDs);
//	2. remove extraneous left-hand attributes;
//	3. remove redundant dependencies.
//
// Steps 2 and 3 scan dependencies (and, inside step 2, left-hand
// attributes) in the deterministic order given by SortFDs, so the
// output is stable for any permutation of the input. Every deletion
// is justified by a closure trace stored in the returned Changes.
func CanonicalCover(fds []FD) CoverResult {
	var changes []Change

	// Step 1: decompose right-hand sides into single attributes.
	// Empty-LHS dependencies ({} -> A) survive untouched.
	seen := make(map[string]bool)
	var work []FD
	for _, f := range fds {
		for _, r := range f.RHS {
			single := NewFD(f.LHS, []string{r})
			if Contains(f.LHS, r) {
				changes = append(changes, Change{
					Kind:     ChangeTrivial,
					Original: single,
					Reason: fmt.Sprintf("%s is trivial: %s is already on the left (reflexivity)",
						single, r),
				})
				continue
			}
			if seen[single.Key()] {
				continue
			}
			seen[single.Key()] = true
			work = append(work, single)
			if len(f.RHS) > 1 {
				changes = append(changes, Change{
					Kind:     ChangeSplit,
					Original: f,
					Result:   single,
					Reason:   fmt.Sprintf("decompose %s by the union/decomposition rule", f),
				})
			}
		}
	}
	SortFDs(work)

	// Step 2: remove extraneous left-hand attributes. For alpha -> B
	// and a in alpha, a is extraneous when B in (alpha - a)+ computed
	// under the current working set (which already reflects earlier
	// replacements). Attributes are tried in sorted order.
	for i := 0; i < len(work); i++ {
		f := work[i]
		for _, a := range f.LHS {
			reduced := Remove(f.LHS, a)
			if len(reduced) == len(f.LHS) {
				continue
			}
			closure, trace := ClosureTrace(reduced, work)
			if !Contains(closure, f.RHS[0]) {
				continue
			}
			replacement := NewFD(reduced, f.RHS)
			changes = append(changes, Change{
				Kind:      ChangeExtraneous,
				Original:  f,
				Result:    replacement,
				Attribute: a,
				Trace:     trace,
				TraceFrom: reduced,
				Reason: fmt.Sprintf("%s is extraneous in %s: {%s}+ already yields %s",
					a, f, joinOrEmpty(reduced), f.RHS[0]),
			})
			work[i] = replacement
			f = replacement
		}
	}
	work = dedupeAndDropSubsumed(work)

	// Step 3: remove redundant dependencies in sorted order. f is
	// redundant when its RHS is reachable from its LHS using the
	// remaining dependencies only. Sequential removal keeps the
	// result equivalent even for cyclic sets (e.g. A->B, B->A with
	// A->C, B->C: exactly one of the C-dependencies survives).
	SortFDs(work)
	kept := make([]FD, 0, len(work))
	for i, f := range work {
		rest := make([]FD, 0, len(work)-1)
		rest = append(rest, kept...)
		rest = append(rest, work[i+1:]...)
		closure, trace := ClosureTrace(f.LHS, rest)
		if Contains(closure, f.RHS[0]) {
			changes = append(changes, Change{
				Kind:      ChangeRedundant,
				Original:  f,
				Trace:     trace,
				TraceFrom: f.LHS,
				Reason: fmt.Sprintf("%s is redundant: %s is derivable from {%s} without it",
					f, f.RHS[0], joinOrEmpty(f.LHS)),
			})
			continue
		}
		kept = append(kept, f)
	}
	SortFDs(kept)

	return CoverResult{Cover: kept, Changes: changes}
}

// dedupeAndDropSubsumed removes exact duplicates produced by step 2
// (e.g. AB -> C and AC -> C both reducing to A -> C).
func dedupeAndDropSubsumed(fds []FD) []FD {
	seen := make(map[string]bool, len(fds))
	out := make([]FD, 0, len(fds))
	for _, f := range fds {
		if seen[f.Key()] {
			continue
		}
		seen[f.Key()] = true
		out = append(out, f)
	}
	return out
}

func joinOrEmpty(attrs []string) string {
	if len(attrs) == 0 {
		return ""
	}
	out := attrs[0]
	for _, a := range attrs[1:] {
		out += "," + a
	}
	return out
}
