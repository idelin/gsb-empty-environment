package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// MaxVars is the maximum number of boolean variables supported.
const MaxVars = 24

// Literal is a boolean variable or its negation. Var is a zero-based
// variable index in [0, NumVars).
type Literal struct {
	Var int
	Neg bool
}

// Pos returns the positive literal of variable v.
func Pos(v int) Literal { return Literal{Var: v} }

// Neg returns the negated literal of variable v.
func Neg(v int) Literal { return Literal{Var: v, Neg: true} }

// Clause is a disjunction of literals. An empty clause is unsatisfiable.
type Clause []Literal

// Group is a named set of clauses that can be dropped as a unit when
// resolving a conflict. IDs must be unique and non-empty.
type Group struct {
	ID      string
	Clauses []Clause
}

// Input describes one conflict-analysis problem.
type Input struct {
	// NumVars is the number of boolean variables, at most MaxVars.
	NumVars int
	// Background holds clauses that always apply and cannot be deleted.
	Background []Clause
	// Groups holds the removable clause groups.
	Groups []Group
}

// Assignment is a total variable assignment.
type Assignment struct {
	Values []bool
}

// Holds reports whether the assignment satisfies lit.
func (a Assignment) Holds(lit Literal) bool {
	return a.Values[lit.Var] != lit.Neg
}

// Satisfies reports whether the assignment satisfies every clause.
func (a Assignment) Satisfies(clauses []Clause) bool {
	for _, c := range clauses {
		ok := false
		for _, lit := range c {
			if a.Holds(lit) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Budget bounds the search effort. A zero Budget means unlimited.
type Budget struct {
	// MaxChecks caps the number of subset satisfiability checks.
	// Non-positive means unlimited.
	MaxChecks int64
}

// Result is the outcome of MinConflictCore.
type Result struct {
	// Core lists the sorted IDs of the returned unsatisfiable subset:
	// the background clauses plus these groups cannot be satisfied.
	// It is nil when no conflict was found or proven.
	Core []string
	// Optimal is false when the budget ran out before minimality was
	// proven; Core then holds the smallest proven core found so far.
	Optimal bool
	// Satisfiable reports that the background plus all groups is
	// satisfiable, so no conflict exists.
	Satisfiable bool
	// BackgroundUnsat reports that the background clauses alone are
	// contradictory; the empty core is then trivially optimal.
	BackgroundUnsat bool
	// Witnesses maps each core group ID to a satisfying assignment for
	// the background plus the core without that group, proving every
	// core group is necessary.
	Witnesses map[string]Assignment
	// Checks is the number of subset satisfiability checks performed.
	Checks int64
}

// MinConflictCore finds a subset of groups of minimum cardinality whose
// clauses together with the background are unsatisfiable. Ties are
// broken by the lexicographic order of the sorted ID list. For every
// group in the returned core it also computes a witness assignment
// satisfying the background and the remaining core groups.
//
// If the budget is exhausted before optimality is proven, the result
// carries the smallest core proven unsatisfiable so far (possibly none)
// and Optimal is false.
func MinConflictCore(in Input, b Budget) (Result, error) {
	if err := validate(in); err != nil {
		return Result{}, err
	}

	groups := make([]Group, len(in.Groups))
	copy(groups, in.Groups)
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })

	if _, ok := solve(in.NumVars, in.Background); !ok {
		return Result{
			Core:            []string{},
			Optimal:         true,
			BackgroundUnsat: true,
			Witnesses:       map[string]Assignment{},
		}, nil
	}

	limit := b.MaxChecks
	if limit <= 0 {
		limit = int64(^uint64(0) >> 1)
	}

	var res Result
	n := len(groups)
	// Enumerate subsets by increasing size; combinations of a given size
	// are generated in lexicographic index order, which matches the
	// lexicographic order of sorted ID lists, so the first conflict found
	// at the minimal size is the canonical answer.
	for k := 1; k <= n; k++ {
		idx := make([]int, k)
		for i := range idx {
			idx[i] = i
		}
		for {
			if res.Checks >= limit {
				return res, nil // budget exhausted: return proven core, if any
			}
			res.Checks++
			if _, ok := solve(in.NumVars, selectedClauses(in.Background, groups, idx)); !ok {
				res.Core = make([]string, k)
				for i, gi := range idx {
					res.Core[i] = groups[gi].ID
				}
				res.Optimal = true
				res.Witnesses = witnesses(in.NumVars, in.Background, groups, idx)
				return res, nil
			}
			if !nextCombination(idx, n) {
				break
			}
		}
	}

	// Every subset is satisfiable, including all groups together.
	res.Optimal = true
	res.Satisfiable = true
	res.Witnesses = map[string]Assignment{}
	return res, nil
}

// witnesses computes, for each group in the core, an assignment
// satisfying the background and the core without that group. Such an
// assignment always exists because the core is cardinality-minimal.
func witnesses(numVars int, background []Clause, groups []Group, core []int) map[string]Assignment {
	w := make(map[string]Assignment, len(core))
	for drop := 0; drop < len(core); drop++ {
		rest := make([]int, 0, len(core)-1)
		rest = append(rest, core[:drop]...)
		rest = append(rest, core[drop+1:]...)
		a, ok := solve(numVars, selectedClauses(background, groups, rest))
		if !ok {
			panic("netsec: minimal core subset is unsatisfiable")
		}
		w[groups[core[drop]].ID] = a
	}
	return w
}

func selectedClauses(background []Clause, groups []Group, idx []int) []Clause {
	total := len(background)
	for _, gi := range idx {
		total += len(groups[gi].Clauses)
	}
	out := make([]Clause, 0, total)
	out = append(out, background...)
	for _, gi := range idx {
		out = append(out, groups[gi].Clauses...)
	}
	return out
}

// nextCombination advances idx to the next k-combination of n in
// lexicographic order and reports whether one exists.
func nextCombination(idx []int, n int) bool {
	k := len(idx)
	for i := k - 1; i >= 0; i-- {
		if idx[i] < n-k+i {
			idx[i]++
			for j := i + 1; j < k; j++ {
				idx[j] = idx[j-1] + 1
			}
			return true
		}
	}
	return false
}

func validate(in Input) error {
	if in.NumVars < 0 || in.NumVars > MaxVars {
		return fmt.Errorf("netsec: NumVars %d out of range [0, %d]", in.NumVars, MaxVars)
	}
	check := func(cs []Clause) error {
		for _, c := range cs {
			for _, lit := range c {
				if lit.Var < 0 || lit.Var >= in.NumVars {
					return fmt.Errorf("netsec: literal variable %d out of range [0, %d)", lit.Var, in.NumVars)
				}
			}
		}
		return nil
	}
	if err := check(in.Background); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(in.Groups))
	for _, g := range in.Groups {
		if g.ID == "" {
			return errors.New("netsec: group ID must not be empty")
		}
		if _, dup := seen[g.ID]; dup {
			return fmt.Errorf("netsec: duplicate group ID %q", g.ID)
		}
		seen[g.ID] = struct{}{}
		if err := check(g.Clauses); err != nil {
			return err
		}
	}
	return nil
}
