// Package ratls solves overdetermined linear least-squares problems
// exactly over the rationals. No floating-point arithmetic is used
// anywhere: every coefficient is a *big.Rat and every verification
// is an exact identity over Q.
package ratls

import (
	"fmt"
	"math/big"
	"strings"
)

// BudgetError is returned when an intermediate or final coefficient
// exceeds the configured bit-length budget.
type BudgetError struct {
	Bits  int // bit length observed
	Limit int // configured budget
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("ratls: coefficient bit length %d exceeds budget %d", e.Bits, e.Limit)
}

// budget bounds the bit length of any coefficient produced during a
// computation. A limit <= 0 means unbounded.
type budget struct{ limit int }

func bitLen(v *big.Rat) int {
	n := v.Num().BitLen()
	if d := v.Denom().BitLen(); d > n {
		n = d
	}
	return n
}

func (b budget) check(v *big.Rat) error {
	if b.limit <= 0 {
		return nil
	}
	if n := bitLen(v); n > b.limit {
		return &BudgetError{Bits: n, Limit: b.limit}
	}
	return nil
}

func (b budget) add(x, y *big.Rat) (*big.Rat, error) {
	z := new(big.Rat).Add(x, y)
	if err := b.check(z); err != nil {
		return nil, err
	}
	return z, nil
}

func (b budget) sub(x, y *big.Rat) (*big.Rat, error) {
	z := new(big.Rat).Sub(x, y)
	if err := b.check(z); err != nil {
		return nil, err
	}
	return z, nil
}

func (b budget) mul(x, y *big.Rat) (*big.Rat, error) {
	z := new(big.Rat).Mul(x, y)
	if err := b.check(z); err != nil {
		return nil, err
	}
	return z, nil
}

func (b budget) inv(x *big.Rat) (*big.Rat, error) {
	z := new(big.Rat).Inv(x)
	if err := b.check(z); err != nil {
		return nil, err
	}
	return z, nil
}

func (b budget) dot(x, y []*big.Rat) (*big.Rat, error) {
	acc := new(big.Rat)
	for i := range x {
		p, err := b.mul(x[i], y[i])
		if err != nil {
			return nil, err
		}
		acc, err = b.add(acc, p)
		if err != nil {
			return nil, err
		}
	}
	return acc, nil
}

// Matrix is a dense matrix over Q stored in row-major order.
type Matrix struct {
	Rows int
	Cols int
	data []*big.Rat
}

// NewMatrix returns a zero rows x cols matrix.
func NewMatrix(rows, cols int) *Matrix {
	if rows < 0 || cols < 0 {
		panic("ratls: negative matrix dimension")
	}
	d := make([]*big.Rat, rows*cols)
	for i := range d {
		d[i] = new(big.Rat)
	}
	return &Matrix{Rows: rows, Cols: cols, data: d}
}

func (m *Matrix) idx(i, j int) int {
	if i < 0 || i >= m.Rows || j < 0 || j >= m.Cols {
		panic("ratls: matrix index out of range")
	}
	return i*m.Cols + j
}

// e returns the internal entry without copying; callers must not mutate it.
func (m *Matrix) e(i, j int) *big.Rat { return m.data[m.idx(i, j)] }

// At returns a copy of the entry at (i, j).
func (m *Matrix) At(i, j int) *big.Rat { return new(big.Rat).Set(m.e(i, j)) }

// Set stores a copy of v at (i, j).
func (m *Matrix) Set(i, j int, v *big.Rat) { m.data[m.idx(i, j)] = new(big.Rat).Set(v) }

// Copy returns a deep copy of m.
func (m *Matrix) Copy() *Matrix {
	out := NewMatrix(m.Rows, m.Cols)
	for i := range out.data {
		out.data[i].Set(m.data[i])
	}
	return out
}

func (m *Matrix) swapRows(i, j int) {
	if i == j {
		return
	}
	for c := 0; c < m.Cols; c++ {
		m.data[i*m.Cols+c], m.data[j*m.Cols+c] = m.data[j*m.Cols+c], m.data[i*m.Cols+c]
	}
}

// String renders the matrix row by row for debugging.
func (m *Matrix) String() string {
	var sb strings.Builder
	for i := 0; i < m.Rows; i++ {
		sb.WriteString("[")
		for j := 0; j < m.Cols; j++ {
			if j > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(m.e(i, j).RatString())
		}
		sb.WriteString("]\n")
	}
	return sb.String()
}

// ParseMatrix builds a matrix from rational literals ("3", "-2/5").
func ParseMatrix(rows, cols int, entries ...string) (*Matrix, error) {
	if len(entries) != rows*cols {
		return nil, fmt.Errorf("ratls: got %d entries for %dx%d matrix", len(entries), rows, cols)
	}
	m := NewMatrix(rows, cols)
	for i, s := range entries {
		if _, ok := m.data[i].SetString(s); !ok {
			return nil, fmt.Errorf("ratls: invalid rational %q", s)
		}
	}
	return m, nil
}

// ParseVector builds a vector from rational literals.
func ParseVector(entries ...string) ([]*big.Rat, error) {
	v := make([]*big.Rat, len(entries))
	for i, s := range entries {
		v[i] = new(big.Rat)
		if _, ok := v[i].SetString(s); !ok {
			return nil, fmt.Errorf("ratls: invalid rational %q", s)
		}
	}
	return v, nil
}

func transpose(m *Matrix) *Matrix {
	out := NewMatrix(m.Cols, m.Rows)
	for i := 0; i < m.Rows; i++ {
		for j := 0; j < m.Cols; j++ {
			out.data[j*out.Cols+i].Set(m.e(i, j))
		}
	}
	return out
}

func matMul(a, b *Matrix, bud budget) (*Matrix, error) {
	if a.Cols != b.Rows {
		return nil, fmt.Errorf("ratls: matmul dimension mismatch %dx%d * %dx%d", a.Rows, a.Cols, b.Rows, b.Cols)
	}
	out := NewMatrix(a.Rows, b.Cols)
	for i := 0; i < a.Rows; i++ {
		for k := 0; k < a.Cols; k++ {
			aik := a.e(i, k)
			if aik.Sign() == 0 {
				continue
			}
			for j := 0; j < b.Cols; j++ {
				bkj := b.e(k, j)
				if bkj.Sign() == 0 {
					continue
				}
				p, err := bud.mul(aik, bkj)
				if err != nil {
					return nil, err
				}
				s, err := bud.add(out.e(i, j), p)
				if err != nil {
					return nil, err
				}
				out.data[i*out.Cols+j] = s
			}
		}
	}
	return out, nil
}

func matVec(m *Matrix, x []*big.Rat, bud budget) ([]*big.Rat, error) {
	if m.Cols != len(x) {
		return nil, fmt.Errorf("ratls: matvec dimension mismatch %dx%d * %d", m.Rows, m.Cols, len(x))
	}
	out := make([]*big.Rat, m.Rows)
	for i := 0; i < m.Rows; i++ {
		acc := new(big.Rat)
		for j := 0; j < m.Cols; j++ {
			aj := m.e(i, j)
			if aj.Sign() == 0 || x[j].Sign() == 0 {
				continue
			}
			p, err := bud.mul(aj, x[j])
			if err != nil {
				return nil, err
			}
			acc, err = bud.add(acc, p)
			if err != nil {
				return nil, err
			}
		}
		out[i] = acc
	}
	return out, nil
}

func vecEqual(a, b []*big.Rat) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Cmp(b[i]) != 0 {
			return false
		}
	}
	return true
}

func vecIsZero(v []*big.Rat) bool {
	for _, x := range v {
		if x.Sign() != 0 {
			return false
		}
	}
	return true
}

func copyVec(v []*big.Rat) []*big.Rat {
	out := make([]*big.Rat, len(v))
	for i, x := range v {
		out[i] = new(big.Rat).Set(x)
	}
	return out
}
