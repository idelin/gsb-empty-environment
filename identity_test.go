package batchlease

import (
	"errors"
	"testing"
)

// TestReopenKeepsBatchIdentity verifies the core requirement end to end:
// after a simulated power loss, promised data is still recognized under its
// original batch and old-generation tokens cannot forge confirmations.
func TestReopenKeepsBatchIdentity(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir, nil)
	firstEpoch := s.Epoch()
	la := mustAcquire(t, s, "batch-A", 1, 4, "A")
	mustAcquire(t, s, "batch-B", 5, 9, "B")
	mustConfirm(t, s, la.Token, 1)
	mustConfirm(t, s, la.Token, 2)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2 := reopenStore(t, dir)
	defer s2.Close()
	if s2.Epoch() <= firstEpoch {
		t.Fatalf("epoch did not advance across reopen: %d <= %d", s2.Epoch(), firstEpoch)
	}
	if s2.Confirmed() != 2 {
		t.Fatalf("confirmed lost: %d", s2.Confirmed())
	}
	if id, ok := s2.Identify(3); !ok || id != "batch-A" {
		t.Fatalf("seq 3 identity: %q %v", id, ok)
	}
	if id, ok := s2.Identify(7); !ok || id != "batch-B" {
		t.Fatalf("seq 7 identity: %q %v", id, ok)
	}
	if _, ok := s2.Identify(100); ok {
		t.Fatal("unclaimed sequence reported as identified")
	}

	// A forged ticket that does not match the persisted lease (wrong
	// generation) must be rejected.
	forged := Token{BatchID: "batch-A", Epoch: la.Token.Epoch + 1}
	if err := s2.Confirm(forged, 3); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("forged token confirm: got %v want ErrInvalidToken", err)
	}

	persisted, ok := s2.Lease("batch-A")
	if !ok {
		t.Fatal("batch-A lease missing")
	}
	mustConfirm(t, s2, persisted.Token, 3)
	mustConfirm(t, s2, persisted.Token, 4)
	if s2.Confirmed() != 4 {
		t.Fatalf("confirmed after resume: %d", s2.Confirmed())
	}

	if _, err := s2.Acquire("batch-X", BatchSummary{FirstSeq: 2, LastSeq: 2, Hash: h("X")}); !errors.Is(err, ErrOverlap) {
		t.Fatalf("re-dispatch: got %v want ErrOverlap", err)
	}
}
