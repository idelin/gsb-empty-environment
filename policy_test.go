package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0132"
)

func TestVerifyAcceptsCorrectLengthAndMatchingNonce(t *testing.T) {
	challenge := []byte("verifier-challenge-1234")
	claims := netsec.Claims{
		UEID:  bytesOfLength(17, 0x01),
		Nonce: append([]byte(nil), challenge...),
	}
	policy := netsec.Policy{RequireUEID: true}

	if err := netsec.Verify(claims, policy, challenge); err != nil {
		t.Fatalf("valid proof was rejected: %v", err)
	}
}

func TestVerifyRejectsWrongLengthUEID(t *testing.T) {
	challenge := []byte("verifier-challenge-1234")
	claims := netsec.Claims{
		UEID:  bytesOfLength(16, 0x01),
		Nonce: append([]byte(nil), challenge...),
	}
	policy := netsec.Policy{RequireUEID: true}

	err := netsec.Verify(claims, policy, challenge)
	if !errors.Is(err, netsec.ErrUEIDLength) {
		t.Fatalf("expected ErrUEIDLength, got %v", err)
	}
}

func TestVerifyRejectsSUEIDOnlyWhenUEIDIsRequired(t *testing.T) {
	challenge := []byte("verifier-challenge-1234")
	claims := netsec.Claims{
		SUEID: bytesOfLength(17, 0x01),
		Nonce: append([]byte(nil), challenge...),
	}
	policy := netsec.Policy{RequireUEID: true}

	err := netsec.Verify(claims, policy, challenge)
	if !errors.Is(err, netsec.ErrUEIDRequired) {
		t.Fatalf("expected ErrUEIDRequired, got %v", err)
	}
}

func TestVerifyRejectsNonceMismatch(t *testing.T) {
	challenge := []byte("verifier-challenge-1234")
	claims := netsec.Claims{
		UEID:  bytesOfLength(17, 0x01),
		Nonce: []byte("attacker-challenge-123"),
	}
	policy := netsec.Policy{RequireUEID: true}

	err := netsec.Verify(claims, policy, challenge)
	if !errors.Is(err, netsec.ErrNonceMismatch) {
		t.Fatalf("expected ErrNonceMismatch, got %v", err)
	}
}

func TestVerifyAllowsSUEIDWhenUEIDIsNotRequired(t *testing.T) {
	challenge := []byte("verifier-challenge-1234")
	claims := netsec.Claims{
		SUEIDs: map[string][]byte{"onboarding": bytesOfLength(17, 0x01)},
		Nonce:  append([]byte(nil), challenge...),
	}
	policy := netsec.Policy{RequireSUEID: true}

	if err := netsec.Verify(claims, policy, challenge); err != nil {
		t.Fatalf("valid sueid-only proof was rejected: %v", err)
	}
}

func TestVerifyUsesPolicySpecificUEIDLengths(t *testing.T) {
	challenge := []byte("verifier-challenge-1234")
	claims := netsec.Claims{
		UEID:  bytesOfLength(20, 0x01),
		Nonce: append([]byte(nil), challenge...),
	}
	policy := netsec.Policy{
		RequireUEID:        true,
		AllowedUEIDLengths: []int{20},
	}

	if err := netsec.Verify(claims, policy, challenge); err != nil {
		t.Fatalf("ueid allowed by policy was rejected: %v", err)
	}
}

func TestNewVerifierRejectsEmptyAllowedLengths(t *testing.T) {
	_, err := netsec.NewVerifier(netsec.Policy{
		AllowedUEIDLengths: []int{},
	})
	if !errors.Is(err, netsec.ErrPolicyInvalid) {
		t.Fatalf("expected ErrPolicyInvalid, got %v", err)
	}
}

func bytesOfLength(length int, first byte) []byte {
	result := make([]byte, length)
	if length > 0 {
		result[0] = first
	}
	return result
}
