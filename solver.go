package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// MaxVars is the maximum number of boolean variables a Solver supports.
const MaxVars = 16

// maxLevels bounds the assumption stack depth so dependency sets fit in a uint64.
const maxLevels = 64

// Lit is a signed boolean literal. Variable v (1..MaxVars) appears as Pos(v)
// for the positive phase and Neg(v) for the negative phase.
type Lit int32

// Pos returns the positive literal of variable v.
func Pos(v int) Lit { return Lit(v) }

// Neg returns the negative literal of variable v.
func Neg(v int) Lit { return -Lit(v) }

func varOf(l Lit) int {
	if l < 0 {
		return int(-l)
	}
	return int(l)
}

var (
	// ErrTooManyVars is returned when the requested variable count exceeds MaxVars.
	ErrTooManyVars = errors.New("netsec: variable count exceeds MaxVars")
	// ErrBadLiteral is returned when a literal references a variable outside 1..nvars.
	ErrBadLiteral = errors.New("netsec: literal out of range")
	// ErrTooManyLevels is returned when the assumption stack would exceed 64 levels.
	ErrTooManyLevels = errors.New("netsec: assumption stack deeper than 64")
)

// learnedClause is a clause derived during search. deps is a bitmask of the
// assumption levels whose literals were used to derive it; the clause is
// discarded as soon as any of those levels is popped.
type learnedClause struct {
	lits []Lit
	deps uint64
}

// Solver is an incremental boolean solver with permanent clauses and a
// stack of nested temporary assumption levels. It is not safe for
// concurrent use.
type Solver struct {
	nvars       int
	perm        [][]Lit
	learned     []learnedClause
	learnedKeys map[string]struct{}
	levels      [][]Lit
}

// New creates a solver over variables 1..nvars, where nvars <= MaxVars.
func New(nvars int) (*Solver, error) {
	if nvars < 0 || nvars > MaxVars {
		return nil, fmt.Errorf("%w: %d", ErrTooManyVars, nvars)
	}
	return &Solver{nvars: nvars, learnedKeys: make(map[string]struct{})}, nil
}

// NumVars returns the number of variables the solver was created with.
func (s *Solver) NumVars() int { return s.nvars }

// Depth returns the current number of assumption levels on the stack.
func (s *Solver) Depth() int { return len(s.levels) }

// LearnedCount returns the number of retained learned clauses.
func (s *Solver) LearnedCount() int { return len(s.learned) }

func (s *Solver) checkLits(lits []Lit) error {
	for _, l := range lits {
		if v := varOf(l); v < 1 || v > s.nvars {
			return fmt.Errorf("%w: %d", ErrBadLiteral, int(l))
		}
	}
	return nil
}

// AddClause adds a permanent clause. Permanent clauses survive Pop and
// participate in every subsequent Solve.
func (s *Solver) AddClause(lits ...Lit) error {
	if err := s.checkLits(lits); err != nil {
		return err
	}
	s.perm = append(s.perm, append([]Lit(nil), lits...))
	return nil
}

// Push opens a new assumption level holding the given literals and returns
// its index. Assumptions behave as unit clauses valid only until the level
// is removed with Pop.
func (s *Solver) Push(assumptions ...Lit) (int, error) {
	if err := s.checkLits(assumptions); err != nil {
		return 0, err
	}
	if len(s.levels) >= maxLevels {
		return 0, ErrTooManyLevels
	}
	s.levels = append(s.levels, append([]Lit(nil), assumptions...))
	return len(s.levels) - 1, nil
}

// Pop removes the most recent assumption level and every learned clause
// whose derivation depended on it. Learned clauses that only depend on
// surviving levels (or on permanent clauses alone) are kept.
func (s *Solver) Pop() {
	n := len(s.levels)
	if n == 0 {
		return
	}
	revoked := uint64(1) << uint(n-1)
	s.levels = s.levels[:n-1]
	kept := s.learned[:0]
	for _, lc := range s.learned {
		if lc.deps&revoked == 0 {
			kept = append(kept, lc)
		} else {
			delete(s.learnedKeys, clauseKey(lc.lits))
		}
	}
	for i := len(kept); i < len(s.learned); i++ {
		s.learned[i] = learnedClause{}
	}
	s.learned = kept
}

// Result is the outcome of a Solve call.
type Result struct {
	// SAT reports whether the permanent clauses plus the active assumption
	// levels are satisfiable.
	SAT bool
	// Model, when SAT is true, holds the satisfying assignment. It has
	// length nvars+1; Model[v] is the value of variable v (index 0 unused).
	Model []bool
	// Core, when SAT is false, lists the assumption levels whose
	// assumptions participate in the refusal: the permanent clauses
	// together with only those levels are already unsatisfiable. An empty
	// Core means the permanent clauses alone are unsatisfiable.
	Core []int
}

// clause pairs clause literals with the dependency set of their origin.
type clause struct {
	lits []Lit
	deps uint64
}

// Solve checks satisfiability of the permanent clauses under the currently
// active assumption levels, reusing learned clauses retained from earlier
// calls.
func (s *Solver) Solve() Result {
	cs := make([]clause, 0, len(s.perm)+len(s.learned)+len(s.levels))
	for _, c := range s.perm {
		cs = append(cs, clause{lits: c})
	}
	for _, lc := range s.learned {
		cs = append(cs, clause{lits: lc.lits, deps: lc.deps})
	}
	for lvl, assumptions := range s.levels {
		for _, l := range assumptions {
			cs = append(cs, clause{lits: []Lit{l}, deps: uint64(1) << uint(lvl)})
		}
	}
	assign := make([]int8, s.nvars+1) // 0 unset, 1 true, -1 false
	adeps := make([]uint64, s.nvars+1)
	sat, cdeps := s.search(&cs, assign, adeps)
	if !sat {
		return Result{SAT: false, Core: levelsOf(cdeps)}
	}
	model := make([]bool, s.nvars+1)
	for v := 1; v <= s.nvars; v++ {
		model[v] = assign[v] == 1
	}
	return Result{SAT: true, Model: model}
}

func levelsOf(deps uint64) []int {
	var out []int
	for lvl := 0; lvl < maxLevels; lvl++ {
		if deps&(uint64(1)<<uint(lvl)) != 0 {
			out = append(out, lvl)
		}
	}
	return out
}

// search runs chronological-backtracking DPLL. On success it fills assign
// and returns true. On failure it returns the bitmask of assumption levels
// responsible for the conflict and records a learned clause for them.
func (s *Solver) search(cs *[]clause, assign []int8, adeps []uint64) (bool, uint64) {
	for {
		progressed := false
		for _, c := range *cs {
			unassigned := 0
			var last Lit
			var falsDeps uint64
			satisfied := false
			for _, l := range c.lits {
				v := varOf(l)
				switch a := assign[v]; {
				case a == 0:
					unassigned++
					last = l
				case (a == 1) == (l > 0):
					satisfied = true
				default:
					falsDeps |= adeps[v]
				}
				if satisfied {
					break
				}
			}
			if satisfied {
				continue
			}
			if unassigned == 0 {
				cdeps := c.deps | falsDeps
				s.learn(cdeps, cs)
				return false, cdeps
			}
			if unassigned == 1 {
				v := varOf(last)
				if last > 0 {
					assign[v] = 1
				} else {
					assign[v] = -1
				}
				adeps[v] = c.deps | falsDeps
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	decisionVar := 0
	for v := 1; v <= s.nvars; v++ {
		if assign[v] == 0 {
			decisionVar = v
			break
		}
	}
	if decisionVar == 0 {
		return true, 0
	}
	var allDeps uint64
	for _, val := range []int8{1, -1} {
		a2 := append([]int8(nil), assign...)
		d2 := append([]uint64(nil), adeps...)
		a2[decisionVar] = val
		sat, cdeps := s.search(cs, a2, d2)
		if sat {
			copy(assign, a2)
			return true, 0
		}
		allDeps |= cdeps
	}
	s.learn(allDeps, cs)
	return false, allDeps
}

// learn records the clause "the assumptions of the given levels do not all
// hold", which is a logical consequence of the permanent clauses alone.
func (s *Solver) learn(deps uint64, cs *[]clause) {
	var lits []Lit
	if deps != 0 {
		for lvl := 0; lvl < len(s.levels); lvl++ {
			if deps&(uint64(1)<<uint(lvl)) == 0 {
				continue
			}
			for _, l := range s.levels[lvl] {
				lits = append(lits, -l)
			}
		}
		if len(lits) == 0 {
			return
		}
	}
	key := clauseKey(lits)
	if _, ok := s.learnedKeys[key]; ok {
		return
	}
	s.learnedKeys[key] = struct{}{}
	s.learned = append(s.learned, learnedClause{lits: lits, deps: deps})
	*cs = append(*cs, clause{lits: lits, deps: deps})
}

func clauseKey(lits []Lit) string {
	sorted := append([]Lit(nil), lits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var b strings.Builder
	for i, l := range sorted {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(int(l)))
	}
	return b.String()
}
