// Package ratlsq solves overdetermined linear least-squares problems
// exactly over the rational numbers. All arithmetic uses math/big.Rat;
// no floating-point value ever enters the computation.
package ratlsq

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ErrDimension is returned when operand shapes do not match.
var ErrDimension = errors.New("ratlsq: dimension mismatch")

// ErrInconsistent is returned when a linear system has no solution.
// It never occurs for normal equations, but can surface in verification.
var ErrInconsistent = errors.New("ratlsq: inconsistent linear system")

// Budget bounds the bit length of every intermediate rational's numerator
// and denominator, controlling coefficient blow-up. The zero Budget is
// unlimited.
type Budget struct {
	MaxBits int
}

// BudgetError reports an intermediate rational that exceeded the Budget.
type BudgetError struct {
	Op   string
	Bits int
	Max  int
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("ratlsq: bit budget exceeded during %s: %d bits (limit %d)", e.Op, e.Bits, e.Max)
}

func (b Budget) check(op string, x *big.Rat) error {
	if b.MaxBits <= 0 {
		return nil
	}
	if bits := x.Num().BitLen(); bits > b.MaxBits {
		return &BudgetError{Op: op, Bits: bits, Max: b.MaxBits}
	}
	if bits := x.Denom().BitLen(); bits > b.MaxBits {
		return &BudgetError{Op: op, Bits: bits, Max: b.MaxBits}
	}
	return nil
}

// Vector is a dense vector of rationals.
type Vector struct {
	data []*big.Rat
}

// NewVector returns a zero vector of length n.
func NewVector(n int) *Vector {
	v := &Vector{data: make([]*big.Rat, n)}
	for i := range v.data {
		v.data[i] = new(big.Rat)
	}
	return v
}

// Len returns the dimension of v.
func (v *Vector) Len() int { return len(v.data) }

// At returns the i-th component. The result must not be mutated.
func (v *Vector) At(i int) *big.Rat { return v.data[i] }

// Set assigns a copy of x to the i-th component.
func (v *Vector) Set(i int, x *big.Rat) { v.data[i] = new(big.Rat).Set(x) }

// Clone returns a deep copy of v.
func (v *Vector) Clone() *Vector {
	w := NewVector(v.Len())
	for i := range w.data {
		w.data[i].Set(v.data[i])
	}
	return w
}

// IsZero reports whether every component is zero.
func (v *Vector) IsZero() bool {
	for _, x := range v.data {
		if x.Sign() != 0 {
			return false
		}
	}
	return true
}

// Equal reports exact component-wise equality.
func (v *Vector) Equal(w *Vector) bool {
	if v.Len() != w.Len() {
		return false
	}
	for i := range v.data {
		if v.data[i].Cmp(w.data[i]) != 0 {
			return false
		}
	}
	return true
}

func (v *Vector) String() string {
	parts := make([]string, v.Len())
	for i, x := range v.data {
		parts[i] = x.RatString()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// Matrix is a dense row-major matrix of rationals.
type Matrix struct {
	Rows int
	Cols int
	data []*big.Rat
}

// NewMatrix returns a rows-by-cols zero matrix. Either dimension may be 0.
func NewMatrix(rows, cols int) *Matrix {
	m := &Matrix{Rows: rows, Cols: cols, data: make([]*big.Rat, rows*cols)}
	for i := range m.data {
		m.data[i] = new(big.Rat)
	}
	return m
}

func (m *Matrix) get(i, j int) *big.Rat { return m.data[i*m.Cols+j] }

func (m *Matrix) set(i, j int, x *big.Rat) { m.data[i*m.Cols+j] = new(big.Rat).Set(x) }

// At returns the (i, j) entry. The result must not be mutated.
func (m *Matrix) At(i, j int) *big.Rat { return m.get(i, j) }

// Set assigns a copy of x to the (i, j) entry.
func (m *Matrix) Set(i, j int, x *big.Rat) { m.set(i, j, x) }

// Clone returns a deep copy of m.
func (m *Matrix) Clone() *Matrix {
	c := NewMatrix(m.Rows, m.Cols)
	for i, x := range m.data {
		c.data[i].Set(x)
	}
	return c
}

func (m *Matrix) swapRows(r1, r2 int) {
	if r1 == r2 {
		return
	}
	for j := 0; j < m.Cols; j++ {
		m.data[r1*m.Cols+j], m.data[r2*m.Cols+j] = m.data[r2*m.Cols+j], m.data[r1*m.Cols+j]
	}
}

// Transpose returns the exact transpose of m.
func (m *Matrix) Transpose() *Matrix {
	t := NewMatrix(m.Cols, m.Rows)
	for i := 0; i < m.Rows; i++ {
		for j := 0; j < m.Cols; j++ {
			t.set(j, i, m.get(i, j))
		}
	}
	return t
}

// Mul returns m*o computed exactly, honoring the budget.
func (m *Matrix) Mul(o *Matrix, budget Budget) (*Matrix, error) {
	if m.Cols != o.Rows {
		return nil, ErrDimension
	}
	out := NewMatrix(m.Rows, o.Cols)
	tmp := new(big.Rat)
	acc := new(big.Rat)
	for i := 0; i < m.Rows; i++ {
		for j := 0; j < o.Cols; j++ {
			acc.SetInt64(0)
			for k := 0; k < m.Cols; k++ {
				tmp.Mul(m.get(i, k), o.get(k, j))
				acc.Add(acc, tmp)
				if err := budget.check("matrix product", acc); err != nil {
					return nil, err
				}
			}
			out.set(i, j, acc)
		}
	}
	return out, nil
}

// MulVec returns m*v computed exactly, honoring the budget.
func (m *Matrix) MulVec(v *Vector, budget Budget) (*Vector, error) {
	if m.Cols != v.Len() {
		return nil, ErrDimension
	}
	out := NewVector(m.Rows)
	tmp := new(big.Rat)
	acc := new(big.Rat)
	for i := 0; i < m.Rows; i++ {
		acc.SetInt64(0)
		for j := 0; j < m.Cols; j++ {
			tmp.Mul(m.get(i, j), v.At(j))
			acc.Add(acc, tmp)
			if err := budget.check("matrix-vector product", acc); err != nil {
				return nil, err
			}
		}
		out.Set(i, acc)
	}
	return out, nil
}

func (m *Matrix) String() string {
	rows := make([]string, m.Rows)
	for i := 0; i < m.Rows; i++ {
		parts := make([]string, m.Cols)
		for j := 0; j < m.Cols; j++ {
			parts[j] = m.get(i, j).RatString()
		}
		rows[i] = "[" + strings.Join(parts, " ") + "]"
	}
	return strings.Join(rows, "\n")
}
