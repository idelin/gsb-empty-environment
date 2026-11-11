package netsec

// solver is a self-contained recursive DPLL solver. Unit propagation scans
// the clauses directly. Inputs are bounded by the 2**12 shared-assignment
// enumeration, so the simple, auditable design is fast enough and the
// recursion depth stays small (at most the number of distinct variables).
type solver struct {
	clauses  []Clause
	nvars    int
	val      []bool // meaningful only when assigned[v]
	assigned []bool
	trail    []assignment
}

type assignment struct {
	v           int
	value       bool
	wasAssigned bool
}

func newSolver(cnf CNF) *solver {
	n := 0
	for _, c := range cnf {
		for _, l := range c {
			if v := l.Var() + 1; v > n {
				n = v
			}
		}
	}
	return &solver{
		clauses:  append([]Clause(nil), cnf...),
		nvars:    n,
		val:      make([]bool, n),
		assigned: make([]bool, n),
	}
}

// ensure grows the assignment arrays so variable v can be recorded. It is
// needed when a shared variable does not itself occur in the CNF but is
// fixed by solveUnder before the search starts.
func (s *solver) ensure(v int) {
	if v < s.nvars {
		return
	}
	grown := make([]bool, v+1)
	copy(grown, s.val)
	s.val = grown
	grownAssigned := make([]bool, v+1)
	copy(grownAssigned, s.assigned)
	s.assigned = grownAssigned
	s.nvars = v + 1
}

func (s *solver) literalValue(l Literal) (value bool, known bool) {
	idx := l.Var()
	if !s.assigned[idx] {
		return false, false
	}
	value = s.val[idx]
	if !l.Sign() {
		value = !value
	}
	return value, true
}

// assign sets v and records the previous state on the trail so that rollback
// restores exactly what the assignment overwrote, including parent-level
// assignments that happen to touch the same variable.
func (s *solver) assign(l Literal) {
	v := l.Var()
	s.ensure(v)
	if s.assigned[v] {
		return
	}
	s.trail = append(s.trail, assignment{v: v, value: s.val[v], wasAssigned: s.assigned[v]})
	s.val[v] = l.Sign()
	s.assigned[v] = true
}

// status summarizes a clause under the current partial assignment.
type clauseStatus int

const (
	clauseSatisfied clauseStatus = iota
	clauseOpen                   // not satisfied, at least one free literal
	clauseConflict               // every literal is false
)

func (s *solver) clauseStatus(c Clause) (clauseStatus, Literal, int) {
	var unit Literal
	free := 0
	for _, l := range c {
		if !s.assigned[l.Var()] {
			free++
			unit = l
			continue
		}
		v, _ := s.literalValue(l)
		if v {
			return clauseSatisfied, 0, 0
		}
	}
	if free == 0 {
		return clauseConflict, 0, 0
	}
	return clauseOpen, unit, free
}

// propagate applies forced unit literals until fixpoint, recording every
// assignment on the trail. A conflicting clause is returned on conflict.
func (s *solver) propagate() Clause {
	for {
		progressed := false
		for _, c := range s.clauses {
			st, unit, free := s.clauseStatus(c)
			switch st {
			case clauseConflict:
				return c
			case clauseOpen:
				// When exactly one literal is free it is the forced unit.
				// assign additionally ignores already-assigned variables.
				if free == 1 {
					s.assign(unit)
					progressed = true
				}
			}
		}
		if !progressed {
			return nil
		}
	}
}

// rollbackTo restores the solver state captured at trail length n.
func (s *solver) rollbackTo(n int) {
	for len(s.trail) > n {
		last := s.trail[len(s.trail)-1]
		s.trail = s.trail[:len(s.trail)-1]
		s.val[last.v] = last.value
		s.assigned[last.v] = last.wasAssigned
	}
}

// pickVar returns the lowest-index unassigned variable, or -1 when every
// variable known to the solver is assigned. A fixed order guarantees each
// decision advances the search instead of revisiting the same variable.
func (s *solver) pickVar() int {
	for v := 0; v < s.nvars; v++ {
		if !s.assigned[v] {
			return v
		}
	}
	return -1
}

// dpll performs the standard recursive search: unit propagation, then a
// binary decision on the first unassigned variable, trying both polarities.
// Every branch restores the trail to its entry snapshot, so assignments made
// on a failed subtree never leak into the next one.
func (s *solver) dpll() bool {
	snapshot := len(s.trail)
	if conflict := s.propagate(); conflict != nil {
		s.rollbackTo(snapshot)
		return false
	}
	v := s.pickVar()
	if v < 0 {
		return true
	}
	for _, polarity := range []bool{true, false} {
		l := MakeLit(v)
		if !polarity {
			l = l.Neg()
		}
		branch := len(s.trail)
		s.assign(l)
		if s.dpll() {
			return true
		}
		s.rollbackTo(branch)
	}
	s.rollbackTo(snapshot)
	return false
}

// model exports the current assignment as a total witness over every variable
// known to the solver; unassigned variables default to false.
func (s *solver) model() map[int]bool {
	out := make(map[int]bool, s.nvars)
	for v := 0; v < s.nvars; v++ {
		if s.assigned[v] {
			out[v] = s.val[v]
		} else {
			out[v] = false
		}
	}
	return out
}

// Sat reports whether cnf is satisfiable.
func Sat(cnf CNF) bool {
	sat, _ := Solve(cnf)
	return sat
}

// Solve checks satisfiability and, on SAT, returns a witnessing assignment
// covering every variable occurring in cnf. The witness is a total map for
// those variables; callers must not treat it as a proof of unsatisfiability.
func Solve(cnf CNF) (bool, map[int]bool) {
	if err := validateCNF(cnf); err != nil {
		return false, nil
	}
	s := newSolver(cnf)
	if s.dpll() {
		return true, s.model()
	}
	return false, nil
}

// solveUnder checks cnf after fixing the shared variables to assignment.
// Private variables remain free. The returned witness covers all variables
// occurring in cnf.
func solveUnder(cnf CNF, shared []int, assignment []bool) (bool, map[int]bool) {
	s := newSolver(cnf)
	for i, v := range shared {
		l := MakeLit(v)
		if !assignment[i] {
			l = l.Neg()
		}
		s.assign(l)
	}
	if s.dpll() {
		return true, s.model()
	}
	return false, nil
}

func validateCNF(cnf CNF) error {
	for ci, c := range cnf {
		for _, l := range c {
			if l.Var() < 0 {
				return &InputError{Op: "cnf", Clause: ci, Msg: "negative variable index"}
			}
			if l.Var() > MaxVarIndex {
				return &InputError{
					Op:     "cnf",
					Clause: ci,
					Msg:    "variable index " + itoa(l.Var()) + " exceeds MaxVarIndex",
				}
			}
		}
	}
	return nil
}

// InputError describes a malformed interpolation input.
type InputError struct {
	Op     string
	Clause int
	Msg    string
}

func (e *InputError) Error() string {
	if e.Op == "cnf" {
		return "netsec: invalid CNF clause " + itoa(e.Clause) + ": " + e.Msg
	}
	return "netsec: invalid input (" + e.Op + "): " + e.Msg
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
