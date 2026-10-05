package unitinfer

import (
	"fmt"
	"strings"
)

// buildSystem flattens equality constraints into A x = b over the integers.
// Unknown x has one entry per (unit variable, base dimension) pair:
// x[var*numDims+d] is the exponent of dimension d in unit variable var.
func buildSystem(cs []Constraint, numDims, nvars int) (a [][]int, b []int, n int) {
	n = nvars * numDims
	for _, c := range cs {
		for d := 0; d < numDims; d++ {
			row := make([]int, n)
			for v, k := range c.LHS.Coeffs {
				row[v*numDims+d] += k
			}
			for v, k := range c.RHS.Coeffs {
				row[v*numDims+d] -= k
			}
			a = append(a, row)
			b = append(b, constAt(c.RHS.Const, d)-constAt(c.LHS.Const, d))
		}
	}
	return a, b, n
}

// Solution is the most general solution of the constraint system:
//
//	x = Particular + sum_k lambda_k * Basis[k],   lambda_k in Z
//
// x is indexed by var*NumDims+d. Free directions are reported explicitly via
// Basis; underdetermined unknowns are never silently pinned to dimensionless.
type Solution struct {
	NumDims    int
	DimNames   []string
	VarNames   []string
	Particular []int
	Basis      [][]int
}

// Underdetermined reports whether free (unconstrained) directions remain.
func (s *Solution) Underdetermined() bool { return len(s.Basis) > 0 }

// Instance evaluates the general solution at integer parameters lambda.
func (s *Solution) Instance(lambda []int) ([]int, error) {
	if len(lambda) != len(s.Basis) {
		return nil, fmt.Errorf("unitinfer: need %d parameters, got %d", len(s.Basis), len(lambda))
	}
	x := make([]int, len(s.Particular))
	copy(x, s.Particular)
	for k, lk := range lambda {
		for i := range x {
			x[i] += lk * s.Basis[k][i]
		}
	}
	return x, nil
}

// UnitOf returns the exponent vector of unit variable v at parameters lambda.
func (s *Solution) UnitOf(v int, lambda []int) ([]int, error) {
	x, err := s.Instance(lambda)
	if err != nil {
		return nil, err
	}
	u := make([]int, s.NumDims)
	copy(u, x[v*s.NumDims:(v+1)*s.NumDims])
	return u, nil
}

// Contains reports whether the flat assignment x is an instance of the
// general solution, i.e. x - Particular lies in the Z-span of Basis.
func (s *Solution) Contains(x []int) bool {
	if len(x) != len(s.Particular) {
		return false
	}
	diff := make([]int, len(x))
	for i := range x {
		diff[i] = x[i] - s.Particular[i]
	}
	k := len(s.Basis)
	if k == 0 {
		for _, v := range diff {
			if v != 0 {
				return false
			}
		}
		return true
	}
	// Solve B lambda = diff where columns of B are the basis vectors.
	m := make([][]int, len(x))
	for i := range m {
		m[i] = make([]int, k)
		for j := 0; j < k; j++ {
			m[i][j] = s.Basis[j][i]
		}
	}
	_, _, ok := solveSystem(m, diff, k)
	return ok
}

// String renders the general solution, one line per unit variable.
func (s *Solution) String() string {
	lines := make([]string, 0, len(s.VarNames))
	for v, name := range s.VarNames {
		base := s.Particular[v*s.NumDims : (v+1)*s.NumDims]
		var sb strings.Builder
		sb.WriteString(name)
		sb.WriteString(" = ")
		sb.WriteString(FormatUnit(base, s.DimNames))
		for k, bv := range s.Basis {
			comp := bv[v*s.NumDims : (v+1)*s.NumDims]
			zero := true
			for _, e := range comp {
				if e != 0 {
					zero = false
					break
				}
			}
			if !zero {
				fmt.Fprintf(&sb, " + l%d*(%s)", k, FormatUnit(comp, s.DimNames))
			}
		}
		lines = append(lines, sb.String())
	}
	return strings.Join(lines, "\n")
}

// Result is the outcome of Solve: either the most general solution, or a
// (subset-)minimal set of conflicting constraints.
type Result struct {
	OK       bool
	Solution *Solution
	Conflict []Constraint
}

// Solve solves the accumulated constraints. On inconsistency it returns a
// minimal conflict set: removing any of its constraints makes it consistent.
func (in *Infer) Solve() *Result {
	a, b, n := buildSystem(in.constraints, in.NumDims, len(in.varNames))
	part, basis, ok := solveSystem(a, b, n)
	if !ok {
		return &Result{
			OK:       false,
			Conflict: MinimalConflict(in.constraints, in.NumDims, len(in.varNames)),
		}
	}
	return &Result{
		OK: true,
		Solution: &Solution{
			NumDims:    in.NumDims,
			DimNames:   in.DimNames,
			VarNames:   append([]string(nil), in.varNames...),
			Particular: part,
			Basis:      basis,
		},
	}
}
