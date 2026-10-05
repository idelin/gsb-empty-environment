package main

import (
	"fmt"
	"sort"
)

// DepEdge is a dependence between two instructions of the original
// sequence: Src must execute before Dst.
type DepEdge struct {
	Src, Dst int      // indices into the original sequence
	Kinds    []string // any of "RAW", "WAR", "WAW", "BAR"
	Loc      string   // a conflicting location, for reporting
}

func firstOverlap(writes, refs []Ref) (Ref, bool) {
	for _, w := range writes {
		for _, r := range refs {
			if w.overlaps(r) {
				return w, true
			}
		}
	}
	return Ref{}, false
}

// edgeBetween computes the dependence edge a -> b (a before b in the
// original program), or nil if the two instructions are independent.
func edgeBetween(a, b *Instr) *DepEdge {
	// A fence orders every instruction before it against every
	// instruction after it (and fences against each other).
	if a.Op == OpFence || b.Op == OpFence {
		return &DepEdge{Src: a.Index, Dst: b.Index, Kinds: []string{"BAR"}, Loc: "fence"}
	}
	e := &DepEdge{Src: a.Index, Dst: b.Index}
	if loc, ok := firstOverlap(a.Writes, b.Reads); ok {
		e.Kinds = append(e.Kinds, "RAW")
		e.Loc = loc.String()
	}
	if loc, ok := firstOverlap(a.Reads, b.Writes); ok {
		e.Kinds = append(e.Kinds, "WAR")
		if e.Loc == "" {
			e.Loc = loc.String()
		}
	}
	if loc, ok := firstOverlap(a.Writes, b.Writes); ok {
		e.Kinds = append(e.Kinds, "WAW")
		if e.Loc == "" {
			e.Loc = loc.String()
		}
	}
	if len(e.Kinds) == 0 {
		return nil
	}
	return e
}

// BuildEdges returns all dependence edges of the original sequence in
// program order (sorted by Src, then Dst).
func BuildEdges(orig []*Instr) []*DepEdge {
	var edges []*DepEdge
	for i := 0; i < len(orig); i++ {
		for j := i + 1; j < len(orig); j++ {
			if e := edgeBetween(orig[i], orig[j]); e != nil {
				edges = append(edges, e)
			}
		}
	}
	return edges
}

// Violation is a dependence edge whose order the candidate sequence
// reverses.
type Violation struct {
	Edge *DepEdge
}

// Result summarizes the legality check.
type Result struct {
	PermErr    string      // non-empty if candidate is not a permutation
	Edges      int         // total dependence edges in the original
	Violations []Violation // violated edges, earliest first
	Sim        SimResult   // auxiliary exhaustive/simulated cross-check
}

// Legal reports whether the candidate is a legal reordering.
func (r *Result) Legal() bool {
	return r.PermErr == "" && len(r.Violations) == 0
}

// Check verifies that cand is a legal reordering of orig: same multiset
// of instructions, and every RAW/WAR/WAW dependence and barrier order
// of orig preserved.
func Check(orig, cand []*Instr) *Result {
	res := &Result{}

	if err := checkPermutation(orig, cand); err != "" {
		res.PermErr = err
	} else {
		pos := make(map[int]int, len(cand)) // original index -> candidate position
		for p, in := range cand {
			pos[in.Index] = p
		}
		edges := BuildEdges(orig)
		res.Edges = len(edges)
		for _, e := range edges {
			if pos[e.Src] > pos[e.Dst] {
				res.Violations = append(res.Violations, Violation{Edge: e})
			}
		}
		// Edges are generated in (Src, Dst) order, so the first
		// violation is the earliest conflicting edge in program order.
		sort.SliceStable(res.Violations, func(i, j int) bool {
			a, b := res.Violations[i].Edge, res.Violations[j].Edge
			if a.Src != b.Src {
				return a.Src < b.Src
			}
			return a.Dst < b.Dst
		})
	}

	// Auxiliary cross-check by executing both sequences over finite
	// initial states. A divergence proves illegality; agreement proves
	// nothing and never overrides the dependence analysis above.
	res.Sim = CrossCheck(orig, cand)
	return res
}

// checkPermutation verifies both sequences hold the same multiset of
// instructions. Candidate instructions are re-anchored to the original
// indices so positions can be compared.
func checkPermutation(orig, cand []*Instr) string {
	if len(orig) != len(cand) {
		return fmt.Sprintf("length mismatch: original has %d instructions, candidate has %d",
			len(orig), len(cand))
	}
	avail := make(map[string][]int) // canonical text -> original indices
	for _, in := range orig {
		avail[in.Text] = append(avail[in.Text], in.Index)
	}
	for _, in := range cand {
		ids := avail[in.Text]
		if len(ids) == 0 {
			return fmt.Sprintf("candidate instruction #%d %q has no counterpart in the original",
				in.Index, in.Text)
		}
		in.Index = ids[0]
		avail[in.Text] = ids[1:]
	}
	return ""
}
