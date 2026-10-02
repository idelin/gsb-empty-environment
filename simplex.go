// Package simplex implements an exact rational simplex method for linear
// programs in the standard inequality form
//
//	maximize    c^T x
//	subject to  A x <= b
//	            x >= 0
//
// All arithmetic is performed with math/big.Rat, so every pivot, basis
// exchange and the final result are exact rational numbers with no
// floating-point loss. The solver uses the two-phase method (phase 1 finds
// a feasible basis when some b_i < 0) and Bland's rule, which guarantees
// termination even on degenerate/cycling problems.
//
// Besides the optimal primal solution, Solve returns the exact dual
// variables (shadow prices), an unboundedness ray when the LP is unbounded,
// or a Farkas infeasibility certificate when the LP is infeasible.
package simplex

import (
	"fmt"
	"math/big"
	"strings"
)

// Status is the outcome of Solve.
type Status int

const (
	// Optimal: a globally optimal rational solution was found.
	Optimal Status = iota
	// Unbounded: the objective grows without bound along Result.Ray.
	Unbounded
	// Infeasible: the feasible region is empty; see Result.Farkas.
	Infeasible
)

func (s Status) String() string {
	switch s {
	case Optimal:
		return "Optimal"
	case Unbounded:
		return "Unbounded"
	case Infeasible:
		return "Infeasible"
	}
	return "Unknown"
}

// Pivot records one exact rational basis exchange.
type Pivot struct {
	Iteration int      // 1-based iteration counter
	Phase     int      // simplex phase (1 or 2)
	Enter     int      // entering variable index (column)
	Leave     int      // leaving variable index (previous basic variable)
	Value     *big.Rat // exact rational pivot element
}

// Result is the exact outcome of Solve.
type Result struct {
	Status     Status
	Objective  *big.Rat   // exact optimal objective value (Optimal only)
	X          []*big.Rat // primal optimal solution, length n (Optimal only)
	Dual       []*big.Rat // dual variables / shadow prices, length m (Optimal only)
	Ray        []*big.Rat // recession direction proving unboundedness (Unbounded only)
	Farkas     []*big.Rat // Farkas certificate y: y>=0, A^T y>=0, b^T y<0 (Infeasible only)
	Iterations int
	Pivots     []Pivot // full basis-exchange history, all rational
}

func zero() *big.Rat { return new(big.Rat) }
func one() *big.Rat  { return big.NewRat(1, 1) }

// Solve maximizes c^T x subject to A x <= b, x >= 0, using exact rational
// arithmetic. c has length n, A is m x n, b has length m.
func Solve(c []*big.Rat, A [][]*big.Rat, b []*big.Rat) *Result {
	n := len(c)
	m := len(b)
	if len(A) != m {
		panic("simplex: len(A) != len(b)")
	}
	for i := range A {
		if len(A[i]) != n {
			panic("simplex: ragged A")
		}
	}

	// Tableau columns: [x_0..x_{n-1} | s_0..s_{m-1} | a_0..a_{m-1} | rhs].
	// s_i are slack variables; a_i are artificial variables used only for
	// rows that had to be sign-flipped because b_i < 0.
	width := n + 2*m + 1
	rhs := width - 1
	slackCol := func(i int) int { return n + i }
	artCol := func(i int) int { return n + m + i }

	T := make([][]*big.Rat, m) // constraint rows
	basis := make([]int, m)    // basic variable per row
	needPhase1 := false
	for i := 0; i < m; i++ {
		sign := int64(1)
		if b[i].Sign() < 0 {
			sign = -1
		}
		s := big.NewRat(sign, 1)
		row := make([]*big.Rat, width)
		for j := range row {
			row[j] = zero()
		}
		for j := 0; j < n; j++ {
			row[j] = new(big.Rat).Mul(s, A[i][j])
		}
		row[slackCol(i)] = new(big.Rat).Set(s)
		row[rhs] = new(big.Rat).Mul(s, b[i])
		if sign < 0 {
			row[artCol(i)] = one()
			basis[i] = artCol(i)
			needPhase1 = true
		} else {
			basis[i] = slackCol(i)
		}
		T[i] = row
	}

	res := &Result{}

	// pivot normalizes row p by the exact rational pivot element and
	// eliminates column q from every other row, including the objective row.
	pivot := func(obj []*big.Rat, p, q int) {
		inv := new(big.Rat).Inv(T[p][q])
		for j := 0; j < width; j++ {
			T[p][j].Mul(T[p][j], inv)
		}
		eliminate := func(r []*big.Rat) {
			f := new(big.Rat).Set(r[q])
			if f.Sign() == 0 {
				return
			}
			for j := 0; j < width; j++ {
				if j == q {
					continue
				}
				r[j].Sub(r[j], new(big.Rat).Mul(f, T[p][j]))
			}
			r[q] = zero()
		}
		for i := 0; i < m; i++ {
			if i != p {
				eliminate(T[i])
			}
		}
		eliminate(obj)
	}

	// buildObj constructs the objective row for the given extended cost
	// vector: obj[j] = c_B^T B^{-1} A_j - c_j, obj[rhs] = current objective.
	// Optimality (for maximization) is obj[j] >= 0 for all eligible j.
	buildObj := func(cost []*big.Rat) []*big.Rat {
		obj := make([]*big.Rat, width)
		for j := 0; j < width; j++ {
			acc := zero()
			for i := 0; i < m; i++ {
				cb := cost[basis[i]]
				if cb.Sign() != 0 {
					acc.Add(acc, new(big.Rat).Mul(cb, T[i][j]))
				}
			}
			obj[j] = acc.Sub(acc, cost[j])
		}
		return obj
	}

	// run executes the simplex loop with Bland's rule. allow selects which
	// columns may enter the basis. It returns the entering column when the
	// problem is unbounded, or -1 at optimality.
	run := func(obj []*big.Rat, phase int, allow func(j int) bool) int {
		for {
			enter := -1
			for j := 0; j < rhs; j++ {
				if allow(j) && obj[j].Sign() < 0 {
					enter = j // Bland: smallest index with negative reduced cost
					break
				}
			}
			if enter == -1 {
				return -1
			}
			leave := -1
			var best *big.Rat
			for i := 0; i < m; i++ {
				if T[i][enter].Sign() <= 0 {
					continue
				}
				ratio := new(big.Rat).Quo(T[i][rhs], T[i][enter])
				if leave == -1 || ratio.Cmp(best) < 0 ||
					(ratio.Cmp(best) == 0 && basis[i] < basis[leave]) {
					leave, best = i, ratio
				}
			}
			if leave == -1 {
				return enter // unbounded direction
			}
			res.Iterations++
			res.Pivots = append(res.Pivots, Pivot{
				Iteration: res.Iterations,
				Phase:     phase,
				Enter:     enter,
				Leave:     basis[leave],
				Value:     new(big.Rat).Set(T[leave][enter]),
			})
			pivot(obj, leave, enter)
			basis[leave] = enter
		}
	}

	// ---- Phase 1: maximize -(sum of artificials) to find a feasible basis.
	if needPhase1 {
		cost := make([]*big.Rat, width)
		for j := range cost {
			cost[j] = zero()
		}
		for i := 0; i < m; i++ {
			if basis[i] == artCol(i) {
				cost[artCol(i)] = big.NewRat(-1, 1)
			}
		}
		obj := buildObj(cost)
		run(obj, 1, func(j int) bool { return true })
		if obj[rhs].Sign() < 0 {
			// Infeasible. The phase-1 duals y_i = obj[slack_i] form a Farkas
			// certificate: y >= 0, A^T y >= 0, b^T y < 0.
			res.Status = Infeasible
			res.Farkas = make([]*big.Rat, m)
			for i := 0; i < m; i++ {
				res.Farkas[i] = new(big.Rat).Set(obj[slackCol(i)])
			}
			return res
		}
	}

	// ---- Phase 2: optimize the true objective from the feasible basis.
	cost := make([]*big.Rat, width)
	for j := range cost {
		cost[j] = zero()
	}
	for j := 0; j < n; j++ {
		cost[j] = new(big.Rat).Set(c[j])
	}
	obj := buildObj(cost)
	// Artificial columns are excluded so they never re-enter the basis.
	enter := run(obj, 2, func(j int) bool { return j < n+m })

	if enter >= 0 {
		// Unbounded: ray d with d_enter = 1, d_basis[i] = -T[i][enter].
		res.Status = Unbounded
		res.Ray = make([]*big.Rat, n)
		for j := 0; j < n; j++ {
			res.Ray[j] = zero()
		}
		if enter < n {
			res.Ray[enter] = one()
		}
		for i := 0; i < m; i++ {
			if basis[i] < n {
				res.Ray[basis[i]] = new(big.Rat).Neg(T[i][enter])
			}
		}
		return res
	}

	res.Status = Optimal
	res.Objective = new(big.Rat).Set(obj[rhs])
	res.X = make([]*big.Rat, n)
	for j := 0; j < n; j++ {
		res.X[j] = zero()
	}
	for i := 0; i < m; i++ {
		if basis[i] < n {
			res.X[basis[i]] = new(big.Rat).Set(T[i][rhs])
		}
	}
	// Dual variables (shadow prices): y_i equals the objective-row entry
	// under slack column i, i.e. y^T = c_B^T B^{-1}.
	res.Dual = make([]*big.Rat, m)
	for i := 0; i < m; i++ {
		res.Dual[i] = new(big.Rat).Set(obj[slackCol(i)])
	}
	return res
}

// String renders a compact human-readable summary of the result.
func (r *Result) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "status: %s (iterations: %d)\n", r.Status, r.Iterations)
	switch r.Status {
	case Optimal:
		fmt.Fprintf(&sb, "objective: %s\n", r.Objective)
		fmt.Fprintf(&sb, "x = %s\n", ratsString(r.X))
		fmt.Fprintf(&sb, "dual y = %s\n", ratsString(r.Dual))
	case Unbounded:
		fmt.Fprintf(&sb, "unbounded ray d = %s\n", ratsString(r.Ray))
	case Infeasible:
		fmt.Fprintf(&sb, "Farkas certificate y = %s\n", ratsString(r.Farkas))
	}
	return sb.String()
}

func ratsString(v []*big.Rat) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = x.RatString()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
