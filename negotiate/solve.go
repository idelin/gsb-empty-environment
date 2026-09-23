package negotiate

import (
	"fmt"
	"sort"
)

// Status is the outcome class of a negotiation.
type Status int

const (
	// StatusOK means a compatible selection was found.
	StatusOK Status = iota
	// StatusInfeasible means the declared conditions conflict; see
	// Result.Reasons and Result.Core.
	StatusInfeasible
	// StatusBudgetExceeded means the search budget ran out before the
	// question could be settled. It is reported separately from
	// infeasibility: the problem is undecided, not rejected.
	StatusBudgetExceeded
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusInfeasible:
		return "infeasible"
	case StatusBudgetExceeded:
		return "budget-exceeded"
	}
	return "unknown"
}

// Score is the optimization objective, compared lexicographically.
type Score struct {
	Weight   int // total preference weight of selected optional features
	Versions int // total of selected versions
}

// Result is the outcome of Negotiate.
type Result struct {
	Status    Status
	Selection Selection // set only when Status == StatusOK
	Score     Score
	Reasons   []string // human-readable rejection reasons
	// Core is a 1-minimal subset of the declared conditions that already
	// conflict: removing any single remaining condition makes the problem
	// feasible. CorePending reports that the budget was insufficient to
	// confirm minimality, so Core may be larger than necessary.
	Core        []string
	CorePending bool
	Nodes       int // search nodes explored by the main solve
}

// feature is the merged, both-sides view of one negotiable feature.
type feature struct {
	name      string
	required  bool
	priority  int
	lo, hi    int // merged supported range (intersection)
	deps      []Requirement
	conflicts map[string]bool
}

type depEdge struct {
	src string
	req Requirement
}

type problem struct {
	feats   []*feature // sorted by name
	index   map[string]int
	symconf map[string]map[string]bool
	income  map[string][]depEdge // target -> incoming dependencies
}

// buildProblem merges both profiles. Features supported by only one side
// are not selectable; if a side requires such a feature, or a required
// feature's ranges do not intersect, a hard reason is returned.
func buildProblem(a, b *Profile) (*problem, []string) {
	var reasons []string
	names := map[string]bool{}
	for _, f := range a.Features {
		names[f.Name] = true
	}
	for _, f := range b.Features {
		names[f.Name] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	p := &problem{
		index:   map[string]int{},
		symconf: map[string]map[string]bool{},
		income:  map[string][]depEdge{},
	}
	for _, n := range sorted {
		da, db := a.feature(n), b.feature(n)
		switch {
		case da != nil && db != nil:
			inter, ok := da.Versions.Intersect(db.Versions)
			if !ok {
				if da.Required || db.Required {
					reasons = append(reasons, fmt.Sprintf(
						"feature %q is required but version ranges %s (%s) and %s (%s) do not intersect",
						n, da.Versions, a.Name, db.Versions, b.Name))
				}
				continue
			}
			f := &feature{
				name:      n,
				required:  da.Required || db.Required,
				priority:  da.Priority + db.Priority,
				lo:        inter.Min,
				hi:        inter.Max,
				deps:      append(append([]Requirement(nil), da.Depends...), db.Depends...),
				conflicts: map[string]bool{},
			}
			for _, c := range da.Conflicts {
				f.conflicts[c] = true
			}
			for _, c := range db.Conflicts {
				f.conflicts[c] = true
			}
			p.index[n] = len(p.feats)
			p.feats = append(p.feats, f)
		case da != nil:
			if da.Required {
				reasons = append(reasons, fmt.Sprintf(
					"feature %q is required by %s but not supported by %s", n, a.Name, b.Name))
			}
		default:
			if db.Required {
				reasons = append(reasons, fmt.Sprintf(
					"feature %q is required by %s but not supported by %s", n, b.Name, a.Name))
			}
		}
	}
	for _, f := range p.feats {
		for c := range f.conflicts {
			if p.symconf[c] == nil {
				p.symconf[c] = map[string]bool{}
			}
			p.symconf[c][f.name] = true
			if p.symconf[f.name] == nil {
				p.symconf[f.name] = map[string]bool{}
			}
			p.symconf[f.name][c] = true
		}
		for _, d := range f.deps {
			p.income[d.Feature] = append(p.income[d.Feature], depEdge{src: f.name, req: d})
		}
	}
	return p, reasons
}

const (
	unassigned = -2
	absent     = -1
)

// solve searches for the best selection. budget < 0 means unlimited;
// otherwise it bounds the number of explored search nodes.
func (p *problem) solve(budget int) (Selection, Score, Status, int) {
	n := len(p.feats)
	assign := make([]int, n)
	for i := range assign {
		assign[i] = unassigned
	}
	remW := make([]int, n+1) // max additional weight from features i..n-1
	remV := make([]int, n+1) // max additional version sum from features i..n-1
	for i := n - 1; i >= 0; i-- {
		remW[i] = remW[i+1] + max(0, p.feats[i].priority)
		remV[i] = remV[i+1] + p.feats[i].hi
	}

	var bestSel Selection
	var bestScore Score
	haveBest := false
	nodes := 0
	blown := false

	var dfs func(i, w, v int)
	dfs = func(i, w, v int) {
		if blown {
			return
		}
		nodes++
		if budget >= 0 && nodes > budget {
			blown = true
			return
		}
		if haveBest {
			// Prune only branches that cannot strictly improve the
			// lexicographic objective; ties are explored so the
			// deterministic tie-break stays exact.
			if w+remW[i] < bestScore.Weight ||
				(w+remW[i] == bestScore.Weight && v+remV[i] < bestScore.Versions) {
				return
			}
		}
		if i == n {
			cand := make(Selection, n)
			for j, f := range p.feats {
				if assign[j] >= 0 {
					cand[f.name] = assign[j]
				}
			}
			sc := Score{Weight: w, Versions: v}
			if !haveBest || p.better(sc, cand, bestScore, bestSel) {
				haveBest, bestScore, bestSel = true, sc, cand
			}
			return
		}
		f := p.feats[i]
		for ver := f.hi; ver >= f.lo; ver-- {
			assign[i] = ver
			if p.consistent(assign, i) {
				dfs(i+1, w+f.priority, v+ver)
			}
		}
		if !f.required {
			assign[i] = absent
			if p.consistent(assign, i) {
				dfs(i+1, w, v)
			}
		}
		assign[i] = unassigned
	}
	dfs(0, 0, 0)

	switch {
	case blown:
		return nil, Score{}, StatusBudgetExceeded, nodes
	case haveBest:
		return bestSel, bestScore, StatusOK, nodes
	default:
		return nil, Score{}, StatusInfeasible, nodes
	}
}

// consistent checks every constraint that involves feature i and is
// decidable under the current partial assignment, including forward checks
// on unassigned dependency targets.
func (p *problem) consistent(assign []int, i int) bool {
	f := p.feats[i]
	val := assign[i]
	if val >= 0 {
		// Mutual exclusions, symmetric across both parties' declarations.
		for c := range p.symconf[f.name] {
			if j, ok := p.index[c]; ok && assign[j] >= 0 {
				return false
			}
		}
		// Own dependencies.
		for _, d := range f.deps {
			if !d.active(val) {
				continue
			}
			j, ok := p.index[d.Feature]
			if !ok {
				return false // target not selectable by both parties
			}
			if assign[j] == unassigned {
				// Forward check: some target version must remain usable.
				if _, ok := (VersionRange{j2lo(p, j), j2hi(p, j)}).Intersect(d.Range); !ok {
					return false
				}
				continue
			}
			if assign[j] < 0 || !d.Range.Contains(assign[j]) {
				return false
			}
		}
	}
	// Dependencies declared by already-assigned features on feature i.
	for _, e := range p.income[f.name] {
		j, ok := p.index[e.src]
		if !ok {
			continue
		}
		sv := assign[j]
		if sv < 0 || !e.req.active(sv) {
			continue
		}
		if val < 0 || !e.req.Range.Contains(val) {
			return false
		}
	}
	return true
}

func j2lo(p *problem, j int) int { return p.feats[j].lo }
func j2hi(p *problem, j int) int { return p.feats[j].hi }

// better reports whether candidate (sc, cand) strictly improves on
// (bestSc, best) under the lexicographic objective and deterministic
// tie-break.
func (p *problem) better(sc Score, cand Selection, bestSc Score, best Selection) bool {
	if sc.Weight != bestSc.Weight {
		return sc.Weight > bestSc.Weight
	}
	if sc.Versions != bestSc.Versions {
		return sc.Versions > bestSc.Versions
	}
	for _, f := range p.feats {
		cv, cok := cand[f.name]
		bv, bok := best[f.name]
		cw, bw := -1, -1
		if cok {
			cw = cv
		}
		if bok {
			bw = bv
		}
		if cw != bw {
			return cw > bw
		}
	}
	return false
}

// validate fully checks a selection against the merged problem. It is used
// for certificate verification and by the exhaustive reference checker.
func (p *problem) validate(sel Selection) error {
	for name := range sel {
		if _, ok := p.index[name]; !ok {
			return fmt.Errorf("negotiate: selection contains unsupported feature %q", name)
		}
	}
	for _, f := range p.feats {
		v, ok := sel[f.name]
		if !ok {
			if f.required {
				return fmt.Errorf("negotiate: required feature %q not selected", f.name)
			}
			continue
		}
		if v < f.lo || v > f.hi {
			return fmt.Errorf("negotiate: feature %q version %d outside negotiated range [%d..%d]", f.name, v, f.lo, f.hi)
		}
		for c := range p.symconf[f.name] {
			if _, clash := sel[c]; clash {
				return fmt.Errorf("negotiate: feature %q conflicts with selected feature %q", f.name, c)
			}
		}
		for _, d := range f.deps {
			if !d.active(v) {
				continue
			}
			tv, ok := sel[d.Feature]
			if !ok {
				return fmt.Errorf("negotiate: feature %q@%d requires %q, which is not selected", f.name, v, d.Feature)
			}
			if !d.Range.Contains(tv) {
				return fmt.Errorf("negotiate: feature %q@%d requires %s, but %q is at version %d", f.name, v, d, d.Feature, tv)
			}
		}
	}
	return nil
}
