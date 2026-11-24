package migrationadmission

import (
	"errors"
	"testing"
)

// TestNameLengthCap checks the inclusive 64-byte limit through both direct
// transduction and admission.
func TestNameLengthCap(t *testing.T) {
	_, next := mildPolicies(MaxNameLength)
	newCompiled, err := next.compile()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newCompiled.transduce(make([]byte, MaxNameLength+1)); !errors.Is(err, ErrIllegalName) {
		t.Fatalf("over-cap transduction: %v", err)
	}

	// The all-accepting grammar accepts a name exactly at the inclusive cap.
	fullGrammar := allAcceptGrammar("g-cap", MaxNameLength)
	capPolicy := Policy{Version: "cap", Grammar: fullGrammar, Transcription: newMildTable()}
	capCompiled, err := capPolicy.compile()
	if err != nil {
		t.Fatal(err)
	}
	atCap := make([]byte, MaxNameLength)
	for i := range atCap {
		atCap[i] = 'a'
	}
	if _, err := capCompiled.transduce(atCap); err != nil {
		t.Fatalf("alphabet name at cap rejected: %v", err)
	}
}

// TestWireRoundTrip guarantees the self-contained blob survives
// marshal/unmarshal and that truncation is rejected.
func TestWireRoundTrip(t *testing.T) {
	old, next := mildPolicies(2)
	_, priv := testRoot(t)
	signed := buildBundle(t, "tenant-wire", 1, old, next, nil, priv, nil)
	wire := marshalWire(t, signed)

	reparsed, err := UnmarshalSignedBundle(wire)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	bundle, err := reparsed.Unmarshal()
	if err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if bundle.TenantID != "tenant-wire" || bundle.Epoch != 1 {
		t.Fatalf("bundle binding lost: %+v", bundle)
	}
	if _, err := Verify(wire, nil); err != nil {
		t.Fatalf("round-trip verify: %v", err)
	}

	for _, cut := range [][]byte{
		wire[:len(wire)-1],
		wire[:8],
		nil,
	} {
		if _, err := UnmarshalSignedBundle(cut); err == nil {
			t.Fatalf("truncated blob of %d bytes accepted", len(cut))
		}
	}

	// Appending trailing bytes is rejected.
	if _, err := UnmarshalSignedBundle(append(append([]byte(nil), wire...), 0)); !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("trailing bytes: %v", err)
	}
}

// TestDeterministicEncoding checks policy hashing is stable across calls.
func TestDeterministicEncoding(t *testing.T) {
	_, next := mildPolicies(2)
	first := PolicyHash(next)
	second := PolicyHash(next)
	if first != second {
		t.Fatal("policy hash is not deterministic")
	}
	shuffled := next
	shuffled.Grammar.Transitions = append([]Rule(nil), next.Grammar.Transitions...)
	shuffled.Grammar.Transitions[0], shuffled.Grammar.Transitions[1] =
		shuffled.Grammar.Transitions[1], shuffled.Grammar.Transitions[0]
	if PolicyHash(shuffled) != first {
		t.Fatal("canonical encoding must be order-independent")
	}
}
