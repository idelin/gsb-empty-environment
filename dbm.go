package netsec

import "math"

// Inf represents the absence of an upper bound in a difference constraint.
// Use -Inf for an absent lower bound in guards.
const Inf = int64(math.MaxInt64)

// satAdd adds two bounds, saturating on overflow and propagating Inf.
func satAdd(a, b int64) int64 {
	if a == Inf || b == Inf {
		return Inf
	}
	s := a + b
	if a > 0 && b > 0 && s <= 0 {
		return Inf
	}
	if a < 0 && b < 0 && s >= 0 {
		return math.MinInt64 + 1
	}
	return s
}

// satNeg negates a bound, mapping -Inf to Inf.
func satNeg(a int64) int64 {
	if a == math.MinInt64 {
		return Inf
	}
	return -a
}

// dbm is a closed difference bound matrix over n variables.
// d[i*n+j] = c means x_i - x_j <= c. Variable 0 is the constant zero.
type dbm struct {
	n   int
	d   []int64
	nxt []int // next-hop matrix for justification path reconstruction
}

func newDBM(n int) *dbm {
	m := &dbm{n: n, d: make([]int64, n*n), nxt: make([]int, n*n)}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			m.d[i*n+j] = Inf
			m.nxt[i*n+j] = j
		}
		m.d[i*n+i] = 0
	}
	return m
}

func (m *dbm) clone() *dbm {
	c := &dbm{n: m.n, d: make([]int64, len(m.d)), nxt: make([]int, len(m.nxt))}
	copy(c.d, m.d)
	copy(c.nxt, m.nxt)
	return c
}

func (m *dbm) at(i, j int) int64 { return m.d[i*m.n+j] }

func (m *dbm) tighten(i, j int, c int64) {
	if c < m.d[i*m.n+j] {
		m.d[i*m.n+j] = c
		m.nxt[i*m.n+j] = j
	}
}

// close computes the tightest closure (Floyd-Warshall) and reports
// feasibility: a negative diagonal entry means the constraints are
// unsatisfiable.
func (m *dbm) close() bool {
	n := m.n
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			ik := m.d[i*n+k]
			if ik == Inf {
				continue
			}
			for j := 0; j < n; j++ {
				kj := m.d[k*n+j]
				if kj == Inf {
					continue
				}
				if v := satAdd(ik, kj); v < m.d[i*n+j] {
					m.d[i*n+j] = v
					m.nxt[i*n+j] = m.nxt[i*n+k]
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		if m.d[i*n+i] < 0 {
			return false
		}
	}
	return true
}

// guard adds the constraint lo <= x_x - x_y <= hi.
func (m *dbm) guard(x, y int, lo, hi int64) {
	m.tighten(x, y, hi)
	m.tighten(y, x, satNeg(lo))
}

// assignAdd applies the exact transformer for x := x + c.
func (m *dbm) assignAdd(x int, c int64) {
	n := m.n
	neg := satNeg(c)
	for j := 0; j < n; j++ {
		if j != x {
			m.d[x*n+j] = satAdd(m.d[x*n+j], c)
		}
	}
	for i := 0; i < n; i++ {
		if i != x {
			m.d[i*n+x] = satAdd(m.d[i*n+x], neg)
		}
	}
}

func (m *dbm) equal(o *dbm) bool {
	if m == nil || o == nil {
		return m == o
	}
	if m.n != o.n {
		return false
	}
	for k := range m.d {
		if m.d[k] != o.d[k] {
			return false
		}
	}
	return true
}

// join computes the least upper bound (elementwise max, then closure).
func join(a, b *dbm) *dbm {
	r := newDBM(a.n)
	for k := range r.d {
		if a.d[k] > b.d[k] {
			r.d[k] = a.d[k]
		} else {
			r.d[k] = b.d[k]
		}
	}
	r.close()
	return r
}

// widen is the standard DBM widening: keep old bounds that stay stable,
// jump unstable ones to infinity.
func widen(old, new *dbm) *dbm {
	r := newDBM(old.n)
	for k := range r.d {
		if new.d[k] <= old.d[k] {
			r.d[k] = old.d[k]
		} else {
			r.d[k] = Inf
		}
	}
	r.close()
	return r
}

// narrow is the standard DBM narrowing: refine infinite old bounds with
// the new (tighter) iterate, keep finite old bounds.
func narrow(old, new *dbm) *dbm {
	r := newDBM(old.n)
	for k := range r.d {
		if old.d[k] == Inf {
			r.d[k] = new.d[k]
		} else {
			r.d[k] = old.d[k]
		}
	}
	r.close()
	return r
}

// path reconstructs a chain of matrix edges whose bounds sum to d[i][j],
// used to justify derived constraints.
func (m *dbm) path(i, j int) [][2]int {
	if m.at(i, j) == Inf {
		return nil
	}
	var p [][2]int
	cur := i
	for cur != j && len(p) <= m.n+1 {
		nx := m.nxt[cur*m.n+j]
		p = append(p, [2]int{cur, nx})
		cur = nx
	}
	return p
}
