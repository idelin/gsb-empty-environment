package hexgrid

import (
	"fmt"
	"sort"
)

// ViolationKind classifies integrity violations found by the Inspector.
type ViolationKind int

const (
	// ViolationDanglingRef: a declared parent/neighbor reference points
	// at a cell that does not exist in the store.
	ViolationDanglingRef ViolationKind = iota
	// ViolationIsolatedLoop: a connected component of >= 2 cells is
	// detached from the main component (classified as a closed loop
	// when every member has in-component degree 2).
	ViolationIsolatedLoop
	// ViolationDataSkew: record distribution across base cells exceeds
	// the configured max/mean ratio.
	ViolationDataSkew
	// ViolationInvariant: a record breaks a formal index invariant
	// (invalid bit layout, parent mismatch with the hierarchy, ...).
	ViolationInvariant
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationDanglingRef:
		return "dangling-reference"
	case ViolationIsolatedLoop:
		return "isolated-loop"
	case ViolationDataSkew:
		return "data-skew"
	case ViolationInvariant:
		return "invariant-breach"
	}
	return "unknown"
}

// Violation describes one integrity violation.
type Violation struct {
	Kind   ViolationKind
	Cell   Cell
	Detail string
}

func (v Violation) String() string {
	return fmt.Sprintf("[%-18s] cell=%-16s %s", v.Kind, v.Cell, v.Detail)
}

// Report is the result of one full offline integrity inspection.
type Report struct {
	Records       int
	Components    int
	MainComponent int
	BucketCounts  [NumBaseCells]int
	SkewRatio     float64
	Violations    []Violation
}

// OK reports whether the store passed every check.
func (r *Report) OK() bool { return len(r.Violations) == 0 }

// Summary renders a compact human-readable report.
func (r *Report) Summary() string {
	s := fmt.Sprintf("records=%d components=%d main-component=%d skew-ratio=%.2f violations=%d",
		r.Records, r.Components, r.MainComponent, r.SkewRatio, len(r.Violations))
	for _, v := range r.Violations {
		s += "\n  " + v.String()
	}
	return s
}

// Inspector runs full-state offline integrity checks over a Store.
type Inspector struct {
	// SkewThreshold is the maximum allowed max/mean bucket ratio;
	// <= 0 disables the skew check.
	SkewThreshold float64
	// MinRecordsForSkew is the minimum store size before skew is judged.
	MinRecordsForSkew int
}

// DefaultInspector returns an Inspector with standard thresholds.
func DefaultInspector() Inspector {
	return Inspector{SkewThreshold: 3.0, MinRecordsForSkew: 20}
}

// Inspect performs a full offline integrity pass over a consistent
// snapshot of the store. It is safe to run concurrently with Store use.
func (in Inspector) Inspect(s *Store) *Report {
	snap := s.Snapshot()
	rep := &Report{Records: len(snap)}

	present := make(map[Cell]Record, len(snap))
	for _, r := range snap {
		present[r.Cell] = r
	}

	// --- per-record formal constraints -------------------------------
	for _, r := range snap {
		if !r.Cell.IsValid() {
			rep.Violations = append(rep.Violations, Violation{
				Kind: ViolationInvariant, Cell: r.Cell,
				Detail: "record key violates cell bit-layout invariant",
			})
			continue
		}
		if r.Parent != 0 {
			switch {
			case !r.Parent.IsValid():
				rep.Violations = append(rep.Violations, Violation{
					Kind: ViolationInvariant, Cell: r.Cell,
					Detail: fmt.Sprintf("declared parent %s is not a valid cell", r.Parent),
				})
			default:
				if _, ok := present[r.Parent]; !ok {
					rep.Violations = append(rep.Violations, Violation{
						Kind: ViolationDanglingRef, Cell: r.Cell,
						Detail: fmt.Sprintf("parent %s referenced but absent from store", r.Parent),
					})
				} else if want, err := r.Cell.Parent(); err == nil && want != r.Parent {
					rep.Violations = append(rep.Violations, Violation{
						Kind: ViolationInvariant, Cell: r.Cell,
						Detail: fmt.Sprintf("declared parent %s != hierarchical parent %s", r.Parent, want),
					})
				}
			}
		}
		for _, nb := range r.Neighbors {
			if !nb.IsValid() {
				rep.Violations = append(rep.Violations, Violation{
					Kind: ViolationInvariant, Cell: r.Cell,
					Detail: fmt.Sprintf("neighbor %016x is not a valid cell", uint64(nb)),
				})
				continue
			}
			if _, ok := present[nb]; !ok {
				rep.Violations = append(rep.Violations, Violation{
					Kind: ViolationDanglingRef, Cell: r.Cell,
					Detail: fmt.Sprintf("neighbor %s referenced but absent from store", nb),
				})
			}
		}
	}

	// --- connectivity: isolated loops / fragments --------------------
	parent := make(map[Cell]Cell, len(snap))
	var find func(Cell) Cell
	find = func(c Cell) Cell {
		p, ok := parent[c]
		if !ok {
			parent[c] = c
			return c
		}
		if p != c {
			parent[c] = find(p)
		}
		return parent[c]
	}
	union := func(a, b Cell) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for _, r := range snap {
		for _, nb := range r.Neighbors {
			if _, ok := present[nb]; ok {
				union(r.Cell, nb)
			}
		}
	}
	comps := make(map[Cell][]Cell)
	for c := range present {
		root := find(c)
		comps[root] = append(comps[root], c)
	}
	rep.Components = len(comps)

	// Identify the single main component: largest size, ties broken by
	// smallest member cell for determinism.
	mainRoot := Cell(0)
	mainSize := 0
	roots := make([]Cell, 0, len(comps))
	for root := range comps {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	for _, root := range roots {
		if len(comps[root]) > mainSize {
			mainSize, mainRoot = len(comps[root]), root
		}
	}
	rep.MainComponent = mainSize

	for root, members := range comps {
		if root == mainRoot || len(members) < 2 {
			continue // main component and isolated singles are not loops
		}
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		inComp := make(map[Cell]bool, len(members))
		for _, c := range members {
			inComp[c] = true
		}
		loop := len(members) >= 3
		for _, c := range members {
			deg := 0
			for _, nb := range present[c].Neighbors {
				if inComp[nb] {
					deg++
				}
			}
			if deg != 2 {
				loop = false
				break
			}
		}
		shape := "closed loop"
		if !loop {
			shape = "fragment"
		}
		rep.Violations = append(rep.Violations, Violation{
			Kind: ViolationIsolatedLoop, Cell: members[0],
			Detail: fmt.Sprintf("component of %d cells (%s) detached from main component of %d",
				len(members), shape, rep.MainComponent),
		})
	}

	// --- data skew across base cells ---------------------------------
	for _, r := range snap {
		if r.Cell.IsValid() {
			rep.BucketCounts[r.Cell.BaseCell()]++
		}
	}
	if rep.Records > 0 {
		mean := float64(rep.Records) / NumBaseCells
		maxCount, maxBucket := 0, 0
		for b := 0; b < NumBaseCells; b++ {
			if rep.BucketCounts[b] > maxCount {
				maxCount, maxBucket = rep.BucketCounts[b], b
			}
		}
		rep.SkewRatio = float64(maxCount) / mean
		minRec := in.MinRecordsForSkew
		if minRec < 1 {
			minRec = 1
		}
		if in.SkewThreshold > 0 && rep.Records >= minRec && rep.SkewRatio > in.SkewThreshold {
			var witness Cell
			for _, r := range snap {
				if r.Cell.IsValid() && r.Cell.BaseCell() == maxBucket {
					witness = r.Cell
					break
				}
			}
			rep.Violations = append(rep.Violations, Violation{
				Kind: ViolationDataSkew, Cell: witness,
				Detail: fmt.Sprintf("base cell %d holds %d/%d records (%.1fx mean %.1f)",
					maxBucket, maxCount, rep.Records, rep.SkewRatio, mean),
			})
		}
	}

	sort.SliceStable(rep.Violations, func(i, j int) bool {
		if rep.Violations[i].Kind != rep.Violations[j].Kind {
			return rep.Violations[i].Kind < rep.Violations[j].Kind
		}
		return rep.Violations[i].Cell < rep.Violations[j].Cell
	})
	return rep
}
