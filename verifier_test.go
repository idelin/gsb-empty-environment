package netsec

import "testing"

// assertProgram raises ExcAssertion exactly when the secret is zero, while
// every non-zero secret returns the same fixed constant. This is the textbook
// secret-dependent exception path from the task statement.
func assertProgram() *Program {
	return &Program{
		Vars: []Variable{
			{Name: "secret", Visibility: Secret, Bits: 3},
			{Name: "fixed", Visibility: Public, Bits: 8, Init: 42},
		},
		Code: []Instr{
			{Op: OpAssert, Src: 0},
			{Op: OpReturn, Src: 1},
		},
	}
}

func TestVerifyDetectsSecretDrivenException(t *testing.T) {
	res, err := Verify(assertProgram(), nil, nil, 0)
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}
	if res.Verdict != LeakFound || res.Counterexample == nil {
		t.Fatalf("expected LeakFound, got %v", res.Verdict)
	}
	ce := res.Counterexample
	if ce.ObservationA.Exc == ce.ObservationB.Exc {
		t.Fatalf("expected differing exceptions, both %v", ce.ObservationA.Exc)
	}
	// One side must be the clean fixed return, the other the assertion fault.
	if !((ce.ObservationA.Exc == ExcAssertion && ce.ObservationB.Exc == ExcNone) ||
		(ce.ObservationB.Exc == ExcAssertion && ce.ObservationA.Exc == ExcNone)) {
		t.Fatalf("unexpected exception pair %v / %v", ce.ObservationA.Exc, ce.ObservationB.Exc)
	}
	if ce.ObservationB.Return != 42 && ce.ObservationA.Return != 42 {
		t.Fatalf("clean run should expose the fixed return value 42")
	}

	// Both counterexample sequences must replay deterministically.
	for _, tr := range []*Trace{ce.TraceA, ce.TraceB} {
		if _, err := Replay(assertProgram(), tr); err != nil {
			t.Fatalf("counterexample trace failed to replay: %v", err)
		}
	}
}

func TestVerifySecretIndependentWhenExceptionIsPublicDriven(t *testing.T) {
	// divisor is public and fixed for the whole verification, so a zero
	// divisor faults identically for every secret.
	res, err := Verify(divisionProgram(), Input{1: 0}, nil, 0)
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}
	if res.Verdict != SecretIndependent || !res.Complete {
		t.Fatalf("expected complete SecretIndependent, got %v complete=%v", res.Verdict, res.Complete)
	}
	if res.Explored != res.DomainSize || res.DomainSize != 16 {
		t.Fatalf("expected full domain of 16, explored=%d domain=%d", res.Explored, res.DomainSize)
	}
}

func TestVerifyDetectsHostCallSequenceLeak(t *testing.T) {
	// When the secret is non-zero an extra host call happens, changing the
	// observable host-call sequence even though the return value is fixed.
	p := &Program{
		Vars: []Variable{
			{Name: "secret", Visibility: Secret, Bits: 2},
			{Name: "tmp", Visibility: Public, Bits: 8},
			{Name: "fixed", Visibility: Public, Bits: 8, Init: 7},
		},
		Code: []Instr{
			{Op: OpBranch, Src: 0, Target: 2},
			{Op: OpReturn, Src: 2},
			{Op: OpHost, Dst: 1, Src: 0, HostID: 1},
			{Op: OpReturn, Src: 2},
		},
	}
	host := HostFunc(func(id uint8, arg uint64) (uint64, bool) { return 1, false })
	res, err := Verify(p, nil, host, 0)
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}
	if res.Verdict != LeakFound {
		t.Fatalf("expected LeakFound from differing host-call sequences, got %v", res.Verdict)
	}
	if len(res.Counterexample.ObservationA.Calls) == len(res.Counterexample.ObservationB.Calls) {
		t.Fatal("counterexample observations must differ in call count")
	}
}

func TestVerifyUnknownWhenBudgetExhausted(t *testing.T) {
	// Two bytes of secret input => 65536 assignments. A tiny budget cannot
	// cover the domain and the leak-free program must come back Unknown.
	p := &Program{
		Vars: []Variable{
			{Name: "s0", Visibility: Secret, Bits: 8},
			{Name: "s1", Visibility: Secret, Bits: 8},
			{Name: "fixed", Visibility: Public, Bits: 8, Init: 1},
		},
		Code: []Instr{{Op: OpReturn, Src: 2}},
	}
	res, err := Verify(p, nil, nil, 100)
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}
	if res.Verdict != Unknown || res.Complete {
		t.Fatalf("expected incomplete Unknown, got %v complete=%v", res.Verdict, res.Complete)
	}
	if res.Budget.Runs > 100 {
		t.Fatalf("budget exceeded: %d runs", res.Budget.Runs)
	}
}

func TestVerifyRejectsInputBeyondSixBytes(t *testing.T) {
	p := &Program{
		Vars: []Variable{
			{Name: "s0", Visibility: Secret, Bits: 16},
			{Name: "s1", Visibility: Secret, Bits: 16},
			{Name: "s2", Visibility: Secret, Bits: 16},
			{Name: "s3", Visibility: Secret, Bits: 16},
			{Name: "out", Visibility: Public, Bits: 8},
		},
		Code: []Instr{{Op: OpReturn, Src: 4}},
	}
	if _, err := Verify(p, nil, nil, 0); err == nil {
		t.Fatal("expected error for secret input exceeding six bytes")
	}
}

func TestVerifyNoSecretsRunsOnce(t *testing.T) {
	p := &Program{
		Vars: []Variable{{Name: "fixed", Visibility: Public, Bits: 8, Init: 9}},
		Code: []Instr{{Op: OpReturn, Src: 0}},
	}
	res, err := Verify(p, nil, nil, 0)
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}
	if res.Verdict != SecretIndependent || res.Explored != 1 || !res.Complete {
		t.Fatalf("expected one complete clean run, got %+v", res)
	}
}
