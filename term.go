package unitinfer

import (
	"fmt"
	"strings"
)

// Term is a unit expression, affine linear in the unit variables:
//
//	Const + sum_{v} Coeffs[v] * X_v
//
// where Const is an integer exponent vector over the base dimensions and
// each X_v is the (unknown) integer exponent vector of unit variable v.
// Multiplication/division of units adds/subtracts Terms; an integer power
// scales a Term. Coefficients stay integral, so unification reduces to a
// system of linear Diophantine equations.
type Term struct {
	Const  []int
	Coeffs map[int]int
}

// VarTerm returns the Term denoting unit variable v.
func VarTerm(v int) Term {
	return Term{Const: nil, Coeffs: map[int]int{v: 1}}
}

// ConstTerm returns the Term for a concrete unit exponent vector.
func ConstTerm(unit []int) Term {
	c := make([]int, len(unit))
	copy(c, unit)
	return Term{Const: c}
}

// Dimensionless returns the Term for the dimensionless unit.
func Dimensionless() Term { return Term{} }

func constAt(c []int, d int) int {
	if d < len(c) {
		return c[d]
	}
	return 0
}

func constDims(a, b []int) int {
	if len(a) > len(b) {
		return len(a)
	}
	return len(b)
}

// Add returns t + o (unit multiplication).
func (t Term) Add(o Term) Term {
	n := constDims(t.Const, o.Const)
	c := make([]int, n)
	for d := 0; d < n; d++ {
		c[d] = constAt(t.Const, d) + constAt(o.Const, d)
	}
	co := map[int]int{}
	for v, k := range t.Coeffs {
		co[v] += k
	}
	for v, k := range o.Coeffs {
		co[v] += k
	}
	return Term{Const: c, Coeffs: co}
}

// Sub returns t - o (unit division).
func (t Term) Sub(o Term) Term {
	return t.Add(o.Scale(-1))
}

// Scale returns k*t (integer power).
func (t Term) Scale(k int) Term {
	c := make([]int, len(t.Const))
	for d := range c {
		c[d] = k * t.Const[d]
	}
	co := map[int]int{}
	for v, c0 := range t.Coeffs {
		co[v] = k * c0
	}
	return Term{Const: c, Coeffs: co}
}

// FormatUnit renders an exponent vector like "L*T^-2"; "1" if dimensionless.
func FormatUnit(vec []int, dimNames []string) string {
	parts := []string{}
	for d, e := range vec {
		if e == 0 {
			continue
		}
		name := fmt.Sprintf("d%d", d)
		if d < len(dimNames) && dimNames[d] != "" {
			name = dimNames[d]
		}
		if e == 1 {
			parts = append(parts, name)
		} else {
			parts = append(parts, fmt.Sprintf("%s^%d", name, e))
		}
	}
	if len(parts) == 0 {
		return "1"
	}
	return strings.Join(parts, "*")
}
