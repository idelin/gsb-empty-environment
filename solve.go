package linsolve

import (
	"errors"
	"fmt"
	"math/big"
)

// Status classifies the solution set of a linear system.
type Status int

const (
	// Inconsistent means the system has no solution.
	Inconsistent Status = iota
	// Unique means the system has exactly one solution.
	Unique
	// Parametric means the system has infinitely many solutions.
	Parametric
)

func (s Status) String() string {
	switch s {
	case Inconsistent:
		return "Inconsistent"
	case Unique:
		return "Unique"
	case Parametric:
		return "Parametric"
	}
	return "Unknown"
}

// Solution describes the exact solution set of A x = b.
//
// The populated fields depend on Status:
//
//	Inconsistent: Certificate holds coefficients c (one per equation)
//	              such that c·A = 0 but c·b != 0, i.e. the linear
//	              combination of equations yields a nonzero constant.
//	Unique:       Point holds the single solution.
//	Parametric:   Point holds a particular solution; Directions[i] is
//	              the homogeneous direction for the parameter of free
//	              variable FreeVars[i]. FreeVars is strictly increasing,
//	              so parameters correspond to free variables by
//	              increasing column index. The full solution set is
//	              Point + sum_i t_i * Directions[i] over rationals t_i.
type Solution struct {
	Status      Status
	Point       []*big.Rat
	Directions  [][]*big.Rat
	FreeVars    []int
	Certificate []*big.Rat
}

// augRow is one working row of [A | b | T], where T tracks the linear
// combination of the original rows that produced this row.
type augRow struct {
	a   map[int]*big.Rat // n columns of A
	rhs *big.Rat
	t   map[int]*big.Rat // m combination coefficients
}

func (r *augRow) scale(s *big.Rat) {
	for c, v := range r.a {
		v.Mul(v, s)
		if v.Sign() == 0 {
			delete(r.a, c)
		}
	}
	r.rhs.Mul(r.rhs, s)
	for c, v := range r.t {
		v.Mul(v, s)
		if v.Sign() == 0 {
			delete(r.t, c)
		}
	}
}

// addScaled sets r += k * other. other must not be r.
func (r *augRow) addScaled(other *augRow, k *big.Rat) {
	for c, v := range other.a {
		cur, ok := r.a[c]
		if !ok {
			cur = new(big.Rat)
			r.a[c] = cur
		}
		cur.Add(cur, new(big.Rat).Mul(k, v))
		if cur.Sign() == 0 {
			delete(r.a, c)
		}
	}
	r.rhs.Add(r.rhs, new(big.Rat).Mul(k, other.rhs))
	for c, v := range other.t {
		cur, ok := r.t[c]
		if !ok {
			cur = new(big.Rat)
			r.t[c] = cur
		}
		cur.Add(cur, new(big.Rat).Mul(k, v))
		if cur.Sign() == 0 {
			delete(r.t, c)
		}
	}
}

// Solve exactly solves A x = b over the rationals via Gauss-Jordan
// elimination with exact zero tests (no floating-point tolerances).
func Solve(A *SparseMatrix, b []*big.Rat) (*Solution, error) {
	if A == nil {
		return nil, errors.New("linsolve: nil matrix")
	}
	m, n := A.Rows(), A.Cols()
	if len(b) != m {
		return nil, fmt.Errorf("linsolve: rhs length %d, want %d", len(b), m)
	}

	rows := make([]*augRow, m)
	for i := 0; i < m; i++ {
		rhs := new(big.Rat)
		if b[i] != nil {
			rhs.Set(b[i])
		}
		rows[i] = &augRow{
			a:   cloneRow(A.rows[i]),
			rhs: rhs,
			t:   map[int]*big.Rat{i: big.NewRat(1, 1)},
		}
	}

	// Gauss-Jordan to reduced row echelon form.
	rank := 0
	pivotCols := make([]int, 0, n)
	for col := 0; col < n && rank < m; col++ {
		piv := -1
		for i := rank; i < m; i++ {
			if v, ok := rows[i].a[col]; ok && v.Sign() != 0 {
				piv = i
				break
			}
		}
		if piv < 0 {
			continue
		}
		rows[rank], rows[piv] = rows[piv], rows[rank]
		pr := rows[rank]
		pr.scale(new(big.Rat).Inv(pr.a[col]))
		k := new(big.Rat)
		for i := 0; i < m; i++ {
			if i == rank {
				continue
			}
			f, ok := rows[i].a[col]
			if !ok || f.Sign() == 0 {
				continue
			}
			rows[i].addScaled(pr, k.Neg(f))
		}
		pivotCols = append(pivotCols, col)
		rank++
	}

	// Rows below the rank have a zero A-part; a nonzero rhs there is a
	// contradiction, and its T-part is exactly the certificate.
	for i := rank; i < m; i++ {
		if rows[i].rhs.Sign() != 0 {
			return &Solution{
				Status:      Inconsistent,
				Certificate: denseFromSparse(rows[i].t, m),
			}, nil
		}
	}

	isPivot := make([]bool, n)
	for _, c := range pivotCols {
		isPivot[c] = true
	}
	freeVars := make([]int, 0, n-rank)
	for c := 0; c < n; c++ {
		if !isPivot[c] {
			freeVars = append(freeVars, c)
		}
	}

	point := DenseVec(n)
	for j, c := range pivotCols {
		point[c].Set(rows[j].rhs)
	}
	if len(freeVars) == 0 {
		return &Solution{Status: Unique, Point: point}, nil
	}

	directions := make([][]*big.Rat, len(freeVars))
	for i, f := range freeVars {
		d := DenseVec(n)
		d[f].SetInt64(1)
		for j, c := range pivotCols {
			if v, ok := rows[j].a[f]; ok {
				d[c].Neg(v)
			}
		}
		directions[i] = d
	}
	return &Solution{
		Status:     Parametric,
		Point:      point,
		Directions: directions,
		FreeVars:   freeVars,
	}, nil
}

func denseFromSparse(row map[int]*big.Rat, n int) []*big.Rat {
	out := DenseVec(n)
	for c, v := range row {
		if c >= 0 && c < n {
			out[c].Set(v)
		}
	}
	return out
}
