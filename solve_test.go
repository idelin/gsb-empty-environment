package linsolve

import (
	"math/big"
	"math/rand"
	"testing"
)

func rat(v int64) *big.Rat { return big.NewRat(v, 1) }

func vecEqRat(got []*big.Rat, want ...int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i, w := range want {
		if got[i].Cmp(rat(w)) != 0 {
			return false
		}
	}
	return true
}

func TestUniqueSolution(t *testing.T) {
	// 2x + y = 5 ; x + 3y = 5  =>  x = 2, y = 1
	A := NewSparseMatrix(2, 2)
	A.Set(0, 0, rat(2))
	A.Set(0, 1, rat(1))
	A.Set(1, 0, rat(1))
	A.Set(1, 1, rat(3))
	b := []*big.Rat{rat(5), rat(5)}

	s, err := Solve(A, b)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != Unique {
		t.Fatalf("status = %v, want Unique", s.Status)
	}
	if !vecEqRat(s.Point, 2, 1) {
		t.Fatalf("point = %v, want [2 1]", s.Point)
	}
	if !Check(A, b, s) {
		t.Fatal("independent check failed for unique solution")
	}
}

func TestParametricSolution(t *testing.T) {
	// x + y + z = 3 ; x - y = 1  =>  free var z (index 2)
	// x = 2 - z/2, y = 1 - z/2
	A := NewSparseMatrix(2, 3)
	A.Set(0, 0, rat(1))
	A.Set(0, 1, rat(1))
	A.Set(0, 2, rat(1))
	A.Set(1, 0, rat(1))
	A.Set(1, 1, big.NewRat(-1, 1))
	b := []*big.Rat{rat(3), rat(1)}

	s, err := Solve(A, b)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != Parametric {
		t.Fatalf("status = %v, want Parametric", s.Status)
	}
	if len(s.FreeVars) != 1 || s.FreeVars[0] != 2 {
		t.Fatalf("free vars = %v, want [2]", s.FreeVars)
	}
	if !vecEqRat(s.Point, 2, 1, 0) {
		t.Fatalf("particular = %v, want [2 1 0]", s.Point)
	}
	half := big.NewRat(-1, 2)
	d := s.Directions[0]
	if d[0].Cmp(half) != 0 || d[1].Cmp(half) != 0 || d[2].Cmp(rat(1)) != 0 {
		t.Fatalf("direction = %v, want [-1/2 -1/2 1]", d)
	}
	if !Check(A, b, s) {
		t.Fatal("independent check failed for parametric solution")
	}

	// Substitute concrete parameter values and verify against A, b.
	for _, tv := range []int64{-4, 0, 7} {
		x := DenseVec(3)
		for c := 0; c < 3; c++ {
			x[c].Add(s.Point[c], new(big.Rat).Mul(rat(tv), d[c]))
		}
		if !VerifyPoint(A, b, x) {
			t.Fatalf("parametric substitution t=%d does not satisfy system", tv)
		}
	}
}

func TestFreeVarsIncreasingOrder(t *testing.T) {
	// x0 + x3 = 2 ; x2 = 5  =>  free vars 1 and 3, in that order.
	A := NewSparseMatrix(2, 4)
	A.Set(0, 0, rat(1))
	A.Set(0, 3, rat(1))
	A.Set(1, 2, rat(1))
	b := []*big.Rat{rat(2), rat(5)}

	s, err := Solve(A, b)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != Parametric {
		t.Fatalf("status = %v, want Parametric", s.Status)
	}
	if len(s.FreeVars) != 2 || s.FreeVars[0] != 1 || s.FreeVars[1] != 3 {
		t.Fatalf("free vars = %v, want [1 3]", s.FreeVars)
	}
	if !Check(A, b, s) {
		t.Fatal("independent check failed")
	}
}

func TestInconsistentCertificate(t *testing.T) {
	// x + y = 1 ; 2x + 2y = 3  =>  2*eq0 - eq1 gives 0 = -1.
	A := NewSparseMatrix(2, 2)
	A.Set(0, 0, rat(1))
	A.Set(0, 1, rat(1))
	A.Set(1, 0, rat(2))
	A.Set(1, 1, rat(2))
	b := []*big.Rat{rat(1), rat(3)}

	s, err := Solve(A, b)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != Inconsistent {
		t.Fatalf("status = %v, want Inconsistent", s.Status)
	}
	if !VerifyCertificate(A, b, s.Certificate) {
		t.Fatalf("certificate %v does not prove inconsistency", s.Certificate)
	}
	if !Check(A, b, s) {
		t.Fatal("independent check failed for certificate")
	}
	// The certificate must combine the equations into 0 = nonzero constant:
	// here the only such combinations are k*(2, -1), i.e. c0 + 2*c1 = 0.
	c := s.Certificate
	sum := new(big.Rat).Add(c[0], new(big.Rat).Mul(c[1], rat(2)))
	if c[1].Sign() == 0 || sum.Sign() != 0 {
		t.Fatalf("certificate %v is not a multiple of (2, -1)", c)
	}
}

func TestCertificateRejectsForgery(t *testing.T) {
	A := NewSparseMatrix(2, 2)
	A.Set(0, 0, rat(1))
	A.Set(0, 1, rat(1))
	A.Set(1, 0, rat(2))
	A.Set(1, 1, rat(2))
	b := []*big.Rat{rat(1), rat(3)}

	if VerifyCertificate(A, b, []*big.Rat{rat(1), rat(1)}) {
		t.Fatal("accepted certificate with c·A != 0")
	}
	// c·A = 0 but c·b = 0 must be rejected.
	if VerifyCertificate(A, b, []*big.Rat{rat(0), rat(0)}) {
		t.Fatal("accepted zero certificate")
	}
	// Wrong length.
	if VerifyCertificate(A, b, []*big.Rat{rat(2)}) {
		t.Fatal("accepted wrong-length certificate")
	}
}

func TestEdgeCases(t *testing.T) {
	// No equations, 2 unknowns: everything is free.
	A := NewSparseMatrix(0, 2)
	s, err := Solve(A, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != Parametric || len(s.FreeVars) != 2 || s.FreeVars[0] != 0 || s.FreeVars[1] != 1 {
		t.Fatalf("got %+v, want parametric with free vars [0 1]", s)
	}
	if !Check(A, nil, s) {
		t.Fatal("check failed for empty system")
	}

	// No equations, no unknowns: unique empty solution.
	A0 := NewSparseMatrix(0, 0)
	s0, err := Solve(A0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s0.Status != Unique || len(s0.Point) != 0 {
		t.Fatalf("got %+v, want unique empty solution", s0)
	}
	if !Check(A0, nil, s0) {
		t.Fatal("check failed for 0x0 system")
	}

	// 0 = 1 with no variables: inconsistent.
	A1 := NewSparseMatrix(1, 0)
	s1, err := Solve(A1, []*big.Rat{rat(7)})
	if err != nil {
		t.Fatal(err)
	}
	if s1.Status != Inconsistent || !Check(A1, []*big.Rat{rat(7)}, s1) {
		t.Fatalf("got %+v, want inconsistent with valid certificate", s1)
	}

	// Dimension mismatch.
	if _, err := Solve(NewSparseMatrix(2, 2), []*big.Rat{rat(1)}); err == nil {
		t.Fatal("expected error for rhs length mismatch")
	}
}

func randRat(r *rand.Rand) *big.Rat {
	return big.NewRat(int64(r.Intn(7)-3), int64(r.Intn(3)+1))
}

func randomMatrix(r *rand.Rand, m, n int, density float64) *SparseMatrix {
	A := NewSparseMatrix(m, n)
	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			if r.Float64() < density {
				A.Set(i, j, randRat(r))
			}
		}
	}
	return A
}

func TestRandomConsistentSystems(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		m := 1 + r.Intn(8)
		n := 1 + r.Intn(8)
		A := randomMatrix(r, m, n, 0.5)
		x0 := DenseVec(n)
		for j := range x0 {
			x0[j] = randRat(r)
		}
		b := matVec(A, x0) // consistent by construction

		s, err := Solve(A, b)
		if err != nil {
			t.Fatal(err)
		}
		if s.Status == Inconsistent {
			t.Fatalf("trial %d: consistent system reported inconsistent", trial)
		}
		if !Check(A, b, s) {
			t.Fatalf("trial %d: independent check failed (status %v)", trial, s.Status)
		}
		if s.Status == Unique && !vecEq(s.Point, x0) {
			t.Fatalf("trial %d: unique solution %v != constructed %v", trial, s.Point, x0)
		}
		if s.Status == Parametric {
			// Spot-check a random parameter assignment by substitution.
			x := DenseVec(n)
			for _, d := range s.Directions {
				tv := randRat(r)
				for c := 0; c < n; c++ {
					x[c].Add(x[c], new(big.Rat).Mul(tv, d[c]))
				}
			}
			for c := 0; c < n; c++ {
				x[c].Add(x[c], s.Point[c])
			}
			if !VerifyPoint(A, b, x) {
				t.Fatalf("trial %d: parametric substitution failed", trial)
			}
		}
	}
}

func TestRandomInconsistentSystems(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	checked := 0
	for trial := 0; trial < 200; trial++ {
		n := 1 + r.Intn(6)
		m := 2 + r.Intn(5)
		A := randomMatrix(r, m, n, 0.5)
		// Duplicate the first row but perturb its rhs: inconsistent.
		for c := 0; c < n; c++ {
			A.Set(m-1, c, nil) // clear leftover random entries
		}
		for c, v := range A.Row(0) {
			A.Set(m-1, c, v)
		}
		b := make([]*big.Rat, m)
		for i := range b {
			b[i] = randRat(r)
		}
		b[m-1] = new(big.Rat).Add(b[0], big.NewRat(1, 2))

		s, err := Solve(A, b)
		if err != nil {
			t.Fatal(err)
		}
		if s.Status != Inconsistent {
			t.Fatalf("trial %d: status = %v, want Inconsistent", trial, s.Status)
		}
		if !Check(A, b, s) {
			t.Fatalf("trial %d: certificate failed independent verification", trial)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no inconsistent systems exercised")
	}
}

func TestNoFloatRankThreshold(t *testing.T) {
	// A pivot of 1/10^30 must be treated as nonzero exactly; any
	// float-based rank threshold would zero it out and misclassify.
	tiny := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil))
	A := NewSparseMatrix(2, 2)
	A.Set(0, 0, tiny)
	A.Set(1, 1, rat(1))
	b := []*big.Rat{tiny, rat(3)}

	s, err := Solve(A, b)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != Unique {
		t.Fatalf("status = %v, want Unique (tiny pivot must count)", s.Status)
	}
	if s.Point[0].Cmp(rat(1)) != 0 || s.Point[1].Cmp(rat(3)) != 0 {
		t.Fatalf("point = %v, want [1 3]", s.Point)
	}
	if !Check(A, b, s) {
		t.Fatal("independent check failed")
	}
}
