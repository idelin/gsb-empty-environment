package psdcomplete

import (
	"fmt"
	"math/big"
	"sort"
)

// PruneKind identifies why a search branch was cut off.
type PruneKind int

const (
	// PruneMinorNegative: a fully assigned principal submatrix has a
	// negative determinant, so no completion of this branch can be PSD.
	PruneMinorNegative PruneKind = iota
	// PruneTraceBound: the smallest trace still reachable in this branch
	// already exceeds the best trace found so far.
	PruneTraceBound
	// PruneLexBound: the branch can only tie the best trace and every
	// completion is already lexicographically larger than the incumbent.
	PruneLexBound
)

func (k PruneKind) String() string {
	switch k {
	case PruneMinorNegative:
		return "negative principal minor"
	case PruneTraceBound:
		return "trace lower bound"
	case PruneLexBound:
		return "lexicographic bound"
	}
	return "unknown"
}

// NamedValue is one assigned matrix entry (0-based position, i <= j).
type NamedValue struct {
	Pos   [2]int
	Value *big.Rat
}

// PruneEvent is the evidence recorded when a branch is pruned.
type PruneEvent struct {
	Kind   PruneKind
	Depth  int          // number of unknown positions assigned at the cut
	Prefix []NamedValue // the assigned unknown entries, in assignment order

	// Subset and Minor are set for PruneMinorNegative.
	Subset []int
	Minor  *big.Rat

	// Bound and Best are set for PruneTraceBound / PruneLexBound.
	Bound *big.Rat
	Best  *big.Rat
}

func (e PruneEvent) String() string {
	prefix := ""
	for i, nv := range e.Prefix {
		if i > 0 {
			prefix += " "
		}
		prefix += fmt.Sprintf("a%d%d=%s", nv.Pos[0]+1, nv.Pos[1]+1, ratString(nv.Value))
	}
	switch e.Kind {
	case PruneMinorNegative:
		return fmt.Sprintf("depth %d [%s]: principal minor on %s = %s < 0",
			e.Depth, prefix, formatSubset(e.Subset), ratString(e.Minor))
	case PruneTraceBound:
		return fmt.Sprintf("depth %d [%s]: reachable trace >= %s > best %s",
			e.Depth, prefix, ratString(e.Bound), ratString(e.Best))
	case PruneLexBound:
		return fmt.Sprintf("depth %d [%s]: trace can only tie best %s and prefix is lexicographically larger",
			e.Depth, prefix, ratString(e.Best))
	}
	return "unknown prune event"
}

// Options tunes the solver.
type Options struct {
	// MaxPruneLog caps how many prune events are retained in
	// Result.PruneLog. <= 0 means unlimited. Default (nil Options) is 10000.
	MaxPruneLog int
}

// Result is the outcome of Solve or SolveBrute.
type Result struct {
	// OK is true iff a feasible (PSD) assignment exists.
	OK bool
	N  int
	// Matrix is the optimal assignment when OK is true.
	Matrix Matrix
	// Trace is the minimal trace when OK is true.
	Trace *big.Rat
	// Certificate proves PSD of Matrix (all principal minors plus an
	// independent exact-elimination check). Nil when OK is false.
	Certificate *Certificate

	// PruneLog holds the recorded per-branch pruning evidence (possibly
	// truncated; PruneTotal is the full count).
	PruneLog   []PruneEvent
	PruneTotal int

	NodesExpanded   int
	LeavesEvaluated int
}

type variable struct {
	pos    [2]int
	cands  []*big.Rat
	isDiag bool
}

type subsetInfo struct {
	indices []int
	// depth is the index of the last unknown position (in assignment
	// order) inside this subset, or -1 if the submatrix is fully fixed.
	depth int
}

type solver struct {
	spec    *Spec
	vars    []variable
	subsets []subsetInfo
	byDepth [][]int // subset indices keyed by completion depth

	suffixMinTrace []*big.Rat // suffixMinTrace[d]: min trace from diag vars >= d

	cur           Matrix
	assignedTrace *big.Rat

	bestTrace *big.Rat
	bestTuple []*big.Rat // unknown-position values in assignment order
	bestMat   Matrix

	pruneLog   []PruneEvent
	pruneTotal int
	maxLog     int
	nodes      int
	leaves     int
}

func newSolver(spec *Spec, maxLog int) *solver {
	s := &solver{spec: spec, maxLog: maxLog, cur: NewMatrix(spec.N), assignedTrace: new(big.Rat)}
	for p, v := range spec.fixed {
		s.cur[p[0]][p[1]] = cloneRat(v)
		s.cur[p[1]][p[0]] = cloneRat(v)
		if p[0] == p[1] {
			s.assignedTrace.Add(s.assignedTrace, v)
		}
	}
	for p, c := range spec.cands {
		s.vars = append(s.vars, variable{pos: p, cands: c, isDiag: p[0] == p[1]})
	}
	sort.Slice(s.vars, func(a, b int) bool {
		if s.vars[a].pos[0] != s.vars[b].pos[0] {
			return s.vars[a].pos[0] < s.vars[b].pos[0]
		}
		return s.vars[a].pos[1] < s.vars[b].pos[1]
	})

	// Map each unknown upper-triangular position to its variable index.
	varIndex := make(map[[2]int]int, len(s.vars))
	for i, v := range s.vars {
		varIndex[v.pos] = i
	}

	// Precompute every non-empty principal subset and the depth at which
	// its submatrix becomes fully assigned.
	n := spec.N
	s.byDepth = make([][]int, len(s.vars))
	for mask := 1; mask < (1 << uint(n)); mask++ {
		var idx []int
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				idx = append(idx, i)
			}
		}
		depth := -1
		for a := 0; a < len(idx); a++ {
			for b := a; b < len(idx); b++ {
				p := [2]int{idx[a], idx[b]}
				if vi, ok := varIndex[p]; ok && vi > depth {
					depth = vi
				}
			}
		}
		si := len(s.subsets)
		s.subsets = append(s.subsets, subsetInfo{indices: idx, depth: depth})
		if depth >= 0 {
			s.byDepth[depth] = append(s.byDepth[depth], si)
		}
	}

	// Suffix sums of minimum diagonal contributions for the trace bound.
	s.suffixMinTrace = make([]*big.Rat, len(s.vars)+1)
	s.suffixMinTrace[len(s.vars)] = new(big.Rat)
	for d := len(s.vars) - 1; d >= 0; d-- {
		s.suffixMinTrace[d] = cloneRat(s.suffixMinTrace[d+1])
		if s.vars[d].isDiag {
			s.suffixMinTrace[d].Add(s.suffixMinTrace[d], s.vars[d].cands[0])
		}
	}
	return s
}

func (s *solver) logPrune(ev PruneEvent) {
	s.pruneTotal++
	if s.maxLog < 0 || len(s.pruneLog) < s.maxLog {
		s.pruneLog = append(s.pruneLog, ev)
	}
}

func (s *solver) prefix(depth int) []NamedValue {
	out := make([]NamedValue, depth)
	for i := 0; i < depth; i++ {
		p := s.vars[i].pos
		out[i] = NamedValue{Pos: p, Value: cloneRat(s.cur[p[0]][p[1]])}
	}
	return out
}

// minorDet computes the determinant of the principal submatrix on the given
// subset; all its entries must already be assigned.
func (s *solver) minorDet(sub subsetInfo) *big.Rat {
	return Det(s.cur.Submatrix(sub.indices))
}

// prefixLexGreater reports whether the values assigned so far are already
// lexicographically greater than the incumbent's (in upper-triangle order,
// which coincides with the assignment order for the unknown positions).
func (s *solver) prefixLexGreater(depth int) bool {
	for i := 0; i < depth; i++ {
		p := s.vars[i].pos
		c := s.cur[p[0]][p[1]].Cmp(s.bestTuple[i])
		if c > 0 {
			return true
		}
		if c < 0 {
			return false
		}
	}
	return false
}

func (s *solver) dfs(depth int) {
	// Check principal minors that became fully assigned by the last move.
	if depth > 0 {
		for _, si := range s.byDepth[depth-1] {
			sub := s.subsets[si]
			if d := s.minorDet(sub); d.Sign() < 0 {
				s.logPrune(PruneEvent{
					Kind:   PruneMinorNegative,
					Depth:  depth,
					Prefix: s.prefix(depth),
					Subset: append([]int(nil), sub.indices...),
					Minor:  d,
				})
				return
			}
		}
	}
	// Trace / lexicographic bounds against the incumbent.
	if s.bestTrace != nil {
		bound := new(big.Rat).Add(s.assignedTrace, s.suffixMinTrace[depth])
		switch bound.Cmp(s.bestTrace) {
		case 1:
			s.logPrune(PruneEvent{
				Kind:   PruneTraceBound,
				Depth:  depth,
				Prefix: s.prefix(depth),
				Bound:  bound,
				Best:   cloneRat(s.bestTrace),
			})
			return
		case 0:
			if s.prefixLexGreater(depth) {
				s.logPrune(PruneEvent{
					Kind:   PruneLexBound,
					Depth:  depth,
					Prefix: s.prefix(depth),
					Bound:  bound,
					Best:   cloneRat(s.bestTrace),
				})
				return
			}
		}
	}
	if depth == len(s.vars) {
		s.leaves++
		s.consider()
		return
	}
	s.nodes++
	v := s.vars[depth]
	p := v.pos
	for _, cand := range v.cands {
		s.cur[p[0]][p[1]] = cand
		s.cur[p[1]][p[0]] = cand
		if v.isDiag {
			s.assignedTrace.Add(s.assignedTrace, cand)
		}
		s.dfs(depth + 1)
		if v.isDiag {
			s.assignedTrace.Sub(s.assignedTrace, cand)
		}
	}
	s.cur[p[0]][p[1]] = nil
	s.cur[p[1]][p[0]] = nil
}

// consider is called at each feasible leaf (all principal minors along the
// path were non-negative, so the full matrix is PSD).
func (s *solver) consider() {
	tr := cloneRat(s.assignedTrace)
	if s.bestTrace != nil {
		switch tr.Cmp(s.bestTrace) {
		case 1:
			return
		case 0:
			if !s.tupleLexLess() {
				return
			}
		}
	}
	s.bestTrace = tr
	s.bestTuple = make([]*big.Rat, len(s.vars))
	for i, v := range s.vars {
		s.bestTuple[i] = cloneRat(s.cur[v.pos[0]][v.pos[1]])
	}
	s.bestMat = s.cur.Clone()
}

func (s *solver) tupleLexLess() bool {
	for i, v := range s.vars {
		c := s.cur[v.pos[0]][v.pos[1]].Cmp(s.bestTuple[i])
		if c < 0 {
			return true
		}
		if c > 0 {
			return false
		}
	}
	return false
}

func (s *solver) result() *Result {
	res := &Result{
		N:               s.spec.N,
		PruneLog:        s.pruneLog,
		PruneTotal:      s.pruneTotal,
		NodesExpanded:   s.nodes,
		LeavesEvaluated: s.leaves,
	}
	if s.bestTrace == nil {
		return res
	}
	res.OK = true
	res.Matrix = s.bestMat
	res.Trace = cloneRat(s.bestTrace)
	res.Certificate = buildCertificate(s.bestMat)
	return res
}

// Solve finds the assignment that makes the matrix positive semidefinite
// with minimal trace, breaking ties by lexicographic order of the upper
// triangle (row-major). It uses depth-first branch and bound with exact
// rational arithmetic and records pruning evidence for every cut branch.
//
// PSD is decided by the all-principal-minors criterion (not merely the
// diagonal and the full determinant), and the returned certificate is
// independently cross-checked by exact LDL^T elimination.
func Solve(spec *Spec, opts *Options) (*Result, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	maxLog := 10000
	if opts != nil {
		maxLog = opts.MaxPruneLog
	}
	s := newSolver(spec, maxLog)

	// Fully fixed principal minors are checked once, up front.
	for _, sub := range s.subsets {
		if sub.depth != -1 {
			continue
		}
		if d := s.minorDet(sub); d.Sign() < 0 {
			s.logPrune(PruneEvent{
				Kind:   PruneMinorNegative,
				Depth:  0,
				Subset: append([]int(nil), sub.indices...),
				Minor:  d,
			})
			return s.result(), nil
		}
	}

	s.dfs(0)
	res := s.result()
	if res.OK && !res.Certificate.Elimination.PSD {
		return nil, fmt.Errorf("psdcomplete: internal error: minor criterion and LDL elimination disagree: %s",
			res.Certificate.Elimination.Detail)
	}
	return res, nil
}

// SolveBrute is the independent exhaustive verifier: it enumerates every
// candidate combination without any pruning, tests each completed matrix
// with the all-principal-minors criterion, and keeps the best one. It is
// intended for cross-checking Solve on small candidate sets.
func SolveBrute(spec *Spec) (*Result, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	s := newSolver(spec, -1)
	var rec func(depth int)
	rec = func(depth int) {
		if depth == len(s.vars) {
			s.leaves++
			if ok, _ := IsPSDMinors(s.cur); ok {
				s.consider()
			}
			return
		}
		v := s.vars[depth]
		p := v.pos
		for _, cand := range v.cands {
			s.cur[p[0]][p[1]] = cand
			s.cur[p[1]][p[0]] = cand
			if v.isDiag {
				s.assignedTrace.Add(s.assignedTrace, cand)
			}
			rec(depth + 1)
			if v.isDiag {
				s.assignedTrace.Sub(s.assignedTrace, cand)
			}
		}
		s.cur[p[0]][p[1]] = nil
		s.cur[p[1]][p[0]] = nil
	}
	rec(0)
	res := s.result()
	if res.OK && !res.Certificate.Elimination.PSD {
		return nil, fmt.Errorf("psdcomplete: internal error: minor criterion and LDL elimination disagree: %s",
			res.Certificate.Elimination.Detail)
	}
	return res, nil
}
