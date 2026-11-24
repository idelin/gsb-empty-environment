package migrationadmission

import (
	"errors"
	"testing"
)

// TestNameOutsideOldGrammar verifies that a new-legal name with no old
// subject cannot be folded onto another name through deletion.
func TestNameOutsideOldGrammar(t *testing.T) {
	old, next := mildPolicies(2)
	// The old grammar only accepts the single character 'a'; any 'b' leaves it.
	old.Grammar = Grammar{
		Name:    "g-old-restricted",
		Start:   0,
		Accepts: []int{1},
		Transitions: []Rule{
			{From: 0, On: 'a', To: 1},
		},
	}
	old.Transcription = tableWith("old-restricted", []TranscriptionRule{
		{On: 'a', Emit: []Symbol{symA}},
	})
	// New policy deletes 'b' so "b" and ""-style suffixes could otherwise fold.
	next.Transcription = tableWith("new-delete", []TranscriptionRule{
		{On: 'a', Emit: []Symbol{symA}},
		{On: 'b', Emit: nil},
	}, symT)

	oldCompiled := mustCompile(t, old)
	newCompiled := mustCompile(t, next)
	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   map[string]bool{},
	})
	if !errors.Is(result.Violation, ErrUnauthorizedFold) {
		t.Fatalf("fold involving an old-grammar outsider accepted: %v", result.Violation)
	}
}

// TestAliasVersionBinding rejects claims signed for a different policy version.
func TestAliasVersionBinding(t *testing.T) {
	old, next := mildPolicies(2)
	next.Transcription = tableWith("new-delete", []TranscriptionRule{
		{On: 'a', Emit: []Symbol{symA}},
		{On: 'b', Emit: nil},
	}, symT)
	_, priv := testRoot(t)
	claims, _ := requiredAliases(t, "tenant-binding", "wrong-version", 1, old, next, priv)
	signed := buildBundle(t, "tenant-binding", 1, old, next, claims, priv, nil)
	if _, err := Verify(marshalWire(t, signed), nil); !errors.Is(err, ErrAliasProofIncomplete) {
		t.Fatalf("wrong policy version claim accepted: %v", err)
	}
}

// TestLatenessTenantBinding rejects a certificate bound to another tenant.
func TestLatenessTenantBinding(t *testing.T) {
	old, next := mildPolicies(2)
	pub, priv := testRoot(t)
	oldCompiled := mustCompile(t, old)
	newCompiled := mustCompile(t, next)
	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   map[string]bool{},
	})
	cert, err := SignLatenessCertificate(priv, LatenessCertificate{
		TenantID:      "other-tenant",
		PolicyVersion: next.Version,
		MaxBacklog:    result.MaxBacklog,
		Epoch:         1,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{
		TenantID:  "tenant-lateness",
		Epoch:     1,
		OldPolicy: old,
		NewPolicy: next,
		RootKey:   pub,
		Lateness:  cert,
	}
	signed, err := bundle.Marshal(priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(marshalWire(t, signed), nil); !errors.Is(err, ErrAliasProofIncomplete) {
		t.Fatalf("foreign lateness tenant accepted: %v", err)
	}
}
