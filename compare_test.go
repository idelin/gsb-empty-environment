package netsec

import "testing"

func testSecret() []byte {
	s := make([]byte, SecretSize)
	for i := range s {
		s[i] = byte(i*7 + 3)
	}
	return s
}

func runCompare(t *testing.T, secret, candidate []byte) ([]Event, byte) {
	t.Helper()
	var vm VM
	copy(vm.Mem[SecretBase:], secret)
	copy(vm.Mem[CandidateBase:], candidate)
	vm.MaxSteps = compareSteps
	if err := vm.Run(CompareProgram()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return vm.Trace, vm.R[0]
}

// TestEqualExhaustive enumerates the all-equal input plus a single-byte
// difference at every byte position with every possible delta, and requires
// both correct results and an identical observable trace for every case.
func TestEqualExhaustive(t *testing.T) {
	secret := testSecret()

	if !Equal(secret, secret) {
		t.Fatal("all-equal input must compare equal")
	}
	refTrace, acc := runCompare(t, secret, secret)
	if acc != 0 {
		t.Fatalf("all-equal input left accumulator %d, want 0", acc)
	}
	if len(refTrace) != 2*SecretSize {
		t.Fatalf("trace has %d events, want %d reads", len(refTrace), 2*SecretSize)
	}
	for i, ev := range refTrace {
		if ev.Kind != EventRead {
			t.Fatalf("trace event %d is %v, want only reads", i, ev.Kind)
		}
	}

	for pos := 0; pos < SecretSize; pos++ {
		for delta := 1; delta < 256; delta++ {
			cand := make([]byte, CandidateSize)
			copy(cand, secret)
			cand[pos] ^= byte(delta)

			if Equal(secret, cand) {
				t.Fatalf("pos %d delta %d: different inputs compared equal", pos, delta)
			}
			tr, acc := runCompare(t, secret, cand)
			if acc == 0 {
				t.Fatalf("pos %d delta %d: accumulator is 0 for different inputs", pos, delta)
			}
			if d := FirstDivergence(refTrace, tr); d != -1 {
				t.Fatalf("pos %d delta %d: trace diverges at event %d (%+v vs %+v)",
					pos, delta, d, refTrace[d], tr[d])
			}
		}
	}
}

// TestEqualLengthValidation checks that length validation rejects mismatched
// inputs before the secret region is touched.
func TestEqualLengthValidation(t *testing.T) {
	secret := testSecret()
	if Equal(secret[:SecretSize-1], secret) {
		t.Fatal("short secret must not compare equal")
	}
	if Equal(secret, secret[:CandidateSize-1]) {
		t.Fatal("short candidate must not compare equal")
	}
	if Equal(append(secret, 0), secret) {
		t.Fatal("long secret must not compare equal")
	}
	if Equal(nil, nil) {
		t.Fatal("empty inputs must not compare equal")
	}
}
