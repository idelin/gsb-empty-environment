package ilse

import "math/big"

// Mat is a dense integer matrix backed by big.Int, row-major.
type Mat struct {
	Rows, Cols int
	Data       []*big.Int
}

func NewMat(rows, cols int) *Mat {
	d := make([]*big.Int, rows*cols)
	for i := range d {
		d[i] = new(big.Int)
	}
	return &Mat{Rows: rows, Cols: cols, Data: d}
}

func Eye(n int) *Mat {
	m := NewMat(n, n)
	for i := 0; i < n; i++ {
		m.Data[i*n+i].SetInt64(1)
	}
	return m
}

func (m *Mat) At(i, j int) *big.Int { return m.Data[i*m.Cols+j] }

func (m *Mat) Set(i, j int, v *big.Int) { m.Data[i*m.Cols+j].Set(v) }

func (m *Mat) Copy() *Mat {
	c := NewMat(m.Rows, m.Cols)
	for i, v := range m.Data {
		c.Data[i].Set(v)
	}
	return c
}

func (m *Mat) Equal(o *Mat) bool {
	if m.Rows != o.Rows || m.Cols != o.Cols {
		return false
	}
	for i, v := range m.Data {
		if v.Cmp(o.Data[i]) != 0 {
			return false
		}
	}
	return true
}

func (m *Mat) IsZero() bool {
	for _, v := range m.Data {
		if v.Sign() != 0 {
			return false
		}
	}
	return true
}

func (m *Mat) Mul(o *Mat) *Mat {
	if m.Cols != o.Rows {
		panic("ilse: dimension mismatch in Mul")
	}
	r := NewMat(m.Rows, o.Cols)
	t := new(big.Int)
	for i := 0; i < m.Rows; i++ {
		for k := 0; k < m.Cols; k++ {
			a := m.At(i, k)
			if a.Sign() == 0 {
				continue
			}
			for j := 0; j < o.Cols; j++ {
				b := o.At(k, j)
				if b.Sign() == 0 {
					continue
				}
				t.Mul(a, b)
				r.Data[i*r.Cols+j].Add(r.Data[i*r.Cols+j], t)
			}
		}
	}
	return r
}

// MulVec computes m*x for a column vector x of length m.Cols.
func (m *Mat) MulVec(x []*big.Int) []*big.Int {
	if len(x) != m.Cols {
		panic("ilse: dimension mismatch in MulVec")
	}
	r := make([]*big.Int, m.Rows)
	t := new(big.Int)
	for i := 0; i < m.Rows; i++ {
		r[i] = new(big.Int)
		for j := 0; j < m.Cols; j++ {
			t.Mul(m.At(i, j), x[j])
			r[i].Add(r[i], t)
		}
	}
	return r
}

// SubCols returns the matrix formed by columns [start, end).
func (m *Mat) SubCols(start, end int) *Mat {
	r := NewMat(m.Rows, end-start)
	for i := 0; i < m.Rows; i++ {
		for j := start; j < end; j++ {
			r.Set(i, j-start, m.At(i, j))
		}
	}
	return r
}

func (m *Mat) swapCols(a, b int) {
	if a == b {
		return
	}
	for i := 0; i < m.Rows; i++ {
		m.Data[i*m.Cols+a], m.Data[i*m.Cols+b] = m.Data[i*m.Cols+b], m.Data[i*m.Cols+a]
	}
}

// combineCols applies the unimodular column transform
//
//	col_a <- s*col_a + t*col_b
//	col_b <- u*col_b - v*col_a   (using the ORIGINAL columns)
//
// where s,t,u,v satisfy s*u + t*v = 1 (determinant +1).
func (m *Mat) combineCols(a, b int, s, t, v, u *big.Int) {
	ta, tb := new(big.Int), new(big.Int)
	for i := 0; i < m.Rows; i++ {
		ca := new(big.Int).Set(m.Data[i*m.Cols+a])
		cb := new(big.Int).Set(m.Data[i*m.Cols+b])
		// new col a
		ta.Mul(s, ca)
		tb.Mul(t, cb)
		m.Data[i*m.Cols+a].Add(ta, tb)
		// new col b
		tb.Mul(u, cb)
		ta.Mul(v, ca)
		m.Data[i*m.Cols+b].Sub(tb, ta)
	}
}

// Det computes the determinant of a square matrix using the
// fraction-free Bareiss algorithm. Returns 0 for a 0x0 matrix is
// avoided: Det of the empty matrix is defined as 1.
func Det(m *Mat) *big.Int {
	if m.Rows != m.Cols {
		panic("ilse: Det of non-square matrix")
	}
	n := m.Rows
	if n == 0 {
		return big.NewInt(1)
	}
	a := m.Copy()
	sign := int64(1)
	prev := big.NewInt(1)
	t := new(big.Int)
	for k := 0; k < n-1; k++ {
		if a.At(k, k).Sign() == 0 {
			p := -1
			for i := k + 1; i < n; i++ {
				if a.At(i, k).Sign() != 0 {
					p = i
					break
				}
			}
			if p < 0 {
				return new(big.Int)
			}
			for j := 0; j < n; j++ {
				a.Data[k*n+j], a.Data[p*n+j] = a.Data[p*n+j], a.Data[k*n+j]
			}
			sign = -sign
		}
		pivot := a.At(k, k)
		for i := k + 1; i < n; i++ {
			aik := a.At(i, k)
			for j := k + 1; j < n; j++ {
				t.Mul(a.At(i, j), pivot)
				t.Sub(t, new(big.Int).Mul(aik, a.At(k, j)))
				t.Quo(t, prev)
				a.Data[i*n+j].Set(t)
			}
			a.Data[i*n+k].SetInt64(0)
		}
		prev.Set(pivot)
	}
	res := new(big.Int).Set(a.At(n-1, n-1))
	if sign < 0 {
		res.Neg(res)
	}
	return res
}

// Rank computes the rank over Q via Gaussian elimination on big.Rat.
func Rank(m *Mat) int {
	rows, cols := m.Rows, m.Cols
	a := make([][]*big.Rat, rows)
	for i := 0; i < rows; i++ {
		a[i] = make([]*big.Rat, cols)
		for j := 0; j < cols; j++ {
			a[i][j] = new(big.Rat).SetInt(m.At(i, j))
		}
	}
	rank := 0
	for col := 0; col < cols && rank < rows; col++ {
		p := -1
		for i := rank; i < rows; i++ {
			if a[i][col].Sign() != 0 {
				p = i
				break
			}
		}
		if p < 0 {
			continue
		}
		a[rank], a[p] = a[p], a[rank]
		inv := new(big.Rat).Inv(a[rank][col])
		for j := col; j < cols; j++ {
			a[rank][j].Mul(a[rank][j], inv)
		}
		for i := 0; i < rows; i++ {
			if i == rank || a[i][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(a[i][col])
			for j := col; j < cols; j++ {
				t := new(big.Rat).Mul(f, a[rank][j])
				a[i][j].Sub(a[i][j], t)
			}
		}
		rank++
	}
	return rank
}
