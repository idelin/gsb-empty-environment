package netsec

import (
	"context"
	"testing"
)

func TestReservationExpiryAndTakeover(t *testing.T) {
	svc, store, _, clock := newTestService(t, testTTL)
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, "img:4", "digest-a", "req-1"); err != nil {
		t.Fatal(err)
	}

	clock.advance(testTTL)

	// Original request retries after expiry: it must learn the reservation died.
	_, err := svc.Reserve(ctx, "img:4", "digest-a", "req-1")
	errIs(t, err, ErrReservationExpired)

	// The expired reservation is atomically revoked and re-acquired by a
	// fresh request inside one transaction.
	takeover, err := svc.Reserve(ctx, "img:4", "digest-b", "req-2")
	if err != nil {
		t.Fatalf("takeover reserve: %v", err)
	}
	rec := mustRecord(t, takeover.Record, StateReserved)
	if rec.RequestID != "req-2" || rec.Digest != "digest-b" || rec.Version != 2 {
		t.Fatalf("unexpected takeover record: %+v", rec)
	}

	snap, _ := store.Label(ctx, "img:4")
	if snap.State != StateReserved {
		t.Fatalf("snapshot = %+v", snap)
	}

	// After takeover, the stale request is simply a foreign request ID and
	// cannot publish the now-owned label; the expiry-specific rejection was
	// already asserted on the retry above (and in the late-content test).
	_, err = svc.Publish(ctx, "img:4", "digest-a", "req-1")
	errIs(t, err, ErrConflict)
}

func TestExpiredReservationPublishesAtomicallyForNewRequest(t *testing.T) {
	svc, store, verifier, clock := newTestService(t, testTTL)
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, "img:5", "digest-a", "req-1"); err != nil {
		t.Fatal(err)
	}
	clock.advance(testTTL)
	verifier.Put("img:5", "digest-b")

	pub, err := svc.Publish(ctx, "img:5", "digest-b", "req-2")
	if err != nil {
		t.Fatalf("publish after expiry: %v", err)
	}
	rec := mustRecord(t, pub.Record, StatePublished)
	if rec.RequestID != "req-2" || rec.Version != 2 {
		t.Fatalf("unexpected record: %+v", rec)
	}
	snap, _ := store.Label(ctx, "img:5")
	if snap.State != StatePublished {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestContentArrivesLate(t *testing.T) {
	t.Run("publish before upload writes nothing then succeeds after arrival", func(t *testing.T) {
		svc, store, verifier, _ := newTestService(t, testTTL)
		ctx := context.Background()

		// Content has not arrived: publish must fail and leave no record.
		_, err := svc.Publish(ctx, "img:6", "digest-a", "req-1")
		errIs(t, err, ErrContentNotFound)
		snap, _ := store.Label(ctx, "img:6")
		if snap != nil {
			t.Fatalf("failed verify must abort without writing state: %+v", snap)
		}

		// Upload arrives late: the same request now publishes successfully.
		verifier.Put("img:6", "digest-a")
		pub, err := svc.Publish(ctx, "img:6", "digest-a", "req-1")
		if err != nil {
			t.Fatalf("publish after upload: %v", err)
		}
		mustRecord(t, pub.Record, StatePublished)
	})

	t.Run("reservation survives late content until expiry", func(t *testing.T) {
		svc, store, verifier, clock := newTestService(t, testTTL)
		ctx := context.Background()

		if _, err := svc.Reserve(ctx, "img:7", "digest-a", "req-1"); err != nil {
			t.Fatal(err)
		}
		_, err := svc.Publish(ctx, "img:7", "digest-a", "req-1")
		errIs(t, err, ErrContentNotFound)
		snap, _ := store.Label(ctx, "img:7")
		if snap.State != StateReserved || snap.Version != 1 {
			t.Fatalf("reservation must survive failed verify: %+v", snap)
		}

		clock.advance(testTTL)
		_, err = svc.Publish(ctx, "img:7", "digest-a", "req-1")
		errIs(t, err, ErrReservationExpired)

		verifier.Put("img:7", "digest-b")
		pub, err := svc.Publish(ctx, "img:7", "digest-b", "req-2")
		if err != nil {
			t.Fatalf("new request after late content: %v", err)
		}
		rec := mustRecord(t, pub.Record, StatePublished)
		if rec.RequestID != "req-2" {
			t.Fatalf("record = %+v", rec)
		}
	})
}

func TestContentMismatchRejectsAndReverifies(t *testing.T) {
	svc, store, verifier, _ := newTestService(t, testTTL)
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, "img:8", "digest-a", "req-1"); err != nil {
		t.Fatal(err)
	}
	verifier.FailMismatch("img:8")
	_, err := svc.Publish(ctx, "img:8", "digest-a", "req-1")
	errIs(t, err, ErrContentMismatch)

	snap, _ := store.Label(ctx, "img:8")
	if snap.State != StateReserved {
		t.Fatalf("mismatch must not publish: %+v", snap)
	}

	// Repair the upload; a retry re-runs verification inside the transaction.
	delete(verifier.mismatch, "img:8")
	verifier.Put("img:8", "digest-a")
	pub, err := svc.Publish(ctx, "img:8", "digest-a", "req-1")
	if err != nil {
		t.Fatalf("retry after repair: %v", err)
	}
	mustRecord(t, pub.Record, StatePublished)
	if verifier.callCount() != 2 {
		t.Fatalf("verifier calls = %d, want 2", verifier.callCount())
	}
}

func TestReleaseFlow(t *testing.T) {
	svc, store, _, _ := newTestService(t, testTTL)
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, "img:9", "digest-a", "req-1"); err != nil {
		t.Fatal(err)
	}

	// Another request cannot release a reservation it does not own.
	_, err := svc.Release(ctx, "img:9", "req-other")
	errIs(t, err, ErrConflict)

	rel, err := svc.Release(ctx, "img:9", "req-1")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	rec := mustRecord(t, rel.Record, StateRevoked)
	if rec.Version != 2 {
		t.Fatalf("version = %d, want 2", rec.Version)
	}

	// Replaying release is idempotent and does not bump the version.
	retry, err := svc.Release(ctx, "img:9", "req-1")
	if err != nil || !retry.Replayed || retry.Record.Version != rec.Version {
		t.Fatalf("replay release: res=%+v err=%v", retry, err)
	}

	// A fresh request can re-acquire the revoked label.
	res, err := svc.Reserve(ctx, "img:9", "digest-b", "req-2")
	if err != nil {
		t.Fatalf("re-reserve after release: %v", err)
	}
	mustRecord(t, res.Record, StateReserved)

	snap, _ := store.Label(ctx, "img:9")
	if snap.State != StateReserved || snap.RequestID != "req-2" {
		t.Fatalf("snapshot = %+v", snap)
	}

	// Releasing an absent label is a conflict.
	_, err = svc.Release(ctx, "img:missing", "req-1")
	errIs(t, err, ErrConflict)
}
