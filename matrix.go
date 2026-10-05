package unitinfer

// Exact integer linear algebra: Hermite normal form based solving of
// A x = b over the integers. All arithmetic stays in int; systems produced
// by unit inference are small, so no big-number support is needed.

// extgcd returns g = gcd(a, b) >= 0 and Bezout coefficients s, t with
// s*a + t*b = g.
func extgcd(a, b int) (g, s, t int) {
	if b == 0 {
		if a < 0 {
			return -a, -1, 0
		}
		return a, 1, 0
	}
	g, s1, t1 := extgcd(b, a%b)
	return g, t1, s1 - (a/b)*t1
}

// floorDiv returns floor(a/b) for b > 0.
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func identity(n int) [][]int {
	m := make([][]int, n)
	for i := range m {
		m[i] = make([]int, n)
		m[i][i] = 1
	}
	return m
}

func copyMatrix(a [][]int) [][]int {
	m := make([][]int, len(a))
	for i := range a {
		m[i] = make([]int, len(a[i]))
		copy(m[i], a[i])
	}
	return m
}

func swapCols(m [][]int, i, j int) {
	if i == j {
		return
	}
	for r := range m {
		m[r][i], m[r][j] = m[r][j], m[r][i]
	}
}

// combineCols replaces (colJ, colK) with (p*colJ + q*colK, r*colJ + s*colK).
func combineCols(m [][]int, j, k, p, q, r, s int) {
	for row := range m {
		cj, ck := m[row][j], m[row][k]
		m[row][j] = p*cj + q*ck
		m[row][k] = r*cj + s*ck
	}
}

// hnf computes a unimodular matrix U (n x n) such that A*U = H where H is in
// column-style lower-trapezoidal Hermite normal form: the first r columns
// hold the pivots (positive, one per pivot row, zeros to their right and
// above), the remaining columns are zero. Returns H, U and the rank r.
//
// Consequences used by the solver:
//   - columns r..n-1 of U form a Z-basis of the integer kernel of A;
//   - A x = b is solvable over Z iff H y = b is solvable with y2 = 0.
func hnf(a [][]int) (h, u [][]int, rank int) {
	m := len(a)
	n := 0
	if m > 0 {
		n = len(a[0])
	}
	h = copyMatrix(a)
	u = identity(n)
	r := 0
	for i := 0; i < m && r < n; i++ {
		// Find a nonzero entry in row i at columns >= r; if none, the row
		// contributes no pivot and we keep the same r for the next row.
		j := -1
		for c := r; c < n; c++ {
			if h[i][c] != 0 {
				j = c
				break
			}
		}
		if j == -1 {
			continue
		}
		swapCols(h, j, r)
		swapCols(u, j, r)
		// Zero out row i at columns > r via unimodular column combinations.
		for c := r + 1; c < n; c++ {
			if h[i][c] != 0 {
				g, s, t := extgcd(h[i][r], h[i][c])
				a1 := h[i][r] / g
				b1 := h[i][c] / g
				// col_r <- s*col_r + t*col_c ; col_c <- -b1*col_r + a1*col_c
				combineCols(h, r, c, s, t, -b1, a1)
				combineCols(u, r, c, s, t, -b1, a1)
			}
		}
		if h[i][r] < 0 {
			for row := 0; row < m; row++ {
				h[row][r] = -h[row][r]
			}
			for row := 0; row < n; row++ {
				u[row][r] = -u[row][r]
			}
		}
		// Reduce entries left of the pivot modulo the pivot (canonical form).
		for c := 0; c < r; c++ {
			if h[i][c] != 0 {
				q := floorDiv(h[i][c], h[i][r])
				for row := 0; row < m; row++ {
					h[row][c] -= q * h[row][r]
				}
				for row := 0; row < n; row++ {
					u[row][c] -= q * u[row][r]
				}
			}
		}
		r++
	}
	return h, u, r
}

// solveSystem solves A x = b over the integers. n is the number of unknowns
// (needed when a has no rows). On success it returns a particular solution
// and a Z-basis of the integer kernel of A (each basis vector has length n),
// so the full solution set is { part + sum lambda_k * basis_k : lambda in Z^k }.
func solveSystem(a [][]int, b []int, n int) (part []int, basis [][]int, ok bool) {
	m := len(a)
	if m == 0 {
		// No equations: everything is free, kernel = whole space.
		part = make([]int, n)
		for i := 0; i < n; i++ {
			v := make([]int, n)
			v[i] = 1
			basis = append(basis, v)
		}
		return part, basis, true
	}
	if n == 0 {
		for i := 0; i < m; i++ {
			if b[i] != 0 {
				return nil, nil, false
			}
		}
		return []int{}, nil, true
	}
	h, u, r := hnf(a)
	// Solve H y = b for the pivot part y1 (columns 0..r-1); free part = 0.
	y1 := make([]int, r)
	assigned := make([]bool, r)
	for i := 0; i < m; i++ {
		p := -1
		for j := 0; j < r; j++ {
			if h[i][j] != 0 {
				p = j // largest nonzero column is this row's pivot
			}
		}
		if p == -1 {
			if b[i] != 0 {
				return nil, nil, false
			}
			continue
		}
		if assigned[p] {
			// No pivot of its own: pure consistency check against the
			// already-determined unknowns.
			sum := 0
			for j := 0; j < r; j++ {
				sum += h[i][j] * y1[j]
			}
			if sum != b[i] {
				return nil, nil, false
			}
			continue
		}
		rhs := b[i]
		for j := 0; j < r; j++ {
			if j != p {
				rhs -= h[i][j] * y1[j]
			}
		}
		if rhs%h[i][p] != 0 {
			return nil, nil, false
		}
		y1[p] = rhs / h[i][p]
		assigned[p] = true
	}
	// Particular solution x0 = U[:, 0:r] * y1.
	part = make([]int, n)
	for j := 0; j < r; j++ {
		if y1[j] != 0 {
			for row := 0; row < n; row++ {
				part[row] += u[row][j] * y1[j]
			}
		}
	}
	// Kernel basis = columns r..n-1 of U.
	for j := r; j < n; j++ {
		v := make([]int, n)
		for row := 0; row < n; row++ {
			v[row] = u[row][j]
		}
		basis = append(basis, v)
	}
	return part, basis, true
}
