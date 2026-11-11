package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const testTTL = time.Minute

func TestReserveThenPublishHappyPath(t *testing.T) {
	svc, store, verifier, _ := newTestService(t, testTTL)
	ctx := context.Background()

	verifier.Put("img:1", "digest-a")
	res, err := svc.Reserve(ctx, "img:1", "digest-a", "req-1")
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	rec := mustRecord(t, res.Record, StateReserved)
	if rec.RequestID != "req-1" || rec.Digest != "digest-a" || rec.Version != 1 {
		t.Fatalf("unexpected reservation: %+v", rec)
	}

	pub, err := svc.Publish(ctx, "img:1", "digest-a", "req-1")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	rec = mustRecord(t, pub.Record, StatePublished)
	if rec.Version != 2 || !rec.ExpiresAt.IsZero() {
		t.Fatalf("unexpected published record: %+v", rec)
	}

	snap, err := store.Label(ctx, "img:1")
	if err != nil || snap.State != StatePublished {
		t.Fatalf("snapshot = %+v, err = %v", snap, err)
	}
}

func TestReserveIdempotentAndDigestConflict(t *testing.T) {
	svc, store, _, _ := newTestService(t, testTTL)
	ctx := context.Background()

	first, err := svc.Reserve(ctx, "img:2", "digest-a", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Reserve(ctx, "img:2", "digest-a", "req-1")
	if err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	if !second.Replayed || second.Record.Version != first.Record.Version {
		t.Fatalf("retry must not mutate: first=%+v second=%+v", first.Record, second.Record)
	}

	_, err = svc.Reserve(ctx, "img:2", "digest-b", "req-1")
	errIs(t, err, ErrConflict)

	_, err = svc.Reserve(ctx, "img:2", "digest-a", "req-other")
	errIs(t, err, ErrConflict)

	snap, _ := store.Label(ctx, "img:2")
	if snap.State != StateReserved || snap.RequestID != "req-1" || snap.Digest != "digest-a" {
		t.Fatalf("record must stay owned by req-1: %+v", snap)
	}
}

func TestPublishedLabelIsImmutable(t *testing.T) {
	svc, _, verifier, _ := newTestService(t, testTTL)
	ctx := context.Background()
	verifier.Put("img:3", "digest-a")

	pub, err := svc.Publish(ctx, "img:3", "digest-a", "req-1")
	if err != nil {
		t.Fatal(err)
	}

	// Same request ID + same digest: idempotent replay, content re-verified.
	retry, err := svc.Publish(ctx, "img:3", "digest-a", "req-1")
	if err != nil || !retry.Replayed || retry.Record.Version != pub.Record.Version {
		t.Fatalf("idempotent publish retry: res=%+v err=%v", retry, err)
	}
	if verifier.callCount() != 2 {
		t.Fatalf("verifier calls = %d, want 2 (re-verification on retry)", verifier.callCount())
	}

	_, err = svc.Publish(ctx, "img:3", "digest-b", "req-1")
	errIs(t, err, ErrConflict)
	_, err = svc.Publish(ctx, "img:3", "digest-a", "req-other")
	errIs(t, err, ErrConflict)

	_, err = svc.Release(ctx, "img:3", "req-1")
	errIs(t, err, ErrConflict)
}

func TestConcurrentFirstPublishesOnlyOneSucceeds(t *testing.T) {
	const rounds = 12
	for round := 0; round < rounds; round++ {
		store := NewMemoryStore()
		inner := newFakeVerifier()
		barrier := newBarrierVerifier(t, inner)
		svc := NewService(store, barrier, WithClock(newFakeClock()), WithTTL(testTTL))
		ctx := context.Background()

		label := "img-concurrent"
		// Both uploads are valid in isolation; the state machine, not the
		// verifier, decides which request owns the immutable label.
		inner.AllowAnyDigest(label)

		type outcome struct {
			ok  bool
			err error
		}
		outcomes := make([]outcome, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := svc.Publish(ctx, label, "digest-a", "req-a")
			outcomes[0] = outcome{err == nil, err}
		}()
		go func() {
			defer wg.Done()
			_, err := svc.Publish(ctx, label, "digest-b", "req-b")
			outcomes[1] = outcome{err == nil, err}
		}()

		barrier.waitEntered(t)
		// Give the loser time to block on the per-label transaction lock.
		time.Sleep(20 * time.Millisecond)
		barrier.release()
		wg.Wait()

		successes := 0
		for i, o := range outcomes {
			if o.ok {
				successes++
			} else if !errors.Is(o.err, ErrConflict) {
				t.Fatalf("round %d loser %d got unexpected error %v", round, i, o.err)
			}
		}
		if successes != 1 {
			t.Fatalf("round %d: successes = %d, want exactly 1", round, successes)
		}
		snap, err := store.Label(ctx, label)
		if err != nil || snap.State != StatePublished || snap.Version != 1 {
			t.Fatalf("round %d: final record = %+v, err = %v", round, snap, err)
		}
	}
}

func TestConcurrentFirstPublishesSameDigestDifferentRequestsConflict(t *testing.T) {
	store := NewMemoryStore()
	inner := newFakeVerifier()
	barrier := newBarrierVerifier(t, inner)
	svc := NewService(store, barrier, WithClock(newFakeClock()), WithTTL(testTTL))
	ctx := context.Background()

	label := "img-same-digest"
	inner.AllowAnyDigest(label)

	var wg sync.WaitGroup
	outcomes := make([]struct {
		ok  bool
		err error
	}, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := svc.Publish(ctx, label, "digest-a", "req-a")
		outcomes[0] = struct {
			ok  bool
			err error
		}{err == nil, err}
	}()
	go func() {
		defer wg.Done()
		_, err := svc.Publish(ctx, label, "digest-a", "req-b")
		outcomes[1] = struct {
			ok  bool
			err error
		}{err == nil, err}
	}()

	barrier.waitEntered(t)
	time.Sleep(20 * time.Millisecond)
	barrier.release()
	wg.Wait()

	successes := 0
	for i, o := range outcomes {
		if o.ok {
			successes++
		} else if !errors.Is(o.err, ErrConflict) {
			t.Fatalf("loser %d: error = %v, want ErrConflict", i, o.err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1 (different request IDs cannot share a label)", successes)
	}
}
