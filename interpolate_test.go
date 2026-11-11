package netsec

import (
	"math/rand"
	"testing"
)

// bruteForceCheck independently evaluates the interpolation properties by
// iterating the full truth table over every variable in use (shared and
// private), without calling the package's own enumeration or solver helpers.
func bruteForceCheck(t *testing.T, a, b CNF, shared []int, f Formula) {
	t.Helper()

	vars := map[int]struct{}{}
	for _, v := range a.Vars() {
		vars[v] = struct{}{}
	}
	for _, v := range b.Vars() {
		vars[v] = struct{}{}
	}
	all := make([]int, 0, len(vars))
	for v := range vars {
		all = append(all, v)
	}

	sharedSet := map[int]struct{}{}
	for _, v := range shared {
		sharedSet[v] = struct{}{}
	}
	for _, v := range Vars(f) {
		if _, ok := sharedSet[v]; !ok {
			t.Fatalf("interpolant uses non-shared variable x%d", v)
		}
	}

	env := make(map[int]bool, len(all))
	for mask := 0; mask < 1<<len(all); mask++ {
		for i, v := range all {
			env[v] = mask&(1<<i) != 0
		}
		aVal, aOK := a.Eval(env)
		bVal, bOK := b.Eval(env)
		if !aOK || !bOK {
			t.Fatal("partial evaluation over full variable set")
		}
		fVal := f.Eval(env)
		if aVal && !fVal {
			t.Fatalf("implication violated: A true but interpolant false at %v", env)
		}
		if bVal && fVal {
			t.Fatalf("contradiction violated: B and interpolant both true at %v", env)
		}
	}
}

func TestInterpolateSimpleContradiction(t *testing.T) {
	x := MakeLit(0)
	a := CNF{{x}}
	b := CNF{{x.Neg()}}

	res, err := Interpolate(a, b, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if res.SAT {
		t.Fatal("expected UNSAT joint conjunction")
	}
	if !res.Verification.Passed {
		t.Fatalf("verification failed: %s", res.Verification.Failure)
	}
	if got := res.Interpolant.String(); got != "x0" && got != "(x0)" {
		t.Fatalf("expected interpolant x0, got %s", got)
	}
	bruteForceCheck(t, a, b, []int{0}, res.Interpolant)
}

func TestInterpolatePrivateVariables(t *testing.T) {
	// Shared: x0. Private: x1 (A side), x2 (B side).
	// A: (x0 ∨ x1) & (!x0 ∨ x1) forces x1, leaves x0 free.
	// B: (x0 ∨ !x2) & (!x0 ∨ !x2) forces !x2 and is consistent with x0.
	// Joint is SAT here, so this case must return a witness.
	x0, x1, x2 := MakeLit(0), MakeLit(1), MakeLit(2)
	a := CNF{{x0, x1}, {x0.Neg(), x1}}
	b := CNF{{x0, x2.Neg()}, {x0.Neg(), x2.Neg()}}
	res, err := Interpolate(a, b, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if !res.SAT {
		t.Fatal("expected SAT and a witness, not a fabricated proof")
	}
	if res.Interpolant != nil {
		t.Fatal("SAT result must not carry an interpolant")
	}
	joint := append(append(CNF{}, a...), b...)
	if !VerifyWitness(joint, res.Witness) {
		t.Fatalf("witness %v does not satisfy joint CNF", res.Witness)
	}
}

func TestInterpolateUnsatWithPrivateVars(t *testing.T) {
	// A forces x0=true through a private variable; B forces x0=false.
	// A: (x0 ∨ x1) & (x0 ∨ !x1)  -> x0
	// B: (!x0 ∨ x2) & (!x0 ∨ !x2) -> !x0
	x0, x1, x2 := MakeLit(0), MakeLit(1), MakeLit(2)
	a := CNF{{x0, x1}, {x0, x1.Neg()}}
	b := CNF{{x0.Neg(), x2}, {x0.Neg(), x2.Neg()}}

	res, err := Interpolate(a, b, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if res.SAT {
		t.Fatal("expected UNSAT")
	}
	if !res.Verification.Passed {
		t.Fatalf("verification failed: %s", res.Verification.Failure)
	}
	bruteForceCheck(t, a, b, []int{0}, res.Interpolant)
	if !res.Interpolant.Eval(map[int]bool{0: true}) {
		t.Fatal("interpolant must hold at x0=true")
	}
	if res.Interpolant.Eval(map[int]bool{0: false}) {
		t.Fatal("interpolant must be false at x0=false")
	}
}

func TestInterpolateNoSharedVars(t *testing.T) {
	// A is independently UNSAT while B is anything; no shared vocabulary.
	x := MakeLit(0)
	a := CNF{{x}, {x.Neg()}}
	b := CNF{{MakeLit(1)}}

	res, err := Interpolate(a, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.SAT {
		t.Fatal("joint conjunction must be UNSAT")
	}
	if got := res.Interpolant.String(); got != "false" {
		t.Fatalf("expected constant false interpolant, got %s", got)
	}
	bruteForceCheck(t, a, b, nil, res.Interpolant)
}

func TestInterpolateConstantTrue(t *testing.T) {
	// A talks only about its private variable and leaves the shared x0 free.
	// B is independently unsatisfiable through a private variable while it
	// also mentions x0, so the joint conjunction is UNSAT and every shared
	// assignment remains A-feasible: the construction simplifies to true.
	x0, x1, x2 := MakeLit(0), MakeLit(1), MakeLit(2)
	a := CNF{{x1}}
	b := CNF{
		{x0.Neg(), x2},
		{x0.Neg(), x2.Neg()},
		{x0, x2},
		{x0, x2.Neg()},
	}

	res, err := Interpolate(a, b, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if res.SAT || !res.Verification.Passed {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got := res.Interpolant.String(); got != "true" {
		t.Fatalf("expected constant true interpolant, got %s", got)
	}
	bruteForceCheck(t, a, b, []int{0}, res.Interpolant)
}

func TestMissingSharedDeclarationRejected(t *testing.T) {
	x := MakeLit(0)
	a := CNF{{x}}
	b := CNF{{x.Neg()}}
	if _, err := Interpolate(a, b, nil); err == nil {
		t.Fatal("expected error when a common variable is not declared shared")
	}
}

func TestTooManySharedVarsRejected(t *testing.T) {
	shared := make([]int, MaxSharedVars+1)
	for i := range shared {
		shared[i] = i
	}
	if _, err := Interpolate(CNF{}, CNF{}, shared); err == nil {
		t.Fatal("expected error beyond MaxSharedVars")
	}
}

func TestRandomRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for trial := 0; trial < 200; trial++ {
		nShared := 1 + rng.Intn(4)
		nPrivate := rng.Intn(3)
		nVars := nShared + nPrivate
		shared := make([]int, nShared)
		for i := range shared {
			shared[i] = i
		}

		// Private variables (indices >= nShared) only occur in A, so the
		// declared shared set covers every variable used by both CNFs.
		a := randomCNF(rng, nVars, 2+rng.Intn(4))
		b := randomCNF(rng, nShared, 2+rng.Intn(4))

		res, err := Interpolate(a, b, shared)
		if err != nil {
			t.Fatalf("trial %d: unexpected error: %v", trial, err)
		}
		joint := append(append(CNF{}, a...), b...)
		if res.SAT {
			if !VerifyWitness(joint, res.Witness) {
				t.Fatalf("trial %d: invalid witness", trial)
			}
			continue
		}
		if !res.Verification.Passed {
			t.Fatalf("trial %d: %s", trial, res.Verification.Failure)
		}
		bruteForceCheck(t, a, b, shared, res.Interpolant)
		if res.Enumerations != 1<<nShared {
			t.Fatalf("trial %d: enumerated %d, want %d",
				trial, res.Enumerations, 1<<nShared)
		}
	}
}

func TestTwelveSharedVarsBound(t *testing.T) {
	// A forces the first shared variable true; B forces it false. Remaining
	// shared variables stay unconstrained, exercising all 4096 assignments.
	shared := make([]int, MaxSharedVars)
	for i := range shared {
		shared[i] = i
	}
	x := MakeLit(0)
	a := CNF{{x}}
	b := CNF{{x.Neg()}}

	res, err := Interpolate(a, b, shared)
	if err != nil {
		t.Fatal(err)
	}
	if res.SAT || res.Enumerations != 1<<MaxSharedVars {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !res.Verification.Passed {
		t.Fatalf("verification failed: %s", res.Verification.Failure)
	}
}

func randomCNF(rng *rand.Rand, nvars, nclauses int) CNF {
	cnf := make(CNF, nclauses)
	for i := range cnf {
		width := 1 + rng.Intn(3)
		clause := make(Clause, width)
		for j := range clause {
			l := MakeLit(rng.Intn(nvars))
			if rng.Intn(2) == 1 {
				l = l.Neg()
			}
			clause[j] = l
		}
		cnf[i] = clause
	}
	return cnf
}
