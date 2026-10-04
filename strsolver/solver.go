// Package strsolver solves finite-domain string constraint problems.
//
// A problem fixes a finite alphabet and a set of string variables. Each
// variable has length bounds and optional prefix/suffix constraints. Global
// constraints are a set of forbidden substrings (no variable's value may
// contain one, including substrings that span the boundary of a
// concatenation mandated by a ConcatEq constraint) and concatenation
// equalities of the form x[Result] = x[Left] + x[Right].
//
// Solution ordering: solutions are ordered first by total length (the sum
// of the lengths of all variables), then lexicographically by the tuple
// (x[0], x[1], ..., x[n-1]) using byte-wise string comparison. Solve
// returns the smallest solution under this order.
//
// Budget: the search budget is counted in explored search states (nodes of
// the length-enumeration tree plus nodes of the character-assignment DFS).
// If the budget is exhausted before the search space is fully explored,
// Solve reports Unknown -- it never reports Unsat for an incomplete search.
package strsolver

import "fmt"

// ConcatEq constrains x[Result] to equal x[Left] + x[Right].
type ConcatEq struct {
	Left   int `json:"left"`
	Right  int `json:"right"`
	Result int `json:"result"`
}

// VarBounds gives the length bounds and affix constraints of one variable.
type VarBounds struct {
	Min    int    `json:"min"`
	Max    int    `json:"max"`
	Prefix string `json:"prefix,omitempty"`
	Suffix string `json:"suffix,omitempty"`
}

// Problem is a full string constraint problem instance.
type Problem struct {
	Alphabet  string      `json:"alphabet"`
	Vars      []VarBounds `json:"vars"`
	Forbidden []string    `json:"forbidden,omitempty"`
	Concat    []ConcatEq  `json:"concat,omitempty"`
}

// Status is the outcome of a solve run.
type Status int

const (
	Sat     Status = iota // a smallest satisfying assignment was found
	Unsat                 // the search space was fully explored, no solution
	Unknown               // the budget was exhausted before the search finished
)

func (s Status) String() string {
	switch s {
	case Sat:
		return "sat"
	case Unsat:
		return "unsat"
	case Unknown:
		return "unknown"
	}
	return "?"
}

// Result is the outcome of Solve.
type Result struct {
	Status     Status
	Assignment []string // variable values when Status == Sat
	States     int64    // search states explored
}

// Validate checks structural well-formedness of p.
func (p *Problem) Validate() error {
	seen := [256]bool{}
	for i := 0; i < len(p.Alphabet); i++ {
		c := p.Alphabet[i]
		if seen[c] {
			return fmt.Errorf("alphabet contains duplicate byte %q", c)
		}
		seen[c] = true
	}
	for i, v := range p.Vars {
		if v.Min < 0 || v.Max < v.Min {
			return fmt.Errorf("var %d: invalid length bounds [%d, %d]", i, v.Min, v.Max)
		}
	}
	n := len(p.Vars)
	for _, c := range p.Concat {
		for _, idx := range [3]int{c.Left, c.Right, c.Result} {
			if idx < 0 || idx >= n {
				return fmt.Errorf("concat constraint references invalid variable %d", idx)
			}
		}
	}
	return nil
}

// Solve finds the smallest satisfying assignment of p (total length first,
// then tuple-lexicographic), or proves none exists. budget is the maximum
// number of search states to explore; budget <= 0 means unlimited. If the
// budget is exhausted first, the result status is Unknown.
func Solve(p *Problem, budget int64) Result {
	s := &solver{p: p, budget: budget}
	for i := 0; i < len(p.Alphabet); i++ {
		c := p.Alphabet[i]
		if !s.inAlpha[c] {
			s.inAlpha[c] = true
			s.alpha = append(s.alpha, c)
		}
	}
	// sort alphabet ascending (insertion sort, alphabets are tiny)
	for i := 1; i < len(s.alpha); i++ {
		for j := i; j > 0 && s.alpha[j] < s.alpha[j-1]; j-- {
			s.alpha[j], s.alpha[j-1] = s.alpha[j-1], s.alpha[j]
		}
	}
	for _, f := range p.Forbidden {
		if len(f) == 0 {
			// Every string contains the empty substring.
			return Result{Status: Unsat}
		}
		if s.cleanWord(f) {
			s.forb = append(s.forb, []byte(f))
		}
		// Words with out-of-alphabet bytes can never occur; drop them.
	}

	n := len(p.Vars)
	sumLo, sumHi := 0, 0
	for _, v := range p.Vars {
		sumLo += v.Min
		sumHi += v.Max
	}

	var best []string
	for total := sumLo; total <= sumHi; total++ {
		lens := make([]int, n)
		set := make([]bool, n)
		abort := s.enumLens(lens, set, total, func(l []int) bool {
			a, found, abort := s.solveChars(l)
			if abort {
				return true
			}
			if found && (best == nil || lessAssign(a, best)) {
				best = a
			}
			return false
		})
		if abort {
			return Result{Status: Unknown, States: s.states}
		}
		if best != nil {
			return Result{Status: Sat, Assignment: best, States: s.states}
		}
	}
	return Result{Status: Unsat, States: s.states}
}

type solver struct {
	p       *Problem
	alpha   []byte
	inAlpha [256]bool
	forb    [][]byte
	budget  int64 // <= 0 means unlimited
	states  int64
}

func (s *solver) cleanWord(w string) bool {
	for i := 0; i < len(w); i++ {
		if !s.inAlpha[w[i]] {
			return false
		}
	}
	return true
}

func (s *solver) over() bool {
	return s.budget > 0 && s.states > s.budget
}

// enumLens enumerates length vectors summing to total that satisfy the
// bounds and the length consequences of the concat constraints, invoking cb
// on each. It returns true if the search was aborted by the budget or cb
// requested an abort.
func (s *solver) enumLens(lens []int, set []bool, total int, cb func([]int) bool) bool {
	s.states++
	if s.over() {
		return true
	}
	// Work on local copies so propagation does not leak across branches.
	lens = append([]int(nil), lens...)
	set = append([]bool(nil), set...)
	if !s.propagateLens(lens, set) {
		return false
	}
	next := -1
	sumSet, minRest, maxRest := 0, 0, 0
	for i := range lens {
		if set[i] {
			sumSet += lens[i]
		} else {
			if next < 0 {
				next = i
			}
			minRest += s.p.Vars[i].Min
			maxRest += s.p.Vars[i].Max
		}
	}
	if sumSet > total || sumSet+minRest > total || sumSet+maxRest < total {
		return false
	}
	if next < 0 {
		if sumSet != total {
			return false
		}
		return cb(lens)
	}
	v := s.p.Vars[next]
	for l := v.Min; l <= v.Max; l++ {
		lens[next] = l
		set[next] = true
		if s.enumLens(lens, set, total, cb) {
			return true
		}
		set[next] = false
	}
	return false
}

// propagateLens enforces len[Result] = len[Left] + len[Right] consequences
// to a fixpoint. It returns false on a detected inconsistency.
func (s *solver) propagateLens(lens []int, set []bool) bool {
	setLen := func(i, v int) bool {
		if set[i] {
			return lens[i] == v
		}
		if v < s.p.Vars[i].Min || v > s.p.Vars[i].Max {
			return false
		}
		lens[i] = v
		set[i] = true
		return true
	}
	changed := true
	for changed {
		changed = false
		for _, c := range s.p.Concat {
			sa, sb, sc := set[c.Left], set[c.Right], set[c.Result]
			switch {
			case sa && sb && sc:
				if lens[c.Left]+lens[c.Right] != lens[c.Result] {
					return false
				}
			case sa && sb:
				if !setLen(c.Result, lens[c.Left]+lens[c.Right]) {
					return false
				}
				changed = true
			case sa && sc:
				d := lens[c.Result] - lens[c.Left]
				if d < 0 || !setLen(c.Right, d) {
					return false
				}
				changed = true
			case sb && sc:
				d := lens[c.Result] - lens[c.Right]
				if d < 0 || !setLen(c.Left, d) {
					return false
				}
				changed = true
			}
		}
	}
	return true
}

// solveChars searches for the lexicographically smallest character
// assignment for a fixed length vector. It returns the assignment, whether
// one exists, and whether the budget aborted the search.
func (s *solver) solveChars(lens []int) ([]string, bool, bool) {
	n := len(lens)
	off := make([]int, n+1)
	for i := range lens {
		off[i+1] = off[i] + lens[i]
	}
	total := off[n]
	cs := &charSearch{
		s:      s,
		lens:   lens,
		off:    off,
		parent: make([]int, total),
		known:  make([]bool, total),
		val:    make([]byte, total),
	}
	for i := range cs.parent {
		cs.parent[i] = i
	}
	// Positions linked by concat constraints form equivalence classes:
	// x[Left][p] ~ x[Result][p] and x[Right][q] ~ x[Result][len(Left)+q].
	for _, c := range s.p.Concat {
		for pos := 0; pos < lens[c.Left]; pos++ {
			cs.union(off[c.Left]+pos, off[c.Result]+pos)
		}
		for pos := 0; pos < lens[c.Right]; pos++ {
			cs.union(off[c.Right]+pos, off[c.Result]+lens[c.Left]+pos)
		}
	}
	// Apply prefix/suffix constraints as fixed class bytes.
	for v, vb := range s.p.Vars {
		if len(vb.Prefix) > lens[v] || len(vb.Suffix) > lens[v] {
			return nil, false, false
		}
		for i := 0; i < len(vb.Prefix); i++ {
			if !s.fixByte(cs, off[v]+i, vb.Prefix[i]) {
				return nil, false, false
			}
		}
		for i := 0; i < len(vb.Suffix); i++ {
			pos := lens[v] - len(vb.Suffix) + i
			if !s.fixByte(cs, off[v]+pos, vb.Suffix[i]) {
				return nil, false, false
			}
		}
	}
	found, abort := cs.dfs()
	if !found || abort {
		return nil, false, abort
	}
	out := make([]string, n)
	for v := 0; v < n; v++ {
		b := make([]byte, lens[v])
		for i := range b {
			b[i] = cs.val[cs.find(off[v]+i)]
		}
		out[v] = string(b)
	}
	return out, true, false
}

func (s *solver) fixByte(cs *charSearch, pos int, ch byte) bool {
	if !s.inAlpha[ch] {
		return false
	}
	r := cs.find(pos)
	if cs.known[r] {
		return cs.val[r] == ch
	}
	cs.known[r] = true
	cs.val[r] = ch
	return true
}

// charSearch is the character-assignment DFS state for one length vector.
// The union-find structure is static per length vector; only known/val of
// class roots change during the search and are restored on backtracking.
type charSearch struct {
	s      *solver
	lens   []int
	off    []int
	parent []int
	known  []bool
	val    []byte
}

func (cs *charSearch) find(x int) int {
	for cs.parent[x] != x {
		cs.parent[x] = cs.parent[cs.parent[x]]
		x = cs.parent[x]
	}
	return x
}

func (cs *charSearch) union(a, b int) {
	ra, rb := cs.find(a), cs.find(b)
	if ra != rb {
		cs.parent[ra] = rb
	}
}

func (cs *charSearch) dfs() (found, abort bool) {
	cs.s.states++
	if cs.s.over() {
		return false, true
	}
	if cs.violated() {
		return false, false
	}
	root := -1
	for v := 0; v < len(cs.lens) && root < 0; v++ {
		for i := 0; i < cs.lens[v]; i++ {
			if r := cs.find(cs.off[v] + i); !cs.known[r] {
				root = r
				break
			}
		}
	}
	if root < 0 {
		return true, false
	}
	for _, ch := range cs.s.alpha {
		cs.known[root] = true
		cs.val[root] = ch
		found, abort := cs.dfs()
		if found || abort {
			return found, abort
		}
		cs.known[root] = false
	}
	return false, false
}

// violated reports whether some forbidden word is fully instantiated in a
// variable's partial assignment.
func (cs *charSearch) violated() bool {
	for v := range cs.lens {
		L := cs.lens[v]
		for _, f := range cs.s.forb {
			if len(f) > L {
				continue
			}
			for o := 0; o+len(f) <= L; o++ {
				match := true
				for k := 0; k < len(f); k++ {
					r := cs.find(cs.off[v] + o + k)
					if !cs.known[r] || cs.val[r] != f[k] {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
	}
	return false
}

// lessAssign orders assignments by total length, then tuple-lexicographic.
func lessAssign(a, b []string) bool {
	la, lb := 0, 0
	for i := range a {
		la += len(a[i])
		lb += len(b[i])
	}
	if la != lb {
		return la < lb
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
