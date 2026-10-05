package main

import (
	"fmt"
	"math/big"
)

// Problem is an integer signal recovery instance: find x in Z^n with
// A*x = b and lo[i] <= x[i] <= hi[i].
type Problem struct {
	m, n int
	A    [][]*big.Int // m x n integer observation matrix
	b    []*big.Int   // m observations
	lo   []*big.Int   // n lower bounds (inclusive)
	hi   []*big.Int   // n upper bounds (inclusive)
}

// Conflict explains why an instance is infeasible.
type Conflict struct {
	Observations []int      // indices of the observations involved
	Coefficients []*big.Int // optional integer certificate over all m observations
	Explanation  string
}

// Result is the outcome of Solve.
type Result struct {
	Status   string // "unique", "multiple" or "infeasible"
	Signals  [][]*big.Int
	Conflict *Conflict
}

// Solve determines whether the feasible set of p is empty, a singleton, or
// contains at least two signals. It never infers multiplicity from rank
// deficiency alone: the integer feasible set is searched exactly.
func Solve(p *Problem) *Result {
	for i := 0; i < p.n; i++ {
		if p.lo[i].Cmp(p.hi[i]) > 0 {
			return &Result{Status: "infeasible", Conflict: &Conflict{
				Observations: []int{},
				Explanation:  fmt.Sprintf("component %d has an empty range: lower bound %s exceeds upper bound %s", i, p.lo[i], p.hi[i]),
			}}
		}
	}
	sols, conflict := integerSolutions(p, 2)
	if conflict != nil {
		return &Result{Status: "infeasible", Conflict: conflict}
	}
	switch len(sols) {
	case 0:
		core := minimalCore(p)
		return &Result{Status: "infeasible", Conflict: &Conflict{
			Observations: core,
			Explanation:  "no integer signal within the given bounds satisfies this subset of observations (minimal conflicting subset)",
		}}
	case 1:
		return &Result{Status: "unique", Signals: sols}
	default:
		return &Result{Status: "multiple", Signals: sols}
	}
}

// integerSolutions returns up to `limit` feasible integer signals. If the
// observation system A*x = b is inconsistent over Z (independent of the
// bounds), it instead returns a Conflict with an exact certificate.
func integerSolutions(p *Problem, limit int) ([][]*big.Int, *Conflict) {
	U, S, V := smithNormalForm(p.A, p.m, p.n)
	c := matVec(U, p.b)

	mn := p.m
	if p.n < mn {
		mn = p.n
	}
	r := 0
	for r < mn && S[r][r].Sign() != 0 {
		r++
	}

	// Consistency of S*z = c, where z = V^{-1}*x ranges over all of Z^n
	// because V is unimodular.
	for i := 0; i < p.m; i++ {
		s := new(big.Int)
		if i < mn {
			s = S[i][i]
		}
		if s.Sign() == 0 {
			if c[i].Sign() != 0 {
				return nil, &Conflict{
					Observations: nzIndices(U[i]),
					Coefficients: U[i],
					Explanation:  fmt.Sprintf("an integer combination of the observations forces 0 = %s", c[i]),
				}
			}
		} else if new(big.Int).Rem(c[i], s).Sign() != 0 {
			return nil, &Conflict{
				Observations: nzIndices(U[i]),
				Coefficients: U[i],
				Explanation:  fmt.Sprintf("an integer combination of the observations requires a multiple of %s to equal %s", s, c[i]),
			}
		}
	}

	// Particular solution: z_i = c_i / s_i for i < r, z_i = 0 otherwise.
	z := make([]*big.Int, p.n)
	for i := 0; i < p.n; i++ {
		z[i] = new(big.Int)
		if i < r {
			z[i].Quo(c[i], S[i][i])
		}
	}
	x0 := matVec(V, z)

	k := p.n - r // number of free integer variables
	if k == 0 {
		if inBox(x0, p.lo, p.hi) {
			return [][]*big.Int{x0}, nil
		}
		return nil, nil
	}

	// x = x0 + W*t with W = columns r..n-1 of V, t in Z^k free.
	W := make([][]*big.Int, p.n)
	for i := 0; i < p.n; i++ {
		W[i] = make([]*big.Int, k)
		for j := 0; j < k; j++ {
			W[i][j] = V[i][r+j]
		}
	}
	Vinv := inverseUnimodular(V)
	return enumerateBox(x0, W, Vinv, r, p.lo, p.hi, limit), nil
}

func nzIndices(v []*big.Int) []int {
	out := []int{}
	for i, x := range v {
		if x.Sign() != 0 {
			out = append(out, i)
		}
	}
	return out
}

func inBox(x, lo, hi []*big.Int) bool {
	for i := range x {
		if x[i].Cmp(lo[i]) < 0 || x[i].Cmp(hi[i]) > 0 {
			return false
		}
	}
	return true
}

// boundConstraint is L <= sum_j w_j * t_j <= H.
type boundConstraint struct {
	lo, hi *big.Int
	js     []int
	ws     []*big.Int
}

// enumerateBox finds up to `limit` integer vectors t in Z^k such that
// lo <= x0 + W*t <= hi, returning the corresponding signals x0 + W*t.
// Vinv is the inverse of the unimodular basis matrix and r the rank; rows
// r..n-1 of Vinv express each free variable t_j as an integer linear form
// of x, which yields exact finite initial domains.
func enumerateBox(x0 []*big.Int, W, Vinv [][]*big.Int, r int, lo, hi []*big.Int, limit int) [][]*big.Int {
	n := len(x0)
	k := n - r

	dLo := make([]*big.Int, k)
	dHi := make([]*big.Int, k)
	for j := 0; j < k; j++ {
		dLo[j], dHi[j] = linearRangeOverBox(Vinv[r+j], lo, hi)
	}

	cons := make([]boundConstraint, n)
	for i := 0; i < n; i++ {
		c := boundConstraint{
			lo: new(big.Int).Sub(lo[i], x0[i]),
			hi: new(big.Int).Sub(hi[i], x0[i]),
		}
		for j := 0; j < k; j++ {
			if W[i][j].Sign() != 0 {
				c.js = append(c.js, j)
				c.ws = append(c.ws, W[i][j])
			}
		}
		cons[i] = c
	}

	var sols [][]*big.Int
	var rec func(dl, dh []*big.Int) bool // returns true when the limit is reached
	rec = func(dl, dh []*big.Int) bool {
		dl = cloneVec(dl)
		dh = cloneVec(dh)
		if !propagate(cons, dl, dh) {
			return false
		}
		split := -1
		var splitSize *big.Int
		for j := 0; j < k; j++ {
			if dl[j].Cmp(dh[j]) < 0 {
				sz := new(big.Int).Sub(dh[j], dl[j])
				if split == -1 || sz.Cmp(splitSize) < 0 {
					split, splitSize = j, sz
				}
			}
		}
		if split == -1 {
			x := make([]*big.Int, n)
			for i := 0; i < n; i++ {
				x[i] = new(big.Int).Set(x0[i])
				for j := 0; j < k; j++ {
					x[i].Add(x[i], new(big.Int).Mul(W[i][j], dl[j]))
				}
			}
			sols = append(sols, x)
			return len(sols) >= limit
		}
		mid := new(big.Int).Add(dl[split], dh[split])
		mid.Rsh(mid, 1) // floor((dl+dh)/2); dl < dh implies dl <= mid < dh
		dhL := cloneVec(dh)
		dhL[split] = mid
		if rec(dl, dhL) {
			return true
		}
		dlR := cloneVec(dl)
		dlR[split] = new(big.Int).Add(mid, big.NewInt(1))
		return rec(dlR, dh)
	}
	rec(dLo, dHi)
	return sols
}

// linearRangeOverBox returns the exact min and max of sum_i c_i*x_i over
// lo <= x <= hi.
func linearRangeOverBox(c, lo, hi []*big.Int) (mn, mx *big.Int) {
	mn, mx = new(big.Int), new(big.Int)
	for i, ci := range c {
		switch ci.Sign() {
		case 1:
			mn.Add(mn, new(big.Int).Mul(ci, lo[i]))
			mx.Add(mx, new(big.Int).Mul(ci, hi[i]))
		case -1:
			mn.Add(mn, new(big.Int).Mul(ci, hi[i]))
			mx.Add(mx, new(big.Int).Mul(ci, lo[i]))
		}
	}
	return mn, mx
}

// propagate tightens the variable domains [dl, dh] using interval
// arithmetic on the bound constraints, iterating to a fixpoint. It returns
// false if some domain becomes empty. Tightening never removes a feasible
// integer point.
func propagate(cons []boundConstraint, dl, dh []*big.Int) bool {
	changed := true
	for changed {
		changed = false
		for ci := range cons {
			c := &cons[ci]
			totalMin, totalMax := new(big.Int), new(big.Int)
			for idx, j := range c.js {
				w := c.ws[idx]
				if w.Sign() > 0 {
					totalMin.Add(totalMin, new(big.Int).Mul(w, dl[j]))
					totalMax.Add(totalMax, new(big.Int).Mul(w, dh[j]))
				} else {
					totalMin.Add(totalMin, new(big.Int).Mul(w, dh[j]))
					totalMax.Add(totalMax, new(big.Int).Mul(w, dl[j]))
				}
			}
			if totalMin.Cmp(c.hi) > 0 || totalMax.Cmp(c.lo) < 0 {
				return false
			}
			for idx, j := range c.js {
				w := c.ws[idx]
				var cMin, cMax *big.Int
				if w.Sign() > 0 {
					cMin = new(big.Int).Mul(w, dl[j])
					cMax = new(big.Int).Mul(w, dh[j])
				} else {
					cMin = new(big.Int).Mul(w, dh[j])
					cMax = new(big.Int).Mul(w, dl[j])
				}
				restMin := new(big.Int).Sub(totalMin, cMin)
				restMax := new(big.Int).Sub(totalMax, cMax)
				var newLo, newHi *big.Int
				if w.Sign() > 0 {
					newHi = floorDiv(new(big.Int).Sub(c.hi, restMin), w)
					newLo = ceilDiv(new(big.Int).Sub(c.lo, restMax), w)
				} else {
					newHi = floorDiv(new(big.Int).Sub(c.lo, restMax), w)
					newLo = ceilDiv(new(big.Int).Sub(c.hi, restMin), w)
				}
				if newLo.Cmp(dl[j]) > 0 {
					dl[j].Set(newLo)
					changed = true
				}
				if newHi.Cmp(dh[j]) < 0 {
					dh[j].Set(newHi)
					changed = true
				}
				if dl[j].Cmp(dh[j]) > 0 {
					return false
				}
			}
		}
	}
	return true
}

// minimalCore finds a subset-minimal set of observation indices that is
// still infeasible together with the bounds (deletion filter).
func minimalCore(p *Problem) []int {
	core := make([]int, p.m)
	for i := range core {
		core[i] = i
	}
	for i := 0; i < p.m; i++ {
		present := false
		for _, j := range core {
			if j == i {
				present = true
				break
			}
		}
		if !present {
			continue
		}
		trial := make([]int, 0, len(core)-1)
		for _, j := range core {
			if j != i {
				trial = append(trial, j)
			}
		}
		sub := subProblem(p, trial)
		sols, conflict := integerSolutions(sub, 1)
		if conflict != nil || len(sols) == 0 {
			core = trial
		}
	}
	return core
}

func subProblem(p *Problem, rows []int) *Problem {
	sub := &Problem{m: len(rows), n: p.n, lo: p.lo, hi: p.hi}
	sub.A = make([][]*big.Int, len(rows))
	sub.b = make([]*big.Int, len(rows))
	for i, r := range rows {
		sub.A[i] = p.A[r]
		sub.b[i] = p.b[r]
	}
	return sub
}
