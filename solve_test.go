package linsolve

import (
	"math/big"
	"math/rand"
	"testing"
)

func rat(v int64) *big.Rat { return big.NewRat(v, 1) }

func frac(p, q int64) *big.Rat { return big.NewRat(p, q) }

func eq(rhs *big.Rat, coeffs map[int]*big.Rat) Equation {
	return Equation{Coeffs: coeffs, RHS: rhs}
}

func solveAndVerify(t *testing.T, sys *System) *Result {
	t.Helper()
	res, err := Solve(sys)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if err := Verify(sys, res); err != nil {
		t.Fatalf("Verify(%s): %v", res.Status, err)
	}
	return res
}

func TestUnique(t *testing.T) {
	// 2x + y = 5 ; x - y = 1  =>  x = 2, y = 1
	sys := &System{NumVars: 2, Equations: []Equation{
		eq(rat(5), map[int]*big.Rat{0: rat(2), 1: rat(1)}),
		eq(rat(1), map[int]*big.Rat{0: rat(1), 1: rat(-1)}),
	}}
	res := solveAndVerify(t, sys)
	if res.Status != Unique {
		t.Fatalf("status = %v, want Unique", res.Status)
	}
	if res.Solution[0].Cmp(rat(2)) != 0 || res.Solution[1].Cmp(rat(1)) != 0 {
		t.Fatalf("solution = (%s, %s), want (2, 1)",
			res.Solution[0].RatString(), res.Solution[1].RatString())
	}
}

func TestUniqueRational(t *testing.T) {
	// (1/2)x = 3 ; (2/3)y + (1/4)x = 2  =>  x = 6, y = 3/4
	sys := &System{NumVars: 2, Equations: []Equation{
		eq(rat(3), map[int]*big.Rat{0: frac(1, 2)}),
		eq(rat(2), map[int]*big.Rat{1: frac(2, 3), 0: frac(1, 4)}),
	}}
	res := solveAndVerify(t, sys)
	if res.Status != Unique {
		t.Fatalf("status = %v, want Unique", res.Status)
	}
	if res.Solution[0].Cmp(rat(6)) != 0 || res.Solution[1].Cmp(frac(3, 4)) != 0 {
		t.Fatalf("solution = (%s, %s), want (6, 3/4)",
			res.Solution[0].RatString(), res.Solution[1].RatString())
	}
}

func TestInfeasibleCertificate(t *testing.T) {
	// x + y = 1 ; x + y = 2 ; 2x + 2y = 5
	sys := &System{NumVars: 2, Equations: []Equation{
		eq(rat(1), map[int]*big.Rat{0: rat(1), 1: rat(1)}),
		eq(rat(2), map[int]*big.Rat{0: rat(1), 1: rat(1)}),
		eq(rat(5), map[int]*big.Rat{0: rat(2), 1: rat(2)}),
	}}
	res := solveAndVerify(t, sys)
	if res.Status != Infeasible {
		t.Fatalf("status = %v, want Infeasible", res.Status)
	}
	// Independently re-check the certificate: lambda*A == 0, lambda*b != 0.
	for c := 0; c < sys.NumVars; c++ {
		acc := new(big.Rat)
		for e := range sys.Equations {
			acc.Add(acc, new(big.Rat).Mul(res.Certificate[e], sys.Equations[e].Coeffs[c]))
		}
		if acc.Sign() != 0 {
			t.Fatalf("(cert*A)[%d] = %s, want 0", c, acc.RatString())
		}
	}
	constant := new(big.Rat)
	for e := range sys.Equations {
		constant.Add(constant, new(big.Rat).Mul(res.Certificate[e], sys.Equations[e].RHS))
	}
	if constant.Sign() == 0 {
		t.Fatal("cert*b = 0, certificate is not a contradiction")
	}
	t.Logf("certificate combines equations to 0 = %s", constant.RatString())
}

func TestParametric(t *testing.T) {
	// x + y + z = 3 ; x - y = 1  =>  free var z (index 2)
	// particular: z=0 => y = x-1, 2x-1=3 => x=2, y=1
	// direction for z: x = -1/2, y = -1/2, z = 1
	sys := &System{NumVars: 3, Equations: []Equation{
		eq(rat(3), map[int]*big.Rat{0: rat(1), 1: rat(1), 2: rat(1)}),
		eq(rat(1), map[int]*big.Rat{0: rat(1), 1: rat(-1)}),
	}}
	res := solveAndVerify(t, sys)
	if res.Status != Parametric {
		t.Fatalf("status = %v, want Parametric", res.Status)
	}
	if len(res.FreeVars) != 1 || res.FreeVars[0] != 2 {
		t.Fatalf("FreeVars = %v, want [2]", res.FreeVars)
	}
	wantPart := []*big.Rat{rat(2), rat(1), rat(0)}
	for i := range wantPart {
		if res.Particular[i].Cmp(wantPart[i]) != 0 {
			t.Fatalf("Particular[%d] = %s, want %s", i,
				res.Particular[i].RatString(), wantPart[i].RatString())
		}
	}
	wantDir := []*big.Rat{frac(-1, 2), frac(-1, 2), rat(1)}
	for i := range wantDir {
		if res.Directions[0][i].Cmp(wantDir[i]) != 0 {
			t.Fatalf("Directions[0][%d] = %s, want %s", i,
				res.Directions[0][i].RatString(), wantDir[i].RatString())
		}
	}
}

func TestParametricFreeVarsIncreasing(t *testing.T) {
	// Only middle variable constrained: x1 = 5. Free vars: 0 and 2.
	sys := &System{NumVars: 3, Equations: []Equation{
		eq(rat(5), map[int]*big.Rat{1: rat(1)}),
	}}
	res := solveAndVerify(t, sys)
	if res.Status != Parametric {
		t.Fatalf("status = %v, want Parametric", res.Status)
	}
	if len(res.FreeVars) != 2 || res.FreeVars[0] != 0 || res.FreeVars[1] != 2 {
		t.Fatalf("FreeVars = %v, want [0 2]", res.FreeVars)
	}
	// Directions[i] must correspond to FreeVars[i].
	if res.Directions[0][0].Cmp(rat(1)) != 0 || res.Directions[1][2].Cmp(rat(1)) != 0 {
		t.Fatal("directions do not match free variable order")
	}
}

func TestTinyCoefficientsNoFloatThreshold(t *testing.T) {
	// A floating point rank threshold could treat 1e-30 as zero and report
	// a parametric solution; exact arithmetic must find the unique one.
	pow30 := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	tiny := new(big.Rat).SetFrac(big.NewInt(1), pow30)
	big_ := new(big.Rat).SetInt(pow30)
	sys := &System{NumVars: 2, Equations: []Equation{
		eq(rat(1), map[int]*big.Rat{0: rat(1)}),
		eq(rat(1), map[int]*big.Rat{1: tiny}),
	}}
	res := solveAndVerify(t, sys)
	if res.Status != Unique {
		t.Fatalf("status = %v, want Unique", res.Status)
	}
	if res.Solution[1].Cmp(big_) != 0 {
		t.Fatalf("x1 = %s, want 10^30", res.Solution[1].RatString())
	}
}

func TestEdgeCases(t *testing.T) {
	// No equations at all: everything is free.
	res := solveAndVerify(t, &System{NumVars: 2})
	if res.Status != Parametric || len(res.FreeVars) != 2 {
		t.Fatalf("empty system: status=%v FreeVars=%v", res.Status, res.FreeVars)
	}
	// 0 = 0 is consistent.
	res = solveAndVerify(t, &System{NumVars: 1, Equations: []Equation{
		eq(rat(0), map[int]*big.Rat{}),
	}})
	if res.Status != Parametric {
		t.Fatalf("0=0: status = %v, want Parametric", res.Status)
	}
	// 0 = 7 is infeasible with certificate [1].
	res = solveAndVerify(t, &System{NumVars: 1, Equations: []Equation{
		eq(rat(7), map[int]*big.Rat{}),
	}})
	if res.Status != Infeasible {
		t.Fatalf("0=7: status = %v, want Infeasible", res.Status)
	}
	// Zero variables, consistent empty system.
	res = solveAndVerify(t, &System{NumVars: 0})
	if res.Status != Unique {
		t.Fatalf("0 vars 0 eqs: status = %v, want Unique", res.Status)
	}
}

// randomSystem builds a random sparse rational system with numEqs equations
// over numVars variables. If consistent, a random x* is chosen and b = A x*.
// If inconsistent, the last equation is a duplicate of the first with a
// different right-hand side.
func randomSystem(rnd *rand.Rand, numVars, numEqs int, consistent bool) *System {
	sys := &System{NumVars: numVars}
	A := make([]map[int]*big.Rat, numEqs)
	for i := 0; i < numEqs; i++ {
		A[i] = make(map[int]*big.Rat)
		for c := 0; c < numVars; c++ {
			if rnd.Intn(3) == 0 { // sparse: ~1/3 density
				A[i][c] = frac(int64(rnd.Intn(9)-4), int64(rnd.Intn(4)+1))
				if A[i][c].Sign() == 0 {
					delete(A[i], c)
				}
			}
		}
	}
	b := make([]*big.Rat, numEqs)
	if consistent {
		x := make([]*big.Rat, numVars)
		for c := range x {
			x[c] = frac(int64(rnd.Intn(7)-3), int64(rnd.Intn(3)+1))
		}
		for i := range A {
			b[i] = dot(A[i], x)
		}
	} else {
		for i := range A {
			b[i] = frac(int64(rnd.Intn(11)-5), 1)
		}
		if numEqs >= 2 && numVars >= 1 {
			// Force a contradiction: copy row 0, perturb its rhs.
			for c, v := range A[0] {
				A[numEqs-1][c] = new(big.Rat).Set(v)
			}
			for c := range A[numEqs-1] {
				if _, ok := A[0][c]; !ok {
					delete(A[numEqs-1], c)
				}
			}
			b[numEqs-1] = new(big.Rat).Add(b[0], rat(1))
		}
	}
	for i := range A {
		sys.Equations = append(sys.Equations, Equation{Coeffs: A[i], RHS: b[i]})
	}
	return sys
}

func TestRandomConsistent(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		numVars := 1 + rnd.Intn(8)
		numEqs := 1 + rnd.Intn(10)
		sys := randomSystem(rnd, numVars, numEqs, true)
		res := solveAndVerify(t, sys)
		if res.Status == Infeasible {
			t.Fatalf("trial %d: consistent system reported infeasible", trial)
		}
		if res.Status == Unique && len(res.FreeVars) != 0 {
			t.Fatalf("trial %d: unique result with free vars", trial)
		}
	}
}

func TestRandomInfeasible(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	found := 0
	for trial := 0; trial < 200; trial++ {
		numVars := 1 + rnd.Intn(6)
		numEqs := 2 + rnd.Intn(6)
		sys := randomSystem(rnd, numVars, numEqs, false)
		res := solveAndVerify(t, sys)
		if res.Status == Infeasible {
			found++
		}
	}
	if found == 0 {
		t.Fatal("no infeasible system generated; test is vacuous")
	}
	t.Logf("%d/200 systems infeasible, all certificates verified", found)
}

func TestVerifyRejectsGarbage(t *testing.T) {
	sys := &System{NumVars: 2, Equations: []Equation{
		eq(rat(5), map[int]*big.Rat{0: rat(2), 1: rat(1)}),
		eq(rat(1), map[int]*big.Rat{0: rat(1), 1: rat(-1)}),
	}}
	// Wrong unique solution.
	bad := &Result{Status: Unique, Solution: []*big.Rat{rat(1), rat(1)}}
	if err := Verify(sys, bad); err == nil {
		t.Fatal("Verify accepted a wrong unique solution")
	}
	// Bogus certificate on a feasible system.
	bad = &Result{Status: Infeasible, Certificate: []*big.Rat{rat(1), rat(1)}}
	if err := Verify(sys, bad); err == nil {
		t.Fatal("Verify accepted a bogus infeasibility certificate")
	}
	// Direction that is not in the null space.
	bad = &Result{Status: Parametric,
		Particular: []*big.Rat{rat(2), rat(1)},
		Directions: [][]*big.Rat{{rat(1), rat(1)}},
		FreeVars:   []int{0},
	}
	if err := Verify(sys, bad); err == nil {
		t.Fatal("Verify accepted a bad parametric solution")
	}
	// Free variables not increasing.
	bad = &Result{Status: Parametric,
		Particular: []*big.Rat{rat(0), rat(0)},
		Directions: [][]*big.Rat{{rat(0), rat(1)}, {rat(1), rat(0)}},
		FreeVars:   []int{1, 0},
	}
	if err := Verify(sys, bad); err == nil {
		t.Fatal("Verify accepted non-increasing free variables")
	}
}
