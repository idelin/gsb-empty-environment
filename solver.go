package main

import "strings"

// compiledEq is an Equality with variable names resolved to indices.
type compiledEq struct {
	left  []int
	right []int
}

// abortSearch is panicked when the state budget is exhausted.
type abortSearch struct{}

type solver struct {
	alphabet     []byte
	rank         [256]int
	n            int
	lo           []int // per-variable lower bounds, raised to fit prefix+suffix
	hi           []int
	prefixes     []string
	suffixes     []string
	names        []string
	eqs          []compiledEq
	forbidden    []string
	maxForbidden int
	sumLo        []int // suffix sums of lo
	sumHi        []int // suffix sums of hi
	budget       int64
	states       int64
	best         string // best concatenation found for the current total length
	bestVals     []string
}

func newSolver(p *Problem) *solver {
	s := &solver{budget: p.Budget}
	s.alphabet = []byte(p.Alphabet)
	for i := range s.rank {
		s.rank[i] = 255
	}
	for i := 0; i < len(p.Alphabet); i++ {
		s.rank[p.Alphabet[i]] = i
	}
	s.n = len(p.Variables)
	s.lo = make([]int, s.n)
	s.hi = make([]int, s.n)
	s.prefixes = make([]string, s.n)
	s.suffixes = make([]string, s.n)
	s.names = make([]string, s.n)
	index := make(map[string]int)
	for i, v := range p.Variables {
		s.names[i] = v.Name
		index[v.Name] = i
		s.prefixes[i] = v.Prefix
		s.suffixes[i] = v.Suffix
		lo := v.MinLen
		if m := minFeasibleLen(v.Prefix, v.Suffix); m > lo {
			lo = m
		}
		s.lo[i] = lo
		s.hi[i] = v.MaxLen
	}
	for _, eq := range p.Equalities {
		ce := compiledEq{}
		for _, n := range eq.Left {
			ce.left = append(ce.left, index[n])
		}
		for _, n := range eq.Right {
			ce.right = append(ce.right, index[n])
		}
		s.eqs = append(s.eqs, ce)
	}
	s.forbidden = p.Forbidden
	for _, f := range p.Forbidden {
		if len(f) > s.maxForbidden {
			s.maxForbidden = len(f)
		}
	}
	s.sumLo = make([]int, s.n+1)
	s.sumHi = make([]int, s.n+1)
	for i := s.n - 1; i >= 0; i-- {
		s.sumLo[i] = s.sumLo[i+1] + s.lo[i]
		s.sumHi[i] = s.sumHi[i+1] + s.hi[i]
	}
	return s
}

// minFeasibleLen returns the minimal length of a string having prefix p and
// suffix s (the two may overlap).
func minFeasibleLen(p, s string) int {
	maxK := len(p)
	if len(s) < maxK {
		maxK = len(s)
	}
	overlap := 0
	for k := 1; k <= maxK; k++ {
		if p[len(p)-k:] == s[:k] {
			overlap = k
		}
	}
	return len(p) + len(s) - overlap
}

// Solve searches for the optimal assignment: minimal total length first,
// then lexicographically smallest concatenation X1..Xn under the alphabet
// order. It returns UNKNOWN (never UNSAT) when the state budget runs out
// before the search space is fully explored.
func Solve(p *Problem) (sol *Solution) {
	s := newSolver(p)
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(abortSearch); ok {
				sol = &Solution{Status: StatusUNKNOWN, States: s.states}
				return
			}
			panic(r)
		}
	}()
	for i := 0; i < s.n; i++ {
		if s.lo[i] > s.hi[i] {
			return &Solution{Status: StatusUNSAT, States: s.states}
		}
	}
	for total := s.sumLo[0]; total <= s.sumHi[0]; total++ {
		s.best = ""
		s.bestVals = nil
		s.enumTuples(0, total, make([]int, s.n))
		if s.bestVals != nil {
			assignment := make(map[string]string, s.n)
			for i, v := range s.bestVals {
				assignment[s.names[i]] = v
			}
			return &Solution{Status: StatusSAT, Assignment: assignment, TotalLength: total, States: s.states}
		}
	}
	return &Solution{Status: StatusUNSAT, States: s.states}
}

func (s *solver) count() {
	s.states++
	if s.budget > 0 && s.states > s.budget {
		panic(abortSearch{})
	}
}

// enumTuples enumerates length tuples for variables i..n-1 summing to rem.
func (s *solver) enumTuples(i, rem int, lens []int) {
	if i == s.n {
		if rem == 0 {
			s.assignVars(lens, 0, nil, make([]string, s.n))
		}
		return
	}
	for l := s.lo[i]; l <= s.hi[i] && l <= rem; l++ {
		rest := rem - l
		if rest < s.sumLo[i+1] || rest > s.sumHi[i+1] {
			continue
		}
		lens[i] = l
		s.enumTuples(i+1, rest, lens)
	}
}

// assignVars assigns variables i..n-1 for a fixed length tuple; built holds
// the concatenation of variables before i.
func (s *solver) assignVars(lens []int, i int, built []byte, vals []string) {
	s.count()
	if i == s.n {
		concat := string(built)
		if s.bestVals == nil || cmpRank(concat, s.best, &s.rank) < 0 {
			s.best = concat
			s.bestVals = append([]string(nil), vals...)
		}
		return
	}
	s.extendVar(lens, i, nil, built, vals)
}

// extendVar chooses the next character of variable i; cur holds the
// characters chosen so far.
func (s *solver) extendVar(lens []int, i int, cur, built []byte, vals []string) {
	s.count()
	j := len(cur)
	if j == lens[i] {
		word := string(cur)
		if !strings.HasPrefix(word, s.prefixes[i]) || !strings.HasSuffix(word, s.suffixes[i]) {
			return
		}
		vals[i] = word
		if !s.equalitiesConsistent(i, vals) {
			return
		}
		nb := make([]byte, 0, len(built)+len(cur))
		nb = append(nb, built...)
		nb = append(nb, cur...)
		s.assignVars(lens, i+1, nb, vals)
		return
	}
	if s.bestVals != nil {
		// Prune against the incumbent: built+cur must not exceed the
		// corresponding prefix of best.
		bl := len(built) + len(cur)
		if cmpRankBytes(built, cur, s.best[:bl], &s.rank) > 0 {
			return
		}
	}
	if j < len(s.prefixes[i]) {
		s.tryChar(lens, i, cur, built, vals, s.prefixes[i][j])
		return
	}
	for _, c := range s.alphabet {
		s.tryChar(lens, i, cur, built, vals, c)
	}
}

func (s *solver) tryChar(lens []int, i int, cur, built []byte, vals []string, c byte) {
	ncur := make([]byte, len(cur)+1)
	copy(ncur, cur)
	ncur[len(cur)] = c
	if s.endsWithForbidden(built, ncur) {
		return
	}
	s.extendVar(lens, i, ncur, built, vals)
}

// endsWithForbidden reports whether built+cur ends with a forbidden
// substring. Occurrences entirely inside built were rejected when built was
// constructed, so only suffixes crossing into cur need checking.
func (s *solver) endsWithForbidden(built, cur []byte) bool {
	if len(s.forbidden) == 0 {
		return false
	}
	start := 0
	if len(built) > s.maxForbidden {
		start = len(built) - s.maxForbidden
	}
	tail := string(built[start:]) + string(cur)
	for _, f := range s.forbidden {
		if len(f) <= len(tail) && tail[len(tail)-len(f):] == f {
			return true
		}
	}
	return false
}

// equalitiesConsistent checks every equality against variables 0..upTo: the
// known prefix of each side must be compatible with the other. A fully
// assigned side is final, so the other side's known part must be a prefix
// of it (or equal it when both are final).
func (s *solver) equalitiesConsistent(upTo int, vals []string) bool {
	for _, eq := range s.eqs {
		lk, lDone := knownPrefix(eq.left, upTo, vals)
		rk, rDone := knownPrefix(eq.right, upTo, vals)
		var ok bool
		switch {
		case lDone && rDone:
			ok = lk == rk
		case lDone:
			ok = strings.HasPrefix(lk, rk)
		case rDone:
			ok = strings.HasPrefix(rk, lk)
		default:
			ok = strings.HasPrefix(lk, rk) || strings.HasPrefix(rk, lk)
		}
		if !ok {
			return false
		}
	}
	return true
}

// knownPrefix returns the known prefix of a concatenation side: the values
// of the assigned variables up to the first unassigned one, and whether
// every variable of the side is assigned.
func knownPrefix(side []int, upTo int, vals []string) (string, bool) {
	known := ""
	done := true
	for _, v := range side {
		if v > upTo {
			done = false
			break
		}
		known += vals[v]
	}
	return known, done
}

// cmpRank compares two strings by alphabet rank, shorter-is-smaller on
// shared prefixes.
func cmpRank(a, b string, rank *[256]int) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for k := 0; k < n; k++ {
		ra, rb := rank[a[k]], rank[b[k]]
		if ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// cmpRankBytes compares built+cur against b, where len(b) == len(built)+len(cur).
func cmpRankBytes(built, cur []byte, b string, rank *[256]int) int {
	for k := 0; k < len(b); k++ {
		var ca byte
		if k < len(built) {
			ca = built[k]
		} else {
			ca = cur[k-len(built)]
		}
		cb := b[k]
		if rank[ca] != rank[cb] {
			if rank[ca] < rank[cb] {
				return -1
			}
			return 1
		}
	}
	return 0
}
