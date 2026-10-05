package ilse

import (
	"errors"
	"fmt"
	"math/big"
)

// VerifySolution independently checks that sol describes exactly the integer
// solution set of A x = b. It verifies:
//
//  1. A*X0 = b (the particular solution satisfies the original system);
//  2. A*B = 0 (every basis vector is a homogeneous solution);
//  3. completeness of the generated lattice: U is unimodular (det = ±1),
//     A*U = [H | 0] with H of full column rank, X0 = U*[Y;0], B = U[:, r:],
//     H*Y = b. Then for any integer x with A x = b, w = U^{-1} x is integral
//     and H*w[:r] = b forces w[:r] = Y (H has full column rank), so
//     x = X0 + B*w[r:]: every integer solution lies in the lattice.
//  4. as an extra independent cross-check (when the number of maximal minors
//     is bounded), that the gcd of the maximal minors of B is 1, i.e. B
//     spans a primitive (saturated) sublattice of Z^n.
func VerifySolution(A *Mat, b []*big.Int, sol *Solution) error {
	if sol == nil {
		return errors.New("ilse: nil solution")
	}
	m, n := A.Rows, A.Cols
	r := sol.Rank
	if len(sol.X0) != n {
		return fmt.Errorf("ilse: X0 has length %d, want %d", len(sol.X0), n)
	}
	if sol.B.Rows != n || sol.B.Cols != n-r {
		return fmt.Errorf("ilse: B has shape %dx%d, want %dx%d", sol.B.Rows, sol.B.Cols, n, n-r)
	}
	if sol.U == nil || sol.U.Rows != n || sol.U.Cols != n {
		return fmt.Errorf("ilse: U must be %dx%d", n, n)
	}
	if sol.H == nil || sol.H.Rows != m || sol.H.Cols != r {
		return fmt.Errorf("ilse: H has wrong shape")
	}
	if len(sol.Y) != r {
		return fmt.Errorf("ilse: Y has length %d, want %d", len(sol.Y), r)
	}

	// 1. A*X0 = b.
	if got := A.MulVec(sol.X0); !vecEqual(got, b) {
		return errors.New("ilse: A*X0 != b")
	}
	// 2. A*B = 0.
	if !A.Mul(sol.B).IsZero() {
		return errors.New("ilse: A*B != 0: basis vector is not homogeneous")
	}
	// 3a. U unimodular.
	d := Det(sol.U)
	if d.Cmp(big.NewInt(1)) != 0 && d.Cmp(big.NewInt(-1)) != 0 {
		return fmt.Errorf("ilse: det(U) = %s, not a unit", d)
	}
	// 3b. A*U = [H | 0].
	AU := A.Mul(sol.U)
	if !AU.SubCols(0, r).Equal(sol.H) {
		return errors.New("ilse: A*U[:, :r] != H")
	}
	if !AU.SubCols(r, n).IsZero() {
		return errors.New("ilse: A*U[:, r:] != 0")
	}
	// 3c. H has full column rank.
	if Rank(sol.H) != r {
		return errors.New("ilse: H does not have full column rank")
	}
	// 3d. H*Y = b.
	if got := sol.H.MulVec(sol.Y); !vecEqual(got, b) {
		return errors.New("ilse: H*Y != b")
	}
	// 3e. X0 = U[:, :r]*Y and B = U[:, r:].
	if got := sol.U.SubCols(0, r).MulVec(sol.Y); !vecEqual(got, sol.X0) {
		return errors.New("ilse: X0 != U[:, :r]*Y")
	}
	if !sol.U.SubCols(r, n).Equal(sol.B) {
		return errors.New("ilse: B != U[:, r:]")
	}
	// 4. Independent primitivity check on B (bounded minor count).
	if n > r && choose(n, n-r) <= 2000 {
		g := minorGCD(sol.B)
		if g.Cmp(big.NewInt(1)) != 0 {
			return fmt.Errorf("ilse: gcd of maximal minors of B is %s, not 1: lattice not saturated", g)
		}
	}
	return nil
}

// VerifyInfeasible checks the certificate c: the set {c*A*x : x in Z^n} is
// g*Z where g is the rational gcd of the entries of c*A (g = 0 iff c*A = 0),
// and the certificate is valid iff c*b does not lie in g*Z.
func VerifyInfeasible(A *Mat, b []*big.Int, inf *Infeasible) error {
	if inf == nil || len(inf.C) != A.Rows {
		return errors.New("ilse: malformed infeasibility certificate")
	}
	c := inf.C
	// cA = c*A (rational row vector).
	cA := make([]*big.Rat, A.Cols)
	for j := 0; j < A.Cols; j++ {
		cA[j] = new(big.Rat)
		for i := 0; i < A.Rows; i++ {
			t := new(big.Rat).Mul(c[i], new(big.Rat).SetInt(A.At(i, j)))
			cA[j].Add(cA[j], t)
		}
	}
	// cb = c*b.
	cb := new(big.Rat)
	for i := 0; i < A.Rows; i++ {
		cb.Add(cb, new(big.Rat).Mul(c[i], new(big.Rat).SetInt(b[i])))
	}
	g := ratGCD(cA)
	if g.Sign() == 0 {
		if cb.Sign() == 0 {
			return errors.New("ilse: invalid certificate: c*A = 0 but c*b = 0")
		}
		return nil
	}
	// cb must not be an integer multiple of g.
	q := new(big.Rat).Quo(cb, g)
	if q.IsInt() {
		return errors.New("ilse: invalid certificate: c*b is in (c*A)*Z^n")
	}
	return nil
}

// InLattice reports whether x equals X0 + B*z for some integer vector z,
// using the unimodular certificate: w = U^{-1}(x - X0) must be integral with
// w[:r] = 0 (then z = w[r:]).
func (sol *Solution) InLattice(x []*big.Int) bool {
	n := sol.U.Cols
	if len(x) != n || len(sol.X0) != n {
		return false
	}
	d := make([]*big.Rat, n)
	for i := 0; i < n; i++ {
		d[i] = new(big.Rat).SetInt(new(big.Int).Sub(x[i], sol.X0[i]))
	}
	w, ok := ratSolve(sol.U, d)
	if !ok {
		return false
	}
	for i := 0; i < n; i++ {
		if !w[i].IsInt() {
			return false
		}
	}
	for j := 0; j < sol.Rank; j++ {
		if w[j].Sign() != 0 {
			return false
		}
	}
	return true
}

// LatticePoint returns X0 + B*z.
func (sol *Solution) LatticePoint(z []*big.Int) []*big.Int {
	x := make([]*big.Int, len(sol.X0))
	for i := range x {
		x[i] = new(big.Int).Set(sol.X0[i])
	}
	t := new(big.Int)
	for j := 0; j < sol.B.Cols; j++ {
		for i := 0; i < sol.B.Rows; i++ {
			t.Mul(sol.B.At(i, j), z[j])
			x[i].Add(x[i], t)
		}
	}
	return x
}

// ratGCD returns the nonnegative rational g with {sum a_i q_i : a_i in Z} = g*Q
// restricted to integers, i.e. g = gcd(numerators)/lcm(denominators); g = 0
// iff all entries are zero.
func ratGCD(q []*big.Rat) *big.Rat {
	num := new(big.Int)
	den := big.NewInt(1)
	for _, x := range q {
		if x.Sign() == 0 {
			continue
		}
		num.GCD(nil, nil, num, x.Num())
		den.Mul(den, new(big.Int).Quo(x.Denom(), new(big.Int).GCD(nil, nil, den, x.Denom())))
	}
	if num.Sign() == 0 {
		return new(big.Rat)
	}
	return new(big.Rat).SetFrac(num, den)
}

// minorGCD computes the gcd of the absolute values of all maximal minors of
// m (which must have full column rank). The lattice generated by the columns
// of m is saturated (a direct summand of Z^n) iff this gcd is 1.
func minorGCD(m *Mat) *big.Int {
	k := m.Cols
	g := new(big.Int)
	rows := make([]int, k)
	var rec func(start, depth int)
	rec = func(start, depth int) {
		if depth == k {
			sub := NewMat(k, k)
			for i := 0; i < k; i++ {
				for j := 0; j < k; j++ {
					sub.Set(i, j, m.At(rows[i], j))
				}
			}
			d := Det(sub)
			g.GCD(nil, nil, g, d)
			return
		}
		for i := start; i <= m.Rows-(k-depth); i++ {
			rows[depth] = i
			rec(i+1, depth+1)
		}
	}
	rec(0, 0)
	return g
}

// ratSolve solves U*w = d over the rationals by Gaussian elimination.
func ratSolve(U *Mat, d []*big.Rat) ([]*big.Rat, bool) {
	n := U.Rows
	if U.Cols != n || len(d) != n {
		return nil, false
	}
	a := make([][]*big.Rat, n)
	for i := 0; i < n; i++ {
		a[i] = make([]*big.Rat, n+1)
		for j := 0; j < n; j++ {
			a[i][j] = new(big.Rat).SetInt(U.At(i, j))
		}
		a[i][n] = new(big.Rat).Set(d[i])
	}
	for col := 0; col < n; col++ {
		p := -1
		for i := col; i < n; i++ {
			if a[i][col].Sign() != 0 {
				p = i
				break
			}
		}
		if p < 0 {
			return nil, false
		}
		a[col], a[p] = a[p], a[col]
		inv := new(big.Rat).Inv(a[col][col])
		for j := col; j <= n; j++ {
			a[col][j].Mul(a[col][j], inv)
		}
		for i := 0; i < n; i++ {
			if i == col || a[i][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(a[i][col])
			for j := col; j <= n; j++ {
				t := new(big.Rat).Mul(f, a[col][j])
				a[i][j].Sub(a[i][j], t)
			}
		}
	}
	w := make([]*big.Rat, n)
	for i := 0; i < n; i++ {
		w[i] = a[i][n]
	}
	return w, true
}

func vecEqual(a, b []*big.Int) bool {
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

func choose(n, k int) int64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	c := int64(1)
	for i := 0; i < k; i++ {
		c = c * int64(n-i) / int64(i+1)
		if c > 1<<62 {
			return 1 << 62
		}
	}
	return c
}
