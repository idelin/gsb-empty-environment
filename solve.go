package unitinfer

import (
	"fmt"
	"math/big"
	"strings"
)

// Solution is the most general solution of a consistent constraint system.
//
// Unknowns are the scalar exponent components of every unit variable:
// unknown index = varPos*Dims + dim. Every solution is of the form
//
//	x = Part + sum_j t_j * Basis[j]      (t_j arbitrary rationals)
//
// where Basis vectors are integral. Unit variables that are underdetermined
// keep free parameters; they are never silently pinned to dimensionless.
type Solution struct {
	Dims  int
	Vars  []int        // unit variable ids; varPos indexes this slice
	Part  []*big.Rat   // particular solution, length len(Vars)*Dims
	Basis [][]*big.Rat // null-space basis vectors, same length
}

// NumParams returns the number of free parameters of the general solution.
func (s *Solution) NumParams() int { return len(s.Basis) }

func (s *Solution) varPos(v int) int {
	for i, x := range s.Vars {
		if x == v {
			return i
		}
	}
	return -1
}

// AffineVector is a vector of affine expressions in the solution parameters.
type AffineVector struct {
	Const  []*big.Rat   // constant part, length Dims
	Params [][]*big.Rat // Params[j][d] = coefficient of parameter j on dim d
}

// UnitOf returns the unit of variable v as an affine vector in the free
// parameters. ok is false if v is not part of the system.
func (s *Solution) UnitOf(v int) (AffineVector, bool) {
	p := s.varPos(v)
	if p < 0 {
		return AffineVector{}, false
	}
	av := AffineVector{
		Const:  make([]*big.Rat, s.Dims),
		Params: make([][]*big.Rat, len(s.Basis)),
	}
	for d := 0; d < s.Dims; d++ {
		av.Const[d] = new(big.Rat).Set(s.Part[p*s.Dims+d])
	}
	for j := range s.Basis {
		av.Params[j] = make([]*big.Rat, s.Dims)
		for d := 0; d < s.Dims; d++ {
			av.Params[j][d] = new(big.Rat).Set(s.Basis[j][p*s.Dims+d])
		}
	}
	return av, true
}

// IsFree reports whether the unit of v still depends on a free parameter,
// i.e. the system leaves it underdetermined.
func (s *Solution) IsFree(v int) bool {
	av, ok := s.UnitOf(v)
	if !ok {
		return false
	}
	for _, pv := range av.Params {
		for _, c := range pv {
			if c.Sign() != 0 {
				return true
			}
		}
	}
	return false
}

// UnitString renders the solved unit of variable v.
func (s *Solution) UnitString(v int, dimNames []string) string {
	av, ok := s.UnitOf(v)
	if !ok {
		return "<unknown var>"
	}
	var parts []string
	for d := 0; d < s.Dims; d++ {
		var terms []string
		if av.Const[d].Sign() != 0 {
			terms = append(terms, av.Const[d].RatString())
		}
		for j, pv := range av.Params {
			if pv[d].Sign() != 0 {
				c := pv[d]
				coef := c.RatString()
				if c.Cmp(big.NewRat(1, 1)) == 0 {
					coef = ""
				} else if c.Cmp(big.NewRat(-1, 1)) == 0 {
					coef = "-"
				}
				terms = append(terms, fmt.Sprintf("%sp%d", coef, j))
			}
		}
		if len(terms) == 0 {
			continue
		}
		name := fmt.Sprintf("d%d", d)
		if d < len(dimNames) && dimNames[d] != "" {
			name = dimNames[d]
		}
		parts = append(parts, fmt.Sprintf("%s^(%s)", name, strings.Join(terms, "+")))
	}
	if len(parts) == 0 {
		return "1"
	}
	return strings.Join(parts, " * ")
}

// Solve finds the most general solution of the constraint system.
// ok is false when the system is inconsistent.
func Solve(dims int, cons []Constraint, vars []int) (*Solution, bool) {
	rows, rhs := buildSystem(dims, cons, vars)
	part, basis, ok := rrefSolve(len(vars)*dims, rows, rhs)
	if !ok {
		return nil, false
	}
	vs := make([]int, len(vars))
	copy(vs, vars)
	return &Solution{Dims: dims, Vars: vs, Part: part, Basis: basis}, true
}

// Consistent reports whether the constraint system has any solution.
func Consistent(dims int, cons []Constraint, vars []int) bool {
	rows, rhs := buildSystem(dims, cons, vars)
	_, _, ok := rrefSolve(len(vars)*dims, rows, rhs)
	return ok
}

// collectVars returns the sorted union of unit variables occurring in cons.
func collectVars(cons []Constraint) []int {
	set := map[int]bool{}
	for _, c := range cons {
		for v := range c.Term.Coeffs {
			set[v] = true
		}
	}
	vs := make([]int, 0, len(set))
	for v := range set {
		vs = append(vs, v)
	}
	for i := 0; i < len(vs); i++ { // insertion sort (small n)
		for j := i; j > 0 && vs[j] < vs[j-1]; j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
	return vs
}

// buildSystem flattens constraints into scalar rows A x = b where the
// unknown vector x holds the Dims components of every unit variable.
func buildSystem(dims int, cons []Constraint, vars []int) ([][]*big.Rat, []*big.Rat) {
	pos := map[int]int{}
	for i, v := range vars {
		pos[v] = i
	}
	n := len(vars) * dims
	var rows [][]*big.Rat
	var rhs []*big.Rat
	for _, c := range cons {
		for d := 0; d < dims; d++ {
			row := make([]*big.Rat, n)
			for i := range row {
				row[i] = new(big.Rat)
			}
			for v, coef := range c.Term.Coeffs {
				p, ok := pos[v]
				if !ok {
					continue
				}
				row[p*dims+d] = big.NewRat(int64(coef), 1)
			}
			rows = append(rows, row)
			rhs = append(rhs, big.NewRat(int64(-c.Term.Base[d]), 1))
		}
	}
	return rows, rhs
}

// rrefSolve solves A x = b exactly over Q via Gauss-Jordan elimination and
// returns a particular solution and an integral null-space basis.
func rrefSolve(n int, rows [][]*big.Rat, rhs []*big.Rat) (part []*big.Rat, basis [][]*big.Rat, ok bool) {
	m := len(rows)
	a := make([][]*big.Rat, m)
	for i := range a {
		a[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			a[i][j] = new(big.Rat).Set(rows[i][j])
		}
		a[i][n] = new(big.Rat).Set(rhs[i])
	}
	var pivots []int
	row := 0
	for col := 0; col < n && row < m; col++ {
		p := -1
		for r := row; r < m; r++ {
			if a[r][col].Sign() != 0 {
				p = r
				break
			}
		}
		if p < 0 {
			continue
		}
		a[row], a[p] = a[p], a[row]
		inv := new(big.Rat).Inv(a[row][col])
		for c := col; c <= n; c++ {
			a[row][c].Mul(a[row][c], inv)
		}
		for r := 0; r < m; r++ {
			if r == row || a[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(a[r][col])
			for c := col; c <= n; c++ {
				a[r][c].Sub(a[r][c], new(big.Rat).Mul(f, a[row][c]))
			}
		}
		pivots = append(pivots, col)
		row++
	}
	for r := row; r < m; r++ {
		if a[r][n].Sign() != 0 {
			return nil, nil, false
		}
	}
	part = make([]*big.Rat, n)
	for i := range part {
		part[i] = new(big.Rat)
	}
	for i, col := range pivots {
		part[col] = new(big.Rat).Set(a[i][n])
	}
	isPivot := make([]bool, n)
	for _, col := range pivots {
		isPivot[col] = true
	}
	for f := 0; f < n; f++ {
		if isPivot[f] {
			continue
		}
		vec := make([]*big.Rat, n)
		for i := range vec {
			vec[i] = new(big.Rat)
		}
		vec[f].SetInt64(1)
		for i, col := range pivots {
			vec[col] = new(big.Rat).Neg(a[i][f])
		}
		basis = append(basis, vec)
	}
	return part, basis, true
}
