package netsec

import (
	"bytes"
	"fmt"
	"testing"
)

type testHarness struct {
	signer   LevelSigner
	verifier LevelVerifier
}

func newTestHarness(t *testing.T) testHarness {
	t.Helper()
	pub, priv, err := GenerateLevelKey()
	if err != nil {
		t.Fatalf("GenerateLevelKey: %v", err)
	}
	signer, err := NewEd25519Signer(priv)
	if err != nil {
		t.Fatalf("NewEd25519Signer: %v", err)
	}
	verifier, err := NewEd25519Verifier(pub)
	if err != nil {
		t.Fatalf("NewEd25519Verifier: %v", err)
	}
	return testHarness{signer: signer, verifier: verifier}
}

// stubLevelKey is a deterministic in-memory signer/verifier used by the
// exhaustive sweep, which needs thousands of signature checks. Real Ed25519
// semantics are covered separately by TestEd25519RoundTripAndRejection.
type stubLevelKey struct {
	secret string
}

func newStubHarness() testHarness {
	key := stubLevelKey{secret: "exhaustive-secret"}
	return testHarness{signer: key, verifier: key}
}

func (s stubLevelKey) SignLevel(nodeID string, level SecurityLevel) ([]byte, error) {
	return []byte(fmt.Sprintf("stub:%s:%d:%s", nodeID, level, s.secret)), nil
}

func (s stubLevelKey) VerifyLevel(nodeID string, level SecurityLevel, signature []byte) error {
	want, err := s.SignLevel(nodeID, level)
	if err != nil {
		return err
	}
	if !bytes.Equal(want, signature) {
		return fmt.Errorf("stub signature mismatch for node %q", nodeID)
	}
	return nil
}

func (h testHarness) node(t *testing.T, id string, level SecurityLevel,
	revoked bool, features, required Features) VersionNode {
	t.Helper()
	signature, err := h.signer.SignLevel(id, level)
	if err != nil {
		t.Fatalf("SignLevel(%q): %v", id, err)
	}
	return VersionNode{
		ID:            id,
		Security:      SignedLevel{Level: level, Signature: signature},
		Features:      features,
		Compatibility: Compatibility{Required: required},
		Revoked:       revoked,
	}
}

func nodeID(i int) string {
	return fmt.Sprintf("v%02d", i)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
