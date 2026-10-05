package unitinfer

import (
	"fmt"
	"math/big"
	"strings"
)

// EnumReport summarizes a small-domain enumeration of the solution set.
type EnumReport struct {
	Vars      []int   // unit variables (columns of each solution)
	Dims      int     // base quantities per variable
	Solutions [][]int // every satisfying assignment, flattened var-major
	Collapsed []int   // variables taking a single value over the domain
	FreeVars  []int   // variables taking more than one value
}

// Enumerate brute-forces all assignments of the unit variables with every
// exponent component in [lo,hi] and keeps those satisfying every constraint.
func Enumerate(dims int, cons []Constraint, vars []int, lo, hi int) *EnumReport {
	n := len(vars) * dims
	pos := map[int]int{}
	for i, v := range vars {
		pos[v] = i
	}
	rep := &EnumReport{Vars: append([]int(nil), vars...), Dims: dims}
	cur := make([]int, n)
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			if satisfies(dims, cons, pos, cur) {
				sol := make([]int, n)
				copy(sol, cur)
				rep.Solutions = append(rep.Solutions, sol)
			}
			return
		}
		for x := lo; x <= hi; x++ {
			cur[k] = x
			rec(k + 1)
		}
	}
	rec(0)
	// Project per variable to find collapsed vs. free variables.
	for i, v := range vars {
		seen := map[string]bool{}
		for _, sol := range rep.Solutions {
			var b strings.Builder
			for d := 0; d < dims; d++ {
				fmt.Fprintf(&b, "%d,", sol[i*dims+d])
			}
			seen[b.String()] = true
		}
		if len(seen) <= 1 {
			rep.Collapsed = append(rep.Collapsed, v)
		} else {
			rep.FreeVars = append(rep.FreeVars, v)
		}
	}
	return rep
}

func satisfies(dims int, cons []Constraint, pos map[int]int, x []int) bool {
	for _, c := range cons {
		for d := 0; d < dims; d++ {
			sum := c.Term.Base[d]
			for v, coef := range c.Term.Coeffs {
				p, ok := pos[v]
				if !ok {
					continue
				}
				sum += coef * x[p*dims+d]
			}
			if sum != 0 {
				return false
			}
		}
	}
	return true
}

// CheckInclusion verifies, by exhaustive enumeration over [lo,hi], that the
// parametric general solution describes exactly the enumerated solution set:
//
//  1. every enumerated integer solution is reachable from the parametric
//     solution (E ⊆ P), and
//  2. every integral in-domain point produced by sweeping the free
//     parameters satisfies all constraints (P ∩ Z ⊆ E).
//
// It also returns the enumeration report so callers can assert that
// underdetermined variables were not arbitrarily pinned (e.g. to
// dimensionless).
func CheckInclusion(sol *Solution, cons []Constraint, lo, hi int) (*EnumReport, error) {
	rep := Enumerate(sol.Dims, cons, sol.Vars, lo, hi)
	n := len(sol.Vars) * sol.Dims
	// E ⊆ P: each enumerated point must be an instance of the general
	// solution, i.e. x - Part must lie in the span of the basis.
	for _, e := range rep.Solutions {
		rhs := make([]*big.Rat, n)
		rows := make([][]*big.Rat, n)
		for i := 0; i < n; i++ {
			rows[i] = make([]*big.Rat, len(sol.Basis))
			for j := range sol.Basis {
				rows[i][j] = new(big.Rat).Set(sol.Basis[j][i])
			}
			rhs[i] = new(big.Rat).Sub(big.NewRat(int64(e[i]), 1), sol.Part[i])
		}
		if _, _, ok := rrefSolve(len(sol.Basis), rows, rhs); !ok {
			return rep, fmt.Errorf("enumerated solution %v not covered by general solution", e)
		}
	}
	// P ∩ Z ⊆ E: sweep parameters and test every integral in-domain point.
	inDomain := map[string]bool{}
	for _, e := range rep.Solutions {
		inDomain[encodeInts(e)] = true
	}
	k := len(sol.Basis)
	ts := make([]int, k)
	var sweep func(j int) error
	sweep = func(j int) error {
		if j == k {
			x := make([]int, n)
			integral := true
			for i := 0; i < n; i++ {
				v := new(big.Rat).Set(sol.Part[i])
				for p := 0; p < k; p++ {
					v.Add(v, new(big.Rat).Mul(big.NewRat(int64(ts[p]), 1), sol.Basis[p][i]))
				}
				if v.Denom().Cmp(big.NewInt(1)) != 0 {
					integral = false
					break
				}
				iv := int(v.Num().Int64())
				if iv < lo || iv > hi {
					integral = false
					break
				}
				x[i] = iv
			}
			if integral && !inDomain[encodeInts(x)] {
				return fmt.Errorf("general solution yields %v which violates the constraints", x)
			}
			return nil
		}
		for t := lo; t <= hi; t++ {
			ts[j] = t
			if err := sweep(j + 1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := sweep(0); err != nil {
		return rep, err
	}
	return rep, nil
}

func encodeInts(xs []int) string {
	var b strings.Builder
	for _, x := range xs {
		fmt.Fprintf(&b, "%d,", x)
	}
	return b.String()
}
