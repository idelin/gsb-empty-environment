package main

import (
	"fmt"
	"sort"
	"strings"
)

// Cube is a conjunction of literals over n variables.
// mask bit i set means variable i is fixed; vals bit i then gives its value.
type Cube struct {
	mask uint64
	vals uint64
}

// Covers reports whether the cube covers the given assignment.
func (c Cube) Covers(assignment uint64) bool {
	return assignment&c.mask == c.vals
}

// Literals renders the cube as "var=1" terms in variable order.
func (c Cube) Literals(vars []string) []string {
	var lits []string
	for i, v := range vars {
		if c.mask&(1<<uint(i)) != 0 {
			if c.vals&(1<<uint(i)) != 0 {
				lits = append(lits, v+"=1")
			} else {
				lits = append(lits, v+"=0")
			}
		}
	}
	return lits
}

func (c Cube) String(vars []string) string {
	lits := c.Literals(vars)
	if len(lits) == 0 {
		return "(true)"
	}
	return strings.Join(lits, " ")
}

// TruthTable evaluates e over all 2^n assignments of vars (n <= 10).
func TruthTable(e Expr, vars []string) []bool {
	n := uint(len(vars))
	rows := 1 << n
	tt := make([]bool, rows)
	for a := 0; a < rows; a++ {
		tt[a] = e.Eval(uint64(a), vars)
	}
	return tt
}

// DiffTable returns the truth table of points where ea and eb differ.
func DiffTable(ea, eb Expr, vars []string) []bool {
	ta, tb := TruthTable(ea, vars), TruthTable(eb, vars)
	d := make([]bool, len(ta))
	for i := range ta {
		d[i] = ta[i] != tb[i]
	}
	return d
}

// cubeSubsetOfOnset reports whether every assignment covered by c is a
// difference point (diff[a] == true).
func cubeSubsetOfOnset(c Cube, diff []bool) bool {
	for a := 0; a < len(diff); a++ {
		if c.Covers(uint64(a)) && !diff[a] {
			return false
		}
	}
	return true
}

// PrimeImplicants enumerates all minimal cubes whose covered assignments are
// all difference points. A cube is minimal iff dropping any single literal
// makes it cover some non-difference point. With n variables there are 3^n
// cubes; n <= 10 keeps enumeration cheap (<= 59049 cubes x 1024 rows).
func PrimeImplicants(diff []bool, n uint) []Cube {
	var primes []Cube
	total := 1
	for i := uint(0); i < n; i++ {
		total *= 3
	}
	for t := 0; t < total; t++ {
		var c Cube
		x := t
		for i := uint(0); i < n; i++ {
			switch x % 3 {
			case 1:
				c.mask |= 1 << i
			case 2:
				c.mask |= 1 << i
				c.vals |= 1 << i
			}
			x /= 3
		}
		if !cubeSubsetOfOnset(c, diff) {
			continue
		}
		minimal := true
		for i := uint(0); i < n && minimal; i++ {
			bit := uint64(1) << i
			if c.mask&bit == 0 {
				continue
			}
			dropped := Cube{mask: c.mask &^ bit, vals: c.vals &^ bit}
			if cubeSubsetOfOnset(dropped, diff) {
				minimal = false
			}
		}
		if minimal {
			primes = append(primes, c)
		}
	}
	sort.Slice(primes, func(i, j int) bool {
		if primes[i].mask != primes[j].mask {
			return primes[i].mask < primes[j].mask
		}
		return primes[i].vals < primes[j].vals
	})
	return primes
}

// VerifyCubes checks against the truth table that:
//  1. soundness: every assignment covered by any cube is a difference point;
//  2. minimality: no literal can be dropped from any cube without covering a
//     non-difference point;
//  3. completeness: every difference point is covered by at least one cube.
func VerifyCubes(cubes []Cube, diff []bool, n uint) error {
	for ci, c := range cubes {
		for a := 0; a < len(diff); a++ {
			if c.Covers(uint64(a)) && !diff[a] {
				return fmt.Errorf("cube %d covers non-difference assignment %d", ci, a)
			}
		}
		for i := uint(0); i < n; i++ {
			bit := uint64(1) << i
			if c.mask&bit == 0 {
				continue
			}
			dropped := Cube{mask: c.mask &^ bit, vals: c.vals &^ bit}
			if cubeSubsetOfOnset(dropped, diff) {
				return fmt.Errorf("cube %d is not minimal: literal %d can be dropped", ci, i)
			}
		}
	}
	for a := 0; a < len(diff); a++ {
		if !diff[a] {
			continue
		}
		covered := false
		for _, c := range cubes {
			if c.Covers(uint64(a)) {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("difference assignment %d is not covered by any cube", a)
		}
	}
	return nil
}
