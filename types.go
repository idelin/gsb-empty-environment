package netsec

// Literal is a signed Boolean variable.
//
// A positive literal is encoded as 2*v and its negation as 2*v+1, where v is
// the zero-based variable index. Use MakeLit and Neg to construct literals.
type Literal uint32

// MakeLit returns the positive literal of variable v (zero-based).
func MakeLit(v int) Literal { return Literal(uint32(v) << 1) }

// Var returns the zero-based variable index of the literal.
func (l Literal) Var() int { return int(l >> 1) }

// Neg returns the complementary literal.
func (l Literal) Neg() Literal { return l ^ 1 }

// Sign reports whether the literal is positive (not negated).
func (l Literal) Sign() bool { return l&1 == 0 }

// Clause is a disjunction of literals.
type Clause []Literal

// CNF is a conjunction of clauses.
//
// An empty CNF (no clauses) is the constant true. A CNF containing an empty
// clause is the constant false.
type CNF []Clause

// Eval evaluates the literal under a total assignment.
// A missing variable is treated as unassigned and causes ok=false.
func (l Literal) Eval(a map[int]bool) (val, ok bool) {
	v, present := a[l.Var()]
	if !present {
		return false, false
	}
	if !l.Sign() {
		v = !v
	}
	return v, true
}

// Eval evaluates the clause under a total assignment.
func (c Clause) Eval(a map[int]bool) (val, ok bool) {
	for _, l := range c {
		v, known := l.Eval(a)
		if !known {
			return false, false
		}
		if v {
			return true, true
		}
	}
	return false, true
}

// Eval evaluates the CNF under a total assignment.
func (f CNF) Eval(a map[int]bool) (val, ok bool) {
	for _, c := range f {
		v, known := c.Eval(a)
		if !known {
			return false, false
		}
		if !v {
			return false, true
		}
	}
	return true, true
}

// Vars returns the sorted, deduplicated set of variables used by the CNF.
func (f CNF) Vars() []int {
	seen := map[int]struct{}{}
	for _, c := range f {
		for _, l := range c {
			seen[l.Var()] = struct{}{}
		}
	}
	out := make([]int, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sortInts(out)
	return out
}

func sortInts(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// MaxSharedVars is the supported upper bound on shared variables. Enumerating
// all shared assignments costs 2**n, so the bound keeps verification bounded.
const MaxSharedVars = 12

// MaxVarIndex bounds every variable index accepted by the package. It keeps
// solver allocation proportional to realistic analysis inputs and rejects
// pathological encodings up front.
const MaxVarIndex = 4095
