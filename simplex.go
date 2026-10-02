// Package simplex solves linear programs exactly with the simplex method.
//
// All arithmetic is performed on *big.Rat, so every pivot, reduced cost and
// ratio test is exact rational arithmetic: there is no floating-point
// precision loss at any stage of the algorithm.
//
// The solver accepts LPs in standard inequality form
//
//	maximize   C^T x
//	subject to A x <= B
//	           x >= 0
//
// and returns the exact optimal primal solution together with the exact dual
// values (shadow prices), or a certificate of unboundedness (a feasible
// point plus a recession ray) or infeasibility (a Farkas vector).
package simplex

import (
	"fmt"
	"math/big"
	"strings"
)

// Status is the outcome of solving an LP.
type Status int

const (
	// Optimal means a provably optimal rational solution was found.
	Optimal Status = iota
	// Unbounded means the objective can grow without bound; Result.Ray
	// and Result.Primal (a feasible point) certify it.
	Unbounded
	// Infeasible means the feasible region is empty; Result.Farkas
	// certifies it.
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

// LP is a linear program in standard inequality form:
//
//	maximize   C^T x
//	subject to A x <= B
//	           x >= 0
type LP struct {
	A [][]*big.Rat // m constraint rows, each of length n
	B []*big.Rat   // m right-hand sides
	C []*big.Rat   // n objective coefficients (maximized)
}

// Result is the exact outcome of Solve.
type Result struct {
	Status Status

	// Objective is the exact optimal objective value (Optimal only).
	Objective *big.Rat
	// Primal is the optimal solution vector x (Optimal), or a feasible
	// point witnessing unboundedness (Unbounded).
	Primal []*big.Rat
	// Dual holds the m exact dual values (shadow prices), one per
	// constraint (Optimal only). They satisfy strong duality:
	// C^T Primal == B^T Dual.
	Dual []*big.Rat

	// Ray is a recession direction d >= 0 with A d <= 0 and C^T d > 0
	// (Unbounded only).
	Ray []*big.Rat
	// Farkas is a vector y >= 0 with y^T A >= 0 and y^T B < 0, proving
	// infeasibility (Infeasible only).
	Farkas []*big.Rat
}

// Rat parses a rational constant such as "3/2", "-7" or "0.25".
// It panics on malformed input; handy for constructing LPs from literals.
func Rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic(fmt.Sprintf("simplex: invalid rational %q", s))
	}
	return r
}

func clone(r *big.Rat) *big.Rat { return new(big.Rat).Set(r) }

func zeroVec(n int) []*big.Rat {
	v := make([]*big.Rat, n)
	for i := range v {
		v[i] = new(big.Rat)
	}
	return v
}

func (lp *LP) validate() error {
	n := len(lp.C)
	if n == 0 {
		return fmt.Errorf("simplex: objective vector C is empty")
	}
	if len(lp.A) != len(lp.B) {
		return fmt.Errorf("simplex: len(A)=%d != len(B)=%d", len(lp.A), len(lp.B))
	}
	for i, row := range lp.A {
		if len(row) != n {
			return fmt.Errorf("simplex: row A[%d] has length %d, want %d", i, len(row), n)
		}
	}
	return nil
}

// tableau is the simplex tableau in the form [B^-1 A | B^-1 b] with a
// separate reduced-cost row. Entry [i][cols] is the right-hand side.
type tableau struct {
	rows, cols int
	a          [][]*big.Rat
	rc         []*big.Rat // reduced costs, length cols
	obj        *big.Rat   // objective value at the current basic solution
	basis      []int      // basis[i] = column of the basic variable in row i
}

func newTableau(rows, cols int) *tableau {
	t := &tableau{
		rows:  rows,
		cols:  cols,
		a:     make([][]*big.Rat, rows),
		rc:    zeroVec(cols),
		obj:   new(big.Rat),
		basis: make([]int, rows),
	}
	for i := range t.a {
		t.a[i] = zeroVec(cols + 1)
	}
	return t
}

// pivot performs an exact Gauss-Jordan pivot on element (r, c).
func (t *tableau) pivot(r, c int) {
	piv := clone(t.a[r][c])
	for j := 0; j <= t.cols; j++ {
		t.a[r][j].Quo(t.a[r][j], piv)
	}
	for i := 0; i < t.rows; i++ {
		if i == r || t.a[i][c].Sign() == 0 {
			continue
		}
		f := clone(t.a[i][c])
		for j := 0; j <= t.cols; j++ {
			t.a[i][j].Sub(t.a[i][j], new(big.Rat).Mul(f, t.a[r][j]))
		}
	}
	if f := clone(t.rc[c]); f.Sign() != 0 {
		for j := 0; j < t.cols; j++ {
			t.rc[j].Sub(t.rc[j], new(big.Rat).Mul(f, t.a[r][j]))
		}
		t.obj.Add(t.obj, new(big.Rat).Mul(f, t.a[r][t.cols]))
	}
	t.basis[r] = c
}

// setObjective recomputes the reduced-cost row and objective value for cost
// vector c, given the current basis.
func (t *tableau) setObjective(c []*big.Rat) {
	for j := 0; j < t.cols; j++ {
		t.rc[j].Set(c[j])
	}
	t.obj.SetInt64(0)
	for i := 0; i < t.rows; i++ {
		cb := c[t.basis[i]]
		if cb.Sign() == 0 {
			continue
		}
		for j := 0; j < t.cols; j++ {
			t.rc[j].Sub(t.rc[j], new(big.Rat).Mul(cb, t.a[i][j]))
		}
		t.obj.Add(t.obj, new(big.Rat).Mul(cb, t.a[i][t.cols]))
	}
}

// run iterates the primal simplex method with Bland's rule (which guarantees
// termination). banned marks columns that may not enter the basis (artificial
// variables after phase 1). It returns the entering column witnessing
// unboundedness, or -1 at optimality.
func (t *tableau) run(banned func(int) bool) int {
	for {
		enter := -1
		for j := 0; j < t.cols; j++ {
			if banned != nil && banned(j) {
				continue
			}
			if t.rc[j].Sign() > 0 {
				enter = j
				break
			}
		}
		if enter < 0 {
			return -1 // optimal: all reduced costs <= 0
		}
		leave := -1
		var best *big.Rat
		for i := 0; i < t.rows; i++ {
			if t.a[i][enter].Sign() <= 0 {
				continue
			}
			ratio := new(big.Rat).Quo(t.a[i][t.cols], t.a[i][enter])
			if leave < 0 || ratio.Cmp(best) < 0 ||
				(ratio.Cmp(best) == 0 && t.basis[i] < t.basis[leave]) {
				leave, best = i, ratio
			}
		}
		if leave < 0 {
			return enter // unbounded: no blocking row
		}
		t.pivot(leave, enter)
	}
}

// dropRow removes a (redundant) constraint row.
func (t *tableau) dropRow(i int) {
	t.a = append(t.a[:i], t.a[i+1:]...)
	t.basis = append(t.basis[:i], t.basis[i+1:]...)
	t.rows--
}

// Solve solves lp exactly and returns the optimal primal and dual solutions
// or the appropriate certificate.
func Solve(lp *LP) (*Result, error) {
	if err := lp.validate(); err != nil {
		return nil, err
	}
	n := len(lp.C)
	m := len(lp.B)

	// Column layout: [x (n) | slack (m) | artificial (k)].
	artOf := make([]int, m)
	numArt := 0
	for i := 0; i < m; i++ {
		if lp.B[i].Sign() < 0 {
			artOf[i] = n + m + numArt
			numArt++
		} else {
			artOf[i] = -1
		}
	}
	cols := n + m + numArt
	t := newTableau(m, cols)

	for i := 0; i < m; i++ {
		row := t.a[i]
		if lp.B[i].Sign() >= 0 {
			for j := 0; j < n; j++ {
				row[j].Set(lp.A[i][j])
			}
			row[n+i].SetInt64(1) // slack s_i is basic
			row[cols].Set(lp.B[i])
			t.basis[i] = n + i
		} else {
			// A_i x + s_i - a_i = b_i, multiplied by -1 so that the
			// artificial variable a_i forms a feasible basis column.
			for j := 0; j < n; j++ {
				row[j].Neg(lp.A[i][j])
			}
			row[n+i].SetInt64(-1)
			row[artOf[i]].SetInt64(1)
			row[cols].Neg(lp.B[i])
			t.basis[i] = artOf[i]
		}
	}

	isArtificial := func(j int) bool { return j >= n+m }

	// ---- Phase 1: minimize the sum of artificial variables ----
	if numArt > 0 {
		c1 := zeroVec(cols)
		for j := n + m; j < cols; j++ {
			c1[j].SetInt64(-1) // maximize -sum(a_i)
		}
		t.setObjective(c1)
		t.run(nil)
		if t.obj.Sign() < 0 {
			// Infeasible. The negated reduced costs of the slack columns
			// form a Farkas certificate y >= 0, y^T A >= 0, y^T B < 0.
			farkas := make([]*big.Rat, m)
			for i := 0; i < m; i++ {
				farkas[i] = new(big.Rat).Neg(t.rc[n+i])
			}
			return &Result{Status: Infeasible, Farkas: farkas}, nil
		}
		// Drive remaining basic artificials (value 0) out of the basis;
		// rows that are identically zero in real columns are redundant.
		for i := 0; i < t.rows; i++ {
			if !isArtificial(t.basis[i]) {
				continue
			}
			piv := -1
			for j := 0; j < n+m; j++ {
				if t.a[i][j].Sign() != 0 {
					piv = j
					break
				}
			}
			if piv >= 0 {
				t.pivot(i, piv)
			} else {
				t.dropRow(i)
				i--
			}
		}
	}

	// ---- Phase 2: optimize the true objective ----
	c2 := zeroVec(cols)
	for j := 0; j < n; j++ {
		c2[j].Set(lp.C[j])
	}
	t.setObjective(c2)
	if e := t.run(isArtificial); e >= 0 {
		return &Result{
			Status: Unbounded,
			Primal: t.primalSolution(n),
			Ray:    t.unboundedRay(n, e),
		}, nil
	}

	dual := make([]*big.Rat, m)
	for i := 0; i < m; i++ {
		dual[i] = new(big.Rat).Neg(t.rc[n+i])
	}
	return &Result{
		Status:    Optimal,
		Objective: clone(t.obj),
		Primal:    t.primalSolution(n),
		Dual:      dual,
	}, nil
}

// primalSolution reads the current basic feasible solution (x part).
func (t *tableau) primalSolution(n int) []*big.Rat {
	x := zeroVec(n)
	for i := 0; i < t.rows; i++ {
		if b := t.basis[i]; b < n {
			x[b].Set(t.a[i][t.cols])
		}
	}
	return x
}

// unboundedRay builds the recession direction for entering column e:
// d_e = 1, d_basic = -column entries, everything else 0.
func (t *tableau) unboundedRay(n, e int) []*big.Rat {
	d := zeroVec(n)
	if e < n {
		d[e].SetInt64(1)
	}
	for i := 0; i < t.rows; i++ {
		if b := t.basis[i]; b < n {
			d[b].Neg(t.a[i][e])
		}
	}
	return d
}

func ratSliceString(v []*big.Rat) string {
	parts := make([]string, len(v))
	for i, r := range v {
		parts[i] = r.RatString()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// String renders the result in a compact human-readable form.
func (r *Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "status: %s", r.Status)
	switch r.Status {
	case Optimal:
		fmt.Fprintf(&b, "\nobjective: %s", r.Objective.RatString())
		fmt.Fprintf(&b, "\nprimal x:  %s", ratSliceString(r.Primal))
		fmt.Fprintf(&b, "\ndual y:    %s", ratSliceString(r.Dual))
	case Unbounded:
		fmt.Fprintf(&b, "\nfeasible point: %s", ratSliceString(r.Primal))
		fmt.Fprintf(&b, "\nray:            %s", ratSliceString(r.Ray)+"\n(objective grows without bound along point + t*ray, t >= 0)")
	case Infeasible:
		fmt.Fprintf(&b, "\nFarkas certificate y: %s", ratSliceString(r.Farkas))
		fmt.Fprintf(&b, "\n(y >= 0, y^T A >= 0, y^T B < 0 proves infeasibility)")
	}
	return b.String()
}
