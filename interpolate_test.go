package netsec

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

func TestInterpolateUnitContradiction(t *testing.T) {
	// A forces x1=true, B forces x1=false over the single shared variable.
	a := CNF{{Lit(1)}}
	b := CNF{{-Lit(1)}}

	it, w, err := Interpolate(a, b, []int{1})
	if err != nil || w != nil {
		t.Fatalf("unexpected witness/error: %v %v", w, err)
	}
	if len(it.Terms) != 1 || len(it.Terms[0]) != 1 || it.Terms[0][0] != Lit(1) {
		t.Fatalf("interpolant = %v, want single minterm x1", it)
	}
	if !it.Eval(map[int]bool{1: true}) || it.Eval(map[int]bool{1: false}) {
		t.Fatal("interpolant truth table incorrect")
	}
	if r := Verify(a, b, []int{1}, it); !r.OK() {
		t.Fatalf("verification failed: %s", r.Failure)
	}
}

func TestInterpolatePrivateForcing(t *testing.T) {
	// Private p in A forces shared x1; B forbids x1.
	a := CNF{{-Lit(2), Lit(1)}, {Lit(2)}}
	b := CNF{{-Lit(1)}}

	it, _, err := Interpolate(a, b, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Terms) != 1 || it.Terms[0][0] != Lit(1) {
		t.Fatalf("interpolant = %v, want x1", it)
	}
	if r := Verify(a, b, []int{1}, it); !r.OK() {
		t.Fatalf("verification failed: %s", r.Failure)
	}
}

func TestInterpolateTwoShared(t *testing.T) {
	// A requires x1 or x2; B requires both to be false. Interpolant is the
	// disjunction of the three minterms satisfying x1 or x2.
	a := CNF{{Lit(1), Lit(2)}}
	b := CNF{{-Lit(1)}, {-Lit(2)}}

	it, _, err := Interpolate(a, b, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Terms) != 3 {
		t.Fatalf("interpolant = %v, want 3 minterms", it)
	}
	for mask := 0; mask < 4; mask++ {
		assign := maskAssignment([]int{1, 2}, mask)
		want := assign[1] || assign[2]
		if it.Eval(assign) != want {
			t.Fatalf("wrong truth table at %v", assign)
		}
	}
	if r := Verify(a, b, []int{1, 2}, it); !r.OK() {
		t.Fatalf("verification failed: %s", r.Failure)
	}
}

func TestInterpolateConstants(t *testing.T) {
	// A itself is unsatisfiable: the strongest shared formula is false.
	aUnsat := CNF{{Lit(1)}, {-Lit(1)}}
	bAny := CNF{{Lit(2)}}
	it, w, err := Interpolate(aUnsat, bAny, []int{2})
	if err != nil || w != nil {
		t.Fatalf("unexpected witness/error: %v %v", w, err)
	}
	if len(it.Terms) != 0 {
		t.Fatalf("interpolant = %v, want false", it)
	}

	// B itself is unsatisfiable: every shared assignment keeps A feasible,
	// so no B-feasible assignment exists. With A = x1 the strongest shared
	// formula is simply x1 itself.
	aFeasible := CNF{{Lit(1)}}
	bUnsat := CNF{{Lit(2)}, {-Lit(2)}}
	it2, _, err := Interpolate(aFeasible, bUnsat, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(it2.Terms) != 1 || it2.Terms[0][0] != Lit(1) {
		t.Fatalf("interpolant = %v, want x1", it2)
	}
}

func TestInterpolateConstantTrue(t *testing.T) {
	// B unsat and A imposes no constraint on the shared variable: the
	// strongest interpolant is the constant true, i.e. every minterm.
	a := CNF{{Lit(2)}}
	b := CNF{{Lit(1)}, {-Lit(1)}}
	it, _, err := Interpolate(a, b, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Terms) != 2 {
		t.Fatalf("interpolant = %v, want tautology over x1", it)
	}
	if r := Verify(a, b, []int{1}, it); !r.OK() {
		t.Fatalf("verification failed: %s", r.Failure)
	}
}

func TestInterpolateZeroShared(t *testing.T) {
	// With no shared vocabulary the interpolant can only be the constant
	// true or false. Here A is satisfiable while B alone is unsatisfiable:
	// the contradiction lives entirely in private space and A implies true.
	a := CNF{{Lit(1)}}
	b := CNF{{Lit(2)}, {-Lit(2)}}
	it, _, err := Interpolate(a, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Terms) != 1 || len(it.Terms[0]) != 0 {
		t.Fatalf("interpolant = %v, want constant true with empty vocabulary", it)
	}
	if r := Verify(a, b, nil, it); !r.OK() {
		t.Fatalf("verification failed: %s", r.Failure)
	}
}

func TestInterpolateZeroSharedFalse(t *testing.T) {
	// A itself unsatisfiable and no shared vocabulary: only false is implied.
	a := CNF{{Lit(1)}, {-Lit(1)}}
	b := CNF{{Lit(2)}}
	it, _, err := Interpolate(a, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Terms) != 0 {
		t.Fatalf("interpolant = %v, want constant false", it)
	}
	if r := Verify(a, b, nil, it); !r.OK() {
		t.Fatalf("verification failed: %s", r.Failure)
	}
}

func TestInterpolateSatisfiableReturnsWitness(t *testing.T) {
	a := CNF{{Lit(1), Lit(2)}}
	b := CNF{{-Lit(1), Lit(3)}}

	it, w, err := Interpolate(a, b, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if it != nil {
		t.Fatal("no interpolant may be produced for a satisfiable pair")
	}
	if w == nil {
		t.Fatal("satisfiable pair must return a witness")
	}
	if !w.Satisfies(a) || !w.Satisfies(b) {
		t.Fatalf("witness does not satisfy both CNFs: %v", w)
	}
}

// TestInterpolateSecurityScenario models an access-control decision. A is
// the system path constraints, B the attacker's exploit attempt; the
// interpolant is the condition that blocks every similar unreachable path.
func TestInterpolateSecurityScenario(t *testing.T) {
	const (
		authenticated = 1
		tokenValid    = 2
		roleAdmin     = 3
		ipTrusted     = 4
		exploitState  = 5 // private to the attacker model B
	)

	// Safe system: reaching the sensitive sink needs (auth or valid token),
	// admin role and a trusted source IP.
	a := CNF{
		{Lit(authenticated), Lit(tokenValid)},
		{Lit(roleAdmin)},
		{Lit(ipTrusted)},
	}

	// Attacker: unauthenticated, forged token, non-admin, external IP, yet
	// demands the exploit state and every safe precondition negated.
	b := CNF{
		{-Lit(authenticated)},
		{-Lit(tokenValid)},
		{-Lit(roleAdmin)},
		{-Lit(ipTrusted)},
		{Lit(exploitState)},
	}

	shared := []int{authenticated, tokenValid, roleAdmin, ipTrusted}
	it, w, err := Interpolate(a, b, shared)
	if err != nil || w != nil {
		t.Fatalf("unexpected witness/error: %v %v", w, err)
	}

	if r := Verify(a, b, shared, it); !r.OK() {
		t.Fatalf("verification failed: %s", r.Failure)
	}

	// The blocking condition must never mention the private exploit flag.
	for _, term := range it.Terms {
		for _, l := range term {
			if l.Var() == exploitState {
				t.Fatal("private exploit-state variable leaked into interpolant")
			}
		}
	}

	// Every satisfying assignment of the safe system is covered by the
	// interpolant, and no attacker-feasible assignment is.
	for mask := 0; mask < 1<<len(shared); mask++ {
		assign := maskAssignment(shared, mask)
		aSat, _ := indSolve(a, maskLiterals(shared, mask))
		bSat, _ := indSolve(b, maskLiterals(shared, mask))
		holds := it.Eval(assign)
		if aSat && !holds {
			t.Fatalf("safe path not covered by interpolant: %v", assign)
		}
		if bSat && holds {
			t.Fatalf("attacker path admitted by interpolant: %v", assign)
		}
	}
}

// TestInterpolateRandomProperties fuzzes random CNF pairs and independently
// confirms every returned interpolant satisfies both Craig properties, or
// that the satisfiable branch yields a genuine witness.
func TestInterpolateRandomProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(99))

	for seed := 0; seed < 250; seed++ {
		k := rng.Intn(6) // 0..5 shared variables
		shared := make([]int, k)
		for i := range shared {
			shared[i] = i + 1
		}
		privateA := 1 + rng.Intn(3)
		privateB := 1 + rng.Intn(3)

		// Strict variable partition: ids 1..k are shared, the next
		// privateA ids are A-private, and the rest are B-private. No
		// variable may occur on both sides without being declared shared.
		a := randomCNFRange(rng, 1, k+privateA, 2+rng.Intn(6), 1+rng.Intn(3))
		b := randomCNFRange(rng, k+privateA+1, k+privateA+privateB,
			2+rng.Intn(6), 1+rng.Intn(3))
		// Let B also reference the shared vocabulary so interpolation is
		// non-trivial.
		if k > 0 && rng.Intn(2) == 0 {
			sharedLits := maskLiterals(shared, rng.Intn(1<<uint(k)))
			b = append(b, Clause{sharedLits[rng.Intn(k)]})
		}

		it, w, err := Interpolate(a, b, shared)
		if err != nil {
			t.Fatalf("seed %d: unexpected error %v", seed, err)
		}

		jointSat, _ := indSolve(append(append(CNF{}, a...), b...), nil)
		if jointSat {
			if it != nil || w == nil || !w.Satisfies(a) || !w.Satisfies(b) {
				t.Fatalf("seed %d: bad witness branch", seed)
			}
			continue
		}
		if w != nil || it == nil {
			t.Fatalf("seed %d: unsat pair produced no interpolant", seed)
		}

		if r := Verify(a, b, shared, it); !r.OK() {
			t.Fatalf("seed %d: %s", seed, r.Failure)
		}

		total := 1
		for range shared {
			total *= 2
		}
		for mask := 0; mask < total; mask++ {
			assign := maskAssignment(shared, mask)
			assumptions := maskLiterals(shared, mask)
			aSat, _ := indSolve(a, assumptions)
			bSat, _ := indSolve(b, assumptions)
			// Under the jointly-unsat precondition the two feasibility
			// regions must be disjoint; a shared overlap would mean a bug in
			// either solver and invalidates this instance for interpolation.
			if aSat && bSat {
				t.Fatalf("seed %d: independent solver found joint model %v despite unsat precondition", seed, assign)
			}
			holds := it.Eval(assign)
			if aSat && !holds {
				t.Fatalf("seed %d: A does not imply I under %v", seed, assign)
			}
			if bSat && holds {
				t.Fatalf("seed %d: I does not contradict B under %v", seed, assign)
			}
		}
	}
}

// randomCNFRange builds a random CNF whose variable ids are drawn from the
// inclusive range [lo, hi]. An empty range yields no variables and is only
// valid when no clauses are requested.
func randomCNFRange(rng *rand.Rand, lo, hi, nClauses, width int) CNF {
	if hi < lo {
		return CNF{}
	}
	f := make(CNF, 0, nClauses)
	for i := 0; i < nClauses; i++ {
		w := 1 + rng.Intn(width)
		c := make(Clause, 0, w)
		for j := 0; j < w; j++ {
			v := lo + rng.Intn(hi-lo+1)
			l := Literal(v)
			if rng.Intn(2) == 0 {
				l = -l
			}
			c = append(c, l)
		}
		f = append(f, c)
	}
	return f
}

func TestInterpolateDeterministic(t *testing.T) {
	a := CNF{{Lit(1), Lit(2)}, {-Lit(1), Lit(3)}}
	b := CNF{{-Lit(2), -Lit(3)}, {Lit(1)}}
	shared := []int{1, 2, 3}

	first, _, err := Interpolate(a, b, shared)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		next, _, err := Interpolate(a, b, shared)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, next) {
			t.Fatal("interpolant construction is not deterministic")
		}
	}
}

func TestInterpolateValidation(t *testing.T) {
	f := CNF{{Lit(1)}}

	tooMany := make([]int, MaxShared+1)
	for i := range tooMany {
		tooMany[i] = i + 1
	}
	if _, _, err := Interpolate(f, f, tooMany); !errors.Is(err, ErrTooManyShared) {
		t.Fatalf("got %v, want ErrTooManyShared", err)
	}

	if _, _, err := Interpolate(f, f, []int{1, 1}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v, want ErrInvalidInput for duplicate", err)
	}
	if _, _, err := Interpolate(f, f, []int{0}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v, want ErrInvalidInput for zero variable", err)
	}
	if _, _, err := Interpolate(CNF{{Literal(0)}}, f, []int{1}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("got %v, want ErrInvalidInput for zero literal", err)
	}
}

func TestInterpolateMaxSharedBound(t *testing.T) {
	shared := make([]int, MaxShared)
	for i := range shared {
		shared[i] = i + 1
	}
	// A requires every shared variable true; B requires the first one false.
	a := make(CNF, 0, MaxShared)
	for _, v := range shared {
		a = append(a, Clause{Lit(v)})
	}
	b := CNF{{-Lit(1)}}

	it, _, err := Interpolate(a, b, shared)
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Terms) != 1 {
		t.Fatalf("interpolant = %v, want one all-true minterm", it)
	}
	r := Verify(a, b, shared, it)
	if !r.OK() || r.AssignmentCount != 1<<MaxShared {
		t.Fatalf("verification = %+v", r)
	}
}
