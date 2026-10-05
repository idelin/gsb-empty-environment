package main

import (
	"math/big"
)

// LPStatus is the outcome of an exact rational LP solve.
type LPStatus string

const (
	LPOptimal    LPStatus = "optimal"
	LPUnbounded  LPStatus = "unbounded"
	LPInfeasible LPStatus = "infeasible"
)

// LPResult carries the solve outcome; Point is in the caller's variable space.
type LPResult struct {
	Status LPStatus
	Value  *big.Rat
	Point  []*big.Rat
}

// SolveLP maximizes c·x subject to A x <= b, x free. Exact rational
// arithmetic, two-phase simplex with Bland's rule (terminates always).
func SolveLP(A [][]*big.Rat, b []*big.Rat, c []*big.Rat) LPResult {
	n := len(c)
	if n == 0 {
		for _, bi := range b {
			if bi.Sign() < 0 {
				return LPResult{Status: LPInfeasible}
			}
		}
		return LPResult{Status: LPOptimal, Value: zeroRat(), Point: nil}
	}
	// Split free variables: x = xp - xm, xp,xm >= 0.
	m := len(A)
	nn := 2 * n
	Ap := make([][]*big.Rat, m)
	for i := 0; i < m; i++ {
		row := make([]*big.Rat, nn)
		for j := 0; j < n; j++ {
			row[j] = cloneRat(A[i][j])
			row[n+j] = new(big.Rat).Neg(A[i][j])
		}
		Ap[i] = row
	}
	cp := make([]*big.Rat, nn)
	for j := 0; j < n; j++ {
		cp[j] = cloneRat(c[j])
		cp[n+j] = new(big.Rat).Neg(c[j])
	}
	res := solveNonneg(Ap, b, cp)
	if res.Status != LPOptimal {
		return res
	}
	point := make([]*big.Rat, n)
	for j := 0; j < n; j++ {
		point[j] = new(big.Rat).Sub(res.Point[j], res.Point[n+j])
	}
	return LPResult{Status: LPOptimal, Value: res.Value, Point: point}
}

// dict is a simplex dictionary: x_B[i] = b[i] - sum_j a[i][j] x_j.
// Columns exist for every variable ever introduced (decision, slack,
// artificial); a[i][j] is only meaningful for nonbasic j, others are 0.
type dict struct {
	m        int
	w        int // total column count
	N        []int
	B        []int
	nonbasic []bool
	a        [][]*big.Rat
	b        []*big.Rat
	c        []*big.Rat
	v        *big.Rat
}

func newDict(A [][]*big.Rat, b []*big.Rat) *dict {
	m := len(A)
	n := len(A[0])
	w := n + m
	d := &dict{
		m:        m,
		w:        w,
		N:        make([]int, 0, w),
		B:        make([]int, m),
		nonbasic: make([]bool, w),
		a:        make([][]*big.Rat, m),
		b:        make([]*big.Rat, m),
		c:        make([]*big.Rat, w),
		v:        zeroRat(),
	}
	for j := 0; j < n; j++ {
		d.N = append(d.N, j)
		d.nonbasic[j] = true
	}
	for i := 0; i < m; i++ {
		d.B[i] = n + i
		row := make([]*big.Rat, w)
		for j := 0; j < w; j++ {
			if j < n {
				row[j] = cloneRat(A[i][j])
			} else {
				row[j] = zeroRat()
			}
		}
		d.a[i] = row
		d.b[i] = cloneRat(b[i])
	}
	for j := 0; j < w; j++ {
		d.c[j] = zeroRat()
	}
	return d
}

func (d *dict) pivot(l, e int) {
	w := d.w
	inv := new(big.Rat).Inv(d.a[l][e])
	leave := d.B[l]
	newRow := make([]*big.Rat, w)
	for j := 0; j < w; j++ {
		newRow[j] = new(big.Rat).Mul(d.a[l][j], inv)
	}
	newRow[e] = zeroRat()
	newRow[leave] = cloneRat(inv)
	newb := new(big.Rat).Mul(d.b[l], inv)
	ce := cloneRat(d.c[e])
	// objective
	d.v.Add(d.v, new(big.Rat).Mul(ce, newb))
	for j := 0; j < w; j++ {
		d.c[j].Sub(d.c[j], new(big.Rat).Mul(ce, newRow[j]))
	}
	d.c[e] = zeroRat()
	// remaining rows
	for i := 0; i < d.m; i++ {
		if i == l {
			continue
		}
		aie := d.a[i][e]
		if aie.Sign() == 0 {
			continue
		}
		factor := cloneRat(aie)
		d.b[i].Sub(d.b[i], new(big.Rat).Mul(factor, newb))
		for j := 0; j < w; j++ {
			d.a[i][j].Sub(d.a[i][j], new(big.Rat).Mul(factor, newRow[j]))
		}
		d.a[i][e] = zeroRat()
	}
	d.a[l] = newRow
	d.b[l] = newb
	for k := range d.N {
		if d.N[k] == e {
			d.N[k] = leave
			break
		}
	}
	d.nonbasic[e] = false
	d.nonbasic[leave] = true
	d.B[l] = e
}

// run executes the simplex loop with Bland's rule on the current objective.
func (d *dict) run() LPStatus {
	for {
		e := -1
		for j := 0; j < d.w; j++ {
			if d.nonbasic[j] && d.c[j].Sign() > 0 {
				e = j
				break
			}
		}
		if e < 0 {
			return LPOptimal
		}
		l := -1
		var minR *big.Rat
		for i := 0; i < d.m; i++ {
			if d.a[i][e].Sign() > 0 {
				r := new(big.Rat).Quo(d.b[i], d.a[i][e])
				if l < 0 || r.Cmp(minR) < 0 || (r.Cmp(minR) == 0 && d.B[i] < d.B[l]) {
					l = i
					minR = r
				}
			}
		}
		if l < 0 {
			return LPUnbounded
		}
		d.pivot(l, e)
	}
}

// initialize runs phase 1 with an artificial variable; returns false if infeasible.
func (d *dict) initialize() bool {
	l := 0
	for i := 1; i < d.m; i++ {
		if d.b[i].Cmp(d.b[l]) < 0 {
			l = i
		}
	}
	if d.b[l].Sign() >= 0 {
		return true
	}
	x0 := d.w
	d.w++
	for i := 0; i < d.m; i++ {
		d.a[i] = append(d.a[i], new(big.Rat).SetInt64(-1))
	}
	d.c = append(d.c, new(big.Rat).SetInt64(-1))
	d.nonbasic = append(d.nonbasic, true)
	d.N = append(d.N, x0)
	d.pivot(l, x0)
	if st := d.run(); st != LPOptimal {
		// phase-1 objective is bounded above by 0; unbounded cannot happen
		return false
	}
	if d.v.Sign() < 0 {
		return false
	}
	// drive x0 out of the basis if it sits there at value 0
	for i := 0; i < d.m; i++ {
		if d.B[i] == x0 {
			for j := 0; j < d.w; j++ {
				if j != x0 && d.nonbasic[j] && d.a[i][j].Sign() != 0 {
					d.pivot(i, j)
					break
				}
			}
		}
	}
	// exclude x0 forever
	d.nonbasic[x0] = false
	for k, j := range d.N {
		if j == x0 {
			d.N = append(d.N[:k], d.N[k+1:]...)
			break
		}
	}
	return true
}

// setObjective installs the original objective for phase 2, expressed over
// the current nonbasic variables. corig has entries for decision and slack
// columns (slack entries are 0); missing columns count as 0.
func (d *dict) setObjective(corig []*big.Rat) {
	getc := func(j int) *big.Rat {
		if j < len(corig) {
			return corig[j]
		}
		return zeroRat()
	}
	d.c = make([]*big.Rat, d.w)
	for j := 0; j < d.w; j++ {
		d.c[j] = zeroRat()
	}
	d.v = zeroRat()
	for i := 0; i < d.m; i++ {
		cb := getc(d.B[i])
		if cb.Sign() != 0 {
			d.v.Add(d.v, new(big.Rat).Mul(cb, d.b[i]))
		}
	}
	for j := 0; j < d.w; j++ {
		if !d.nonbasic[j] {
			continue
		}
		acc := cloneRat(getc(j))
		for i := 0; i < d.m; i++ {
			cb := getc(d.B[i])
			if cb.Sign() != 0 && d.a[i][j].Sign() != 0 {
				acc.Sub(acc, new(big.Rat).Mul(cb, d.a[i][j]))
			}
		}
		d.c[j] = acc
	}
}

func (d *dict) point() []*big.Rat {
	out := make([]*big.Rat, d.w)
	for j := 0; j < d.w; j++ {
		out[j] = zeroRat()
	}
	for i := 0; i < d.m; i++ {
		out[d.B[i]] = cloneRat(d.b[i])
	}
	return out
}

// solveNonneg maximizes c·x subject to A x <= b, x >= 0.
func solveNonneg(A [][]*big.Rat, b []*big.Rat, c []*big.Rat) LPResult {
	n := len(c)
	if len(A) == 0 {
		for j := 0; j < n; j++ {
			if c[j].Sign() > 0 {
				return LPResult{Status: LPUnbounded}
			}
		}
		return LPResult{Status: LPOptimal, Value: zeroRat(), Point: make([]*big.Rat, n)}
	}
	d := newDict(A, b)
	if !d.initialize() {
		return LPResult{Status: LPInfeasible}
	}
	d.setObjective(c)
	if st := d.run(); st != LPOptimal {
		return LPResult{Status: LPUnbounded}
	}
	pt := d.point()
	return LPResult{Status: LPOptimal, Value: cloneRat(d.v), Point: pt[:n]}
}

// toRows flattens a constraint system into LP rows [a | tcoef] <= rhs.
// Strict constraints get a margin variable coefficient of 1 when withMargin
// is set; equalities become two opposing rows.
func toRows(sys []*Constraint, numVars int, withMargin bool) (A [][]*big.Rat, b []*big.Rat) {
	width := numVars
	if withMargin {
		width++
	}
	addRow := func(c *Constraint, sign *big.Rat) {
		row := make([]*big.Rat, width)
		for j := 0; j < numVars; j++ {
			row[j] = new(big.Rat).Mul(c.Coeff[j], sign)
		}
		for j := numVars; j < width; j++ {
			row[j] = zeroRat()
		}
		if withMargin && c.Kind == Lt {
			row[numVars] = cloneRat(sign) // margin enters with the same sign
		}
		rhs := new(big.Rat).Mul(c.Const, sign)
		rhs.Neg(rhs)
		A = append(A, row)
		b = append(b, rhs)
	}
	one := big.NewRat(1, 1)
	negOne := big.NewRat(-1, 1)
	for _, c := range sys {
		switch c.Kind {
		case Eq:
			addRow(c, one)
			addRow(c, negOne)
		default:
			addRow(c, one)
		}
	}
	return A, b
}

// FeasibleMixed decides exact feasibility of a system mixing <=, < and =
// constraints, via the margin trick: maximize t with strict rows relaxed by
// t; the system is feasible iff the optimum is strictly positive.
func FeasibleMixed(sys []*Constraint, numVars int) bool {
	if len(sys) == 0 {
		return true
	}
	A, b := toRows(sys, numVars, true)
	// t <= 1 caps the objective so the LP is never unbounded
	capRow := make([]*big.Rat, numVars+1)
	for j := 0; j < numVars; j++ {
		capRow[j] = zeroRat()
	}
	capRow[numVars] = big.NewRat(1, 1)
	A = append(A, capRow)
	b = append(b, big.NewRat(1, 1))
	obj := make([]*big.Rat, numVars+1)
	for j := 0; j < numVars; j++ {
		obj[j] = zeroRat()
	}
	obj[numVars] = big.NewRat(1, 1)
	res := SolveLP(A, b, obj)
	if res.Status == LPUnbounded {
		return true
	}
	if res.Status == LPInfeasible {
		return false
	}
	return res.Value.Sign() > 0
}

// OptimizeClosure optimizes a linear objective over the closure of the
// system (strict inequalities treated as non-strict). Used to compare
// projection extremes and to sample boundary points.
func OptimizeClosure(sys []*Constraint, numVars int, obj []*big.Rat) LPResult {
	if numVars == 0 {
		for _, c := range sys {
			if c.isZeroRow() {
				ok := c.Const.Sign() <= 0 // closure: strict treated as <=
				if c.Kind == Eq {
					ok = c.Const.Sign() == 0
				}
				if !ok {
					return LPResult{Status: LPInfeasible}
				}
			}
		}
		return LPResult{Status: LPOptimal, Value: zeroRat(), Point: nil}
	}
	A, b := toRows(sys, numVars, false)
	return SolveLP(A, b, obj)
}
