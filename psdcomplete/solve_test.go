package psdcomplete

import (
	"math/big"
	"math/rand"
	"testing"
)

func mustSpec(t *testing.T, n int) *Spec {
	t.Helper()
	s, err := NewSpec(n)
	if err != nil {
		t.Fatalf("NewSpec: %v", err)
	}
	return s
}

func mustFix(t *testing.T, s *Spec, i, j int, v *big.Rat) {
	t.Helper()
	if err := s.Fix(i, j, v); err != nil {
		t.Fatalf("Fix(%d,%d): %v", i, j, err)
	}
}

func mustCands(t *testing.T, s *Spec, i, j int, vals ...*big.Rat) {
	t.Helper()
	if err := s.SetCandidates(i, j, vals...); err != nil {
		t.Fatalf("SetCandidates(%d,%d): %v", i, j, err)
	}
}

func mustSolve(t *testing.T, s *Spec) *Result {
	t.Helper()
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	r, err := Solve(s, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return r
}

// Classic 2x2: [[1, x], [x, 1]] is PSD iff |x| <= 1.
func TestTwoByTwo(t *testing.T) {
	s := mustSpec(t, 2)
	mustFix(t, s, 0, 0, Rat(1))
	mustFix(t, s, 1, 1, Rat(1))
	mustCands(t, s, 0, 1, Rat(-2), Frac(-1, 2), Rat(0), Frac(1, 2), Rat(2))

	r := mustSolve(t, s)
	if !r.OK {
		t.Fatal("expected feasible")
	}
	if got := r.Matrix[0][1]; got.Cmp(Frac(-1, 2)) != 0 {
		t.Fatalf("a12 = %s, want -1/2", got)
	}
	if r.Trace.Cmp(Rat(2)) != 0 {
		t.Fatalf("trace = %s, want 2", r.Trace)
	}
	if len(r.Certificate.Minors) != 3 { // 2^2 - 1 principal minors
		t.Fatalf("certificate has %d minors, want 3", len(r.Certificate.Minors))
	}
}

// Same trace for x=-1 and x=1; lexicographically smallest upper triangle
// (a11, a12, a22) must win, i.e. x = -1.
func TestLexTieBreak(t *testing.T) {
	s := mustSpec(t, 2)
	mustFix(t, s, 0, 0, Rat(1))
	mustFix(t, s, 1, 1, Rat(1))
	mustCands(t, s, 0, 1, Rat(1), Rat(-1))

	r := mustSolve(t, s)
	if !r.OK {
		t.Fatal("expected feasible")
	}
	if got := r.Matrix[0][1]; got.Cmp(Rat(-1)) != 0 {
		t.Fatalf("a12 = %s, want -1 (lexicographic tie-break)", got)
	}
}

// Diagonal candidates drive the trace; off-diagonal zero keeps everything PSD.
func TestTraceMinimization(t *testing.T) {
	s := mustSpec(t, 2)
	mustCands(t, s, 0, 0, Rat(0), Rat(1), Rat(2))
	mustCands(t, s, 1, 1, Rat(0), Rat(3))
	mustFix(t, s, 0, 1, Rat(0))

	r := mustSolve(t, s)
	if !r.OK {
		t.Fatal("expected feasible")
	}
	if r.Trace.Cmp(Rat(0)) != 0 {
		t.Fatalf("trace = %s, want 0", r.Trace)
	}
	if r.Matrix[0][0].Sign() != 0 || r.Matrix[1][1].Sign() != 0 {
		t.Fatalf("expected zero diagonal, got %s, %s", r.Matrix[0][0], r.Matrix[1][1])
	}
}

// Block diagonal [[1,x],[x,1]] + [[1,y],[y,1]]: the full determinant is
// (1-x^2)(1-y^2) >= 0 and the diagonal is positive even for x=y=2, so a
// checker that only looks at the diagonal and the full determinant would
// wrongly accept x=y=2. All-principal-minors catches it.
func TestDetAndDiagInsufficient(t *testing.T) {
	s := mustSpec(t, 4)
	for i := 0; i < 4; i++ {
		mustFix(t, s, i, i, Rat(1))
	}
	mustFix(t, s, 0, 2, Rat(0))
	mustFix(t, s, 0, 3, Rat(0))
	mustFix(t, s, 1, 2, Rat(0))
	mustFix(t, s, 1, 3, Rat(0))
	cands := []*big.Rat{Rat(0), Rat(1), Rat(2), Rat(3)}
	mustCands(t, s, 0, 1, cands...)
	mustCands(t, s, 2, 3, cands...)

	r := mustSolve(t, s)
	if !r.OK {
		t.Fatal("expected feasible")
	}
	if r.Matrix[0][1].Sign() != 0 || r.Matrix[2][3].Sign() != 0 {
		t.Fatalf("want a12=a34=0, got %s and %s", r.Matrix[0][1], r.Matrix[2][3])
	}

	// Sanity: the naive "diag >= 0 and det >= 0" test accepts x = y = 2.
	m := NewMatrix(4)
	for i := 0; i < 4; i++ {
		m[i][i] = Rat(1)
	}
	m[0][1], m[1][0] = Rat(2), Rat(2)
	m[2][3], m[3][2] = Rat(2), Rat(2)
	if Det(m).Sign() < 0 {
		t.Fatal("test setup: full determinant should be non-negative")
	}
	if ok, bad := IsPSDMinors(m); ok {
		t.Fatal("minors criterion must reject x=y=2")
	} else if len(bad.Subset) != 2 {
		t.Fatalf("expected a negative 2x2 principal minor, got subset %v", bad.Subset)
	}
	if VerifyLDL(m).PSD {
		t.Fatal("LDL verifier must reject x=y=2")
	}
}

// Fully fixed, indefinite matrix: no feasible completion.
func TestInfeasibleFixed(t *testing.T) {
	s := mustSpec(t, 2)
	mustFix(t, s, 0, 0, Rat(1))
	mustFix(t, s, 1, 1, Rat(1))
	mustFix(t, s, 0, 1, Rat(2))

	r := mustSolve(t, s)
	if r.OK {
		t.Fatal("expected infeasible")
	}
	if r.PruneTotal == 0 {
		t.Fatal("expected pruning evidence for the infeasible instance")
	}
	if r.PruneLog[0].Kind != PruneMinorNegative {
		t.Fatalf("expected negative-minor evidence, got %v", r.PruneLog[0].Kind)
	}
}

// Every candidate branch is cut by a negative principal minor; the evidence
// must cover all 2 combinations.
func TestInfeasibleAllBranchesPruned(t *testing.T) {
	s := mustSpec(t, 2)
	mustFix(t, s, 0, 0, Rat(1))
	mustFix(t, s, 1, 1, Rat(1))
	mustCands(t, s, 0, 1, Rat(2), Rat(3))

	r := mustSolve(t, s)
	if r.OK {
		t.Fatal("expected infeasible")
	}
	if r.PruneTotal != 2 {
		t.Fatalf("PruneTotal = %d, want 2 (one per candidate)", r.PruneTotal)
	}
	for _, ev := range r.PruneLog {
		if ev.Kind != PruneMinorNegative || ev.Minor.Sign() >= 0 {
			t.Fatalf("bad prune event: %v", ev)
		}
	}
}

func TestComboLimit(t *testing.T) {
	s := mustSpec(t, 4)
	c := make([]*big.Rat, 11)
	for i := range c {
		c[i] = Rat(int64(i))
	}
	// 10 upper-triangular positions, 11 candidates each => 11^10 combos.
	for i := 0; i < 4; i++ {
		for j := i; j < 4; j++ {
			mustCands(t, s, i, j, c...)
		}
	}
	if err := s.Validate(); err == nil {
		t.Fatal("expected combination-limit error")
	}
}

func TestCertificateComplete(t *testing.T) {
	s := mustSpec(t, 3)
	mustFix(t, s, 0, 0, Rat(2))
	mustCands(t, s, 0, 1, Rat(-1), Rat(0), Rat(1))
	mustCands(t, s, 0, 2, Frac(1, 2), Rat(1))
	mustCands(t, s, 1, 1, Rat(1), Rat(2))
	mustCands(t, s, 1, 2, Rat(0), Frac(-1, 3))
	mustCands(t, s, 2, 2, Rat(1), Rat(3))

	r := mustSolve(t, s)
	if !r.OK {
		t.Fatal("expected feasible")
	}
	if got, want := len(r.Certificate.Minors), (1<<3)-1; got != want {
		t.Fatalf("certificate has %d minors, want %d", got, want)
	}
	for _, mc := range r.Certificate.Minors {
		if mc.Det.Sign() < 0 {
			t.Fatalf("certificate contains negative minor on %v", mc.Subset)
		}
	}
	if !r.Certificate.Valid() {
		t.Fatal("certificate must validate (minors and LDL agree)")
	}
	// Independent re-check of the returned matrix.
	if ok, _ := IsPSDMinors(r.Matrix); !ok {
		t.Fatal("returned matrix fails the minors criterion")
	}
	if !VerifyLDL(r.Matrix).PSD {
		t.Fatal("returned matrix fails the LDL criterion")
	}
}

// Randomized cross-check: the pruning solver must agree with brute-force
// exhaustive enumeration on small instances.
func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261008))
	pool := []*big.Rat{Rat(-2), Frac(-3, 2), Rat(-1), Frac(-1, 2), Rat(0), Frac(1, 2), Rat(1), Frac(3, 2), Rat(2)}
	for trial := 0; trial < 400; trial++ {
		n := 1 + rng.Intn(4)
		s := mustSpec(t, n)
		for i := 0; i < n; i++ {
			for j := i; j < n; j++ {
				if rng.Float64() < 0.35 {
					mustFix(t, s, i, j, pool[rng.Intn(len(pool))])
					continue
				}
				k := 1 + rng.Intn(3)
				vals := make([]*big.Rat, k)
				for c := range vals {
					vals[c] = pool[rng.Intn(len(pool))]
				}
				mustCands(t, s, i, j, vals...)
			}
		}
		if s.ComboCount() > 20000 {
			trial--
			continue
		}
		if err := s.Validate(); err != nil {
			t.Fatalf("trial %d: Validate: %v", trial, err)
		}
		fast, err := Solve(s, nil)
		if err != nil {
			t.Fatalf("trial %d: Solve: %v", trial, err)
		}
		brute, err := SolveBrute(s)
		if err != nil {
			t.Fatalf("trial %d: SolveBrute: %v", trial, err)
		}
		if fast.OK != brute.OK {
			t.Fatalf("trial %d: feasibility mismatch: Solve=%v SolveBrute=%v", trial, fast.OK, brute.OK)
		}
		if !fast.OK {
			continue
		}
		if fast.Trace.Cmp(brute.Trace) != 0 {
			t.Fatalf("trial %d: trace mismatch: %s vs %s", trial, fast.Trace, brute.Trace)
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if fast.Matrix[i][j].Cmp(brute.Matrix[i][j]) != 0 {
					t.Fatalf("trial %d: matrix mismatch at (%d,%d): %s vs %s",
						trial, i, j, fast.Matrix[i][j], brute.Matrix[i][j])
				}
			}
		}
		if !fast.Certificate.Valid() {
			t.Fatalf("trial %d: certificate invalid", trial)
		}
	}
}

// Randomized cross-check of the two independent PSD tests themselves.
func TestMinorsVsLDL(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	pool := []*big.Rat{Rat(-2), Rat(-1), Frac(-1, 2), Rat(0), Frac(1, 2), Rat(1), Rat(2)}
	for trial := 0; trial < 3000; trial++ {
		n := 1 + rng.Intn(4)
		m := NewMatrix(n)
		for i := 0; i < n; i++ {
			for j := i; j < n; j++ {
				v := pool[rng.Intn(len(pool))]
				m[i][j] = v
				m[j][i] = v
			}
		}
		minorsOK, _ := IsPSDMinors(m)
		ldlOK := VerifyLDL(m).PSD
		if minorsOK != ldlOK {
			t.Fatalf("trial %d: minors=%v but LDL=%v for %v", trial, minorsOK, ldlOK, m)
		}
	}
}

// Exact rational arithmetic: 1/3-scale entries must stay exact.
func TestExactRationals(t *testing.T) {
	s := mustSpec(t, 2)
	mustCands(t, s, 0, 0, Frac(1, 3), Frac(2, 3))
	mustCands(t, s, 1, 1, Frac(1, 3), Frac(2, 3))
	mustCands(t, s, 0, 1, Frac(-1, 3), Frac(1, 3))

	r := mustSolve(t, s)
	if !r.OK {
		t.Fatal("expected feasible")
	}
	// Minimal trace 2/3 with a11=a22=1/3 forces |a12| <= 1/3; both signs
	// are feasible, lexicographic order picks -1/3.
	if r.Trace.Cmp(Frac(2, 3)) != 0 {
		t.Fatalf("trace = %s, want 2/3", r.Trace)
	}
	if r.Matrix[0][1].Cmp(Frac(-1, 3)) != 0 {
		t.Fatalf("a12 = %s, want -1/3", r.Matrix[0][1])
	}
}
