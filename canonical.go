package fd

import "fmt"

// ActionKind identifies the normalization step a log entry belongs to.
type ActionKind int

const (
	SplitRHS ActionKind = iota
	RemoveExtraneousAttr
	RemoveDuplicate
	RemoveRedundantFD
)

func (k ActionKind) String() string {
	switch k {
	case SplitRHS:
		return "split-rhs"
	case RemoveExtraneousAttr:
		return "remove-extraneous-lhs-attr"
	case RemoveDuplicate:
		return "remove-duplicate"
	case RemoveRedundantFD:
		return "remove-redundant-fd"
	}
	return "?"
}

// LogEntry records one normalization action together with the
// derivation evidence (a closure trace) that justifies it.
type LogEntry struct {
	Kind     ActionKind
	Detail   string
	Evidence Trace // non-empty only for deletions
}

// Report is the result of Normalize: the canonical cover plus the full
// ordered log of every action taken.
type Report struct {
	Canonical []FD
	Log       []LogEntry
}

// Normalize computes the canonical (minimal) cover of fds:
//
//	1. split every RHS into single attributes (done at parse time for
//	   ParseFDs input; repeated here for programmatic input);
//	2. remove extraneous left-hand attributes;
//	3. remove redundant dependencies.
//
// Steps 2 and 3 process FDs (and LHS attributes) in a fixed sorted
// order, so the resulting cover and log are stable across runs.
// Every deletion carries a closure trace as derivation evidence.
func Normalize(fds []FD) Report {
	var rep Report

	// Step 1: singleton RHS, dedup, then fix the processing order.
	var split []FD
	for _, f := range fds {
		f.LHS = NewAttrSet(f.LHS...)
		split = append(split, f)
	}
	split = dedupFDs(split)
	sortFDs(split)
	rep.Log = append(rep.Log, LogEntry{
		Kind:   SplitRHS,
		Detail: fmt.Sprintf("normalized to %d singleton-RHS dependencies", len(split)),
	})

	work := append([]FD{}, split...)

	// Step 2: extraneous LHS attributes. For each FD (in fixed order)
	// and each of its LHS attributes a (sorted), a is extraneous when
	// RHS is still derivable from LHS\{a} under the current set.
	for i := 0; i < len(work); i++ {
		for _, a := range append(AttrSet{}, work[i].LHS...) {
			rest := work[i].LHS.minus(a)
			closure, trace := Closure(rest, work)
			if closure.Contains(work[i].RHS) {
				rep.Log = append(rep.Log, LogEntry{
					Kind: RemoveExtraneousAttr,
					Detail: fmt.Sprintf("in (%s): attribute %q is extraneous; %s derives %s without it",
						work[i], a, rest, work[i].RHS),
					Evidence: trace,
				})
				work[i].LHS = rest
			}
		}
	}

	// Left reduction may have made two dependencies identical; drop
	// the duplicates so redundancy evidence never "proves" an FD by
	// an indistinguishable copy of itself.
	{
		seen := make(map[string]bool, len(work))
		deduped := make([]FD, 0, len(work))
		for _, f := range work {
			if seen[f.key()] {
				rep.Log = append(rep.Log, LogEntry{
					Kind:   RemoveDuplicate,
					Detail: fmt.Sprintf("(%s) duplicates another dependency after left reduction", f),
				})
				continue
			}
			seen[f.key()] = true
			deduped = append(deduped, f)
		}
		work = deduped
	}

	// Step 3: redundant FDs. In fixed order, f is redundant when its
	// RHS is derivable from its LHS using only the remaining FDs.
	// Cyclic pairs (A->B, B->A) survive: each needs the other.
	i := 0
	for i < len(work) {
		f := work[i]
		others := make([]FD, 0, len(work)-1)
		others = append(others, work[:i]...)
		others = append(others, work[i+1:]...)
		closure, trace := Closure(f.LHS, others)
		if closure.Contains(f.RHS) {
			rep.Log = append(rep.Log, LogEntry{
				Kind: RemoveRedundantFD,
				Detail: fmt.Sprintf("(%s) is redundant; %s derives %s from the remaining %d dependencies",
					f, f.LHS, f.RHS, len(others)),
				Evidence: trace,
			})
			work = others
			// do not advance i: the next FD shifted into position i
			continue
		}
		i++
	}

	// Re-sort the surviving FDs so the emitted cover has a canonical,
	// input-order-independent arrangement.
	sortFDs(work)
	rep.Canonical = work
	return rep
}
