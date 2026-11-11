package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStoreFaultAbortsTransactionAndIdempotentRetryRecovers(t *testing.T) {
	svc, store, verifier, _ := newTestService(t, testTTL)
	ctx := context.Background()
	verifier.Put("img:10", "digest-a")

	injected := errors.New("simulated storage outage")
	var attempts int
	store.setFailHook(func(label string, next *LabelRecord) error {
		attempts++
		if attempts == 1 {
			return injected
		}
		return nil
	})

	// First attempt: the commit fails, so the transaction must have no effect.
	_, err := svc.Publish(ctx, "img:10", "digest-a", "req-1")
	if !errors.Is(err, injected) {
		t.Fatalf("error = %v, want injected failure", err)
	}
	snap, _ := store.Label(ctx, "img:10")
	if snap != nil {
		t.Fatalf("aborted transaction must leave no record: %+v", snap)
	}

	// Storage recovers; the same request ID retries and publishes exactly once.
	pub, err := svc.Publish(ctx, "img:10", "digest-a", "req-1")
	if err != nil {
		t.Fatalf("retry after storage recovery: %v", err)
	}
	rec := mustRecord(t, pub.Record, StatePublished)
	if rec.Version != 1 {
		t.Fatalf("version = %d, want 1 (no partial commit from attempt 1)", rec.Version)
	}
	if attempts != 2 {
		t.Fatalf("hook attempts = %d, want 2", attempts)
	}
}

// TestCommitThenResponseLost models a successful durable commit whose response
// is lost before reaching the client: the client retries with the same
// request ID and observes one idempotent replay instead of a double commit.
func TestCommitThenResponseLost(t *testing.T) {
	svc, store, verifier, _ := newTestService(t, testTTL)
	ctx := context.Background()
	verifier.Put("img:11", "digest-a")

	pub, err := svc.Publish(ctx, "img:11", "digest-a", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	committedVersion := pub.Record.Version

	// The network drops the response; the client retries verbatim.
	retry, err := svc.Publish(ctx, "img:11", "digest-a", "req-1")
	if err != nil {
		t.Fatalf("retry after lost response: %v", err)
	}
	if !retry.Replayed {
		t.Fatal("retry must be reported as an idempotent replay")
	}
	if retry.Record.Version != committedVersion {
		t.Fatalf("version changed from %d to %d on replay", committedVersion, retry.Record.Version)
	}

	snap, _ := store.Label(ctx, "img:11")
	if snap.State != StatePublished || snap.Version != committedVersion {
		t.Fatalf("store must contain exactly one commit: %+v", snap)
	}
	// Re-verification still runs so a corrupt upload cannot hide behind a replay.
	if verifier.callCount() != 2 {
		t.Fatalf("verifier calls = %d, want 2", verifier.callCount())
	}

	// A retried request that smuggles a different digest is rejected.
	_, err = svc.Publish(ctx, "img:11", "digest-evil", "req-1")
	errIs(t, err, ErrConflict)
}

func TestMemoryStoreTransitionGuard(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	if snap, err := store.Label(ctx, "x"); err != nil || snap != nil {
		t.Fatalf("absent label lookup: snap=%+v err=%v", snap, err)
	}

	_, err := store.Update(ctx, "x", func(*LabelRecord) (*LabelRecord, error) {
		return &LabelRecord{Label: "x", State: StateRevoked}, nil
	})
	errIs(t, err, ErrIllegalTransition)

	rec, err := store.Update(ctx, "x", func(*LabelRecord) (*LabelRecord, error) {
		return &LabelRecord{Label: "x", State: StateReserved, Digest: "d", RequestID: "r"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != 1 {
		t.Fatalf("version = %d, want 1", rec.Version)
	}

	// Published labels can never move back to reserved or revoked.
	if _, err = store.Update(ctx, "x", func(cur *LabelRecord) (*LabelRecord, error) {
		cur.State = StatePublished
		cur.ExpiresAt = time.Time{}
		return cur, nil
	}); err != nil {
		t.Fatalf("publish seed: %v", err)
	}
	for _, target := range []State{StateReserved, StateRevoked} {
		_, err = store.Update(ctx, "x", func(cur *LabelRecord) (*LabelRecord, error) {
			next := *cur
			next.State = target
			return &next, nil
		})
		errIs(t, err, ErrIllegalTransition)
	}

	// Mismatched label keys are rejected.
	_, err = store.Update(ctx, "x", func(*LabelRecord) (*LabelRecord, error) {
		return &LabelRecord{Label: "y", State: StatePublished}, nil
	})
	errIs(t, err, ErrIllegalTransition)
}

func TestContextCancellationAborts(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Update(ctx, "img:12", func(*LabelRecord) (*LabelRecord, error) {
		return &LabelRecord{Label: "img:12", State: StatePublished}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	snap, _ := store.Label(context.Background(), "img:12")
	if snap != nil {
		t.Fatalf("cancelled transaction must leave no record: %+v", snap)
	}
}

func TestInvalidArguments(t *testing.T) {
	svc, _, _, _ := newTestService(t, testTTL)
	ctx := context.Background()

	cases := []struct {
		label, digest, requestID string
	}{
		{"", "d", "r"},
		{"l", "", "r"},
		{"l", "d", ""},
	}
	for _, tc := range cases {
		if _, err := svc.Reserve(ctx, tc.label, tc.digest, tc.requestID); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Reserve(%+v) err = %v", tc, err)
		}
		if _, err := svc.Publish(ctx, tc.label, tc.digest, tc.requestID); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Publish(%+v) err = %v", tc, err)
		}
	}
	if _, err := svc.Release(ctx, "", "r"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Release empty label err = %v", err)
	}
}
