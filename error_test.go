package netsec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRefUpdateKindHelpers(t *testing.T) {
	creation := RefUpdate{Ref: "r", Old: ZeroOID, New: OID("n")}
	if !creation.IsCreation() || creation.IsDeletion() {
		t.Fatal("expected creation update")
	}

	deletion := RefUpdate{Ref: "r", Old: OID("o"), New: ZeroOID}
	if deletion.IsCreation() || !deletion.IsDeletion() {
		t.Fatal("expected deletion update")
	}

	update := RefUpdate{Ref: "r", Old: OID("o"), New: OID("n")}
	if update.IsCreation() || update.IsDeletion() {
		t.Fatal("expected ordinary update")
	}
}

func TestAdmissionErrorMessages(t *testing.T) {
	quotaErr := &AdmissionError{
		Needed: 100, Available: 40,
		Err: ErrQuotaExhausted,
	}
	if !errors.Is(quotaErr, ErrQuotaExhausted) {
		t.Fatal("quota error should unwrap to sentinel")
	}
	if !strings.Contains(quotaErr.Error(), "needs 100 bytes but only 40 available") {
		t.Fatalf("unexpected message: %s", quotaErr)
	}

	violationErr := &AdmissionError{
		Violations: []Violation{{
			Ref: "refs/heads/main", Commit: OID("c1"),
			Err: ErrUnsignedCommit,
		}},
	}
	if !errors.Is(violationErr, ErrUnsignedCommit) {
		t.Fatal("violation error should unwrap through the first violation")
	}
	if !strings.Contains(violationErr.Error(), "signature violation") {
		t.Fatalf("unexpected message: %s", violationErr)
	}

	plain := &AdmissionError{Err: ErrObjectNotFound}
	if !strings.Contains(plain.Error(), "push rejected") {
		t.Fatalf("unexpected message: %s", plain)
	}
}

func TestAdmitValidatesConfiguration(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	var nilPolicy *Policy
	if _, err := nilPolicy.Admit(ctx, nil); err == nil {
		t.Fatal("nil policy must error")
	}

	if _, err := NewPolicy(repo, nil, MemoryVerifier, alice).Admit(ctx, nil); err == nil {
		t.Fatal("missing quota must error")
	}
	if _, err := NewPolicy(repo, NewQuota(1), nil, alice).Admit(ctx, nil); err == nil {
		t.Fatal("missing verifier must error")
	}
}
