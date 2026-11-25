package admission

import (
	"bytes"
	"testing"
)

func admissionTestSignedFor(domain, alias string, cp Checkpoint) WitnessSignature {
	// The signature bytes are opaque and pre-verified in this test; the binding
	// fields are what must make replay detectable.
	return NewWitnessSignature(domain, alias, cp, []byte("sig:"+domain))
}

func TestImplementationWitnessAliasesDedupByActualDomain(t *testing.T) {
	cp := NewCheckpoint(4, MerkleRoot(admissionTestLeaves(4)))
	sigs := []WitnessSignature{
		admissionTestSignedFor("domain-a", "proxy-1", cp),
		admissionTestSignedFor("domain-a", "proxy-2", cp),
		admissionTestSignedFor("domain-b", "proxy-3", cp),
	}
	if got := ValidWitnesses(sigs, cp, nil); got != 2 {
		t.Fatalf("distinct domains=%d want 2", got)
	}
	if MeetsWitnessThreshold(sigs, cp, 3, nil) {
		t.Fatal("aliased signatures counted more than once")
	}
}

func TestImplementationWitnessBindingPreventsCrossTreeReplay(t *testing.T) {
	cp1 := NewCheckpoint(4, MerkleRoot(admissionTestLeaves(4)))
	cp2 := NewCheckpoint(8, MerkleRoot(admissionTestLeaves(8)))
	sig := admissionTestSignedFor("domain-a", "", cp1)
	if err := sig.BindingError("", cp1); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	if err := sig.BindingError("", cp2); err == nil {
		t.Fatal("signature replayed across trees")
	}

	// The source tag is part of the signed message. A payload created for one
	// source must not validate for another.
	sig.SourceTag = "source-1"
	sig.Payload = WitnessMessage("source-1", cp1.Size, cp1.Root)
	if err := sig.BindingError("", cp1); err != nil {
		t.Fatalf("canonical payload rejected: %v", err)
	}
	sig.SourceTag = "source-2"
	if err := sig.BindingError("", cp1); err == nil {
		t.Fatal("source tag change was not bound")
	}
}

func TestImplementationWitnessVerifierIsRequiredWhenConfigured(t *testing.T) {
	cp := NewCheckpoint(4, MerkleRoot(admissionTestLeaves(4)))
	sig := admissionTestSignedFor("domain-a", "", cp)
	sig.Signature = []byte("bad")
	verifier := func(domain string, message, signature []byte) bool {
		return domain == "domain-a" && bytes.Equal(signature, []byte("good"))
	}
	if MeetsWitnessThreshold([]WitnessSignature{sig}, cp, 1, verifier) {
		t.Fatal("invalid cryptographic signature accepted")
	}
	sig.Signature = []byte("good")
	if !MeetsWitnessThreshold([]WitnessSignature{sig}, cp, 1, verifier) {
		t.Fatal("valid cryptographic signature rejected")
	}
}
