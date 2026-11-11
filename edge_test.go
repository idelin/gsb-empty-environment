package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

type brokenVerifier struct{ err error }

func (b brokenVerifier) Verify(context.Context, string, string) error { return b.err }

func TestPublishPropagatesVerifierFailureWithoutState(t *testing.T) {
	store := NewMemoryStore()
	boom := errors.New("verifier backend down")
	svc := NewService(store, brokenVerifier{err: boom}, WithClock(newFakeClock()), WithTTL(testTTL))
	ctx := context.Background()

	_, err := svc.Publish(ctx, "img:13", "digest-a", "req-1")
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped verifier error", err)
	}
	snap, _ := store.Label(ctx, "img:13")
	if snap != nil {
		t.Fatalf("verifier failure must not create a record: %+v", snap)
	}
}

func TestReleaseExpiredReservationIsIdempotentRevoke(t *testing.T) {
	svc, store, _, clock := newTestService(t, testTTL)
	ctx := context.Background()

	if _, err := svc.Reserve(ctx, "img:14", "digest-a", "req-1"); err != nil {
		t.Fatal(err)
	}
	clock.advance(testTTL)

	rel, err := svc.Release(ctx, "img:14", "req-1")
	if err != nil {
		t.Fatalf("release expired reservation: %v", err)
	}
	rec := mustRecord(t, rel.Record, StateRevoked)
	if !rel.Replayed {
		t.Fatal("expired reservation release should be reported as replayed cleanup")
	}

	// Second release after the state is already revoked: no version change.
	again, err := svc.Release(ctx, "img:14", "req-1")
	if err != nil || !again.Replayed || again.Record.Version != rec.Version {
		t.Fatalf("second release: res=%+v err=%v", again, err)
	}

	snap, _ := store.Label(ctx, "img:14")
	if snap.State != StateRevoked {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestClockAndTTLDefaultsAndOptions(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newFakeVerifier())

	before := time.Now()
	res, err := svc.Reserve(context.Background(), "img:15", "d", "r")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	wantMin := before.Add(15 * time.Minute)
	wantMax := after.Add(15 * time.Minute)
	if res.Record.ExpiresAt.Before(wantMin) || res.Record.ExpiresAt.After(wantMax) {
		t.Fatalf("default TTL expiry = %v outside [%v, %v]", res.Record.ExpiresAt, wantMin, wantMax)
	}
	if got := StatePublished.String(); got != "published" {
		t.Fatalf("state string = %q", got)
	}
	if got := State(99).String(); got != "absent" {
		t.Fatalf("unknown state string = %q", got)
	}
}

func TestVerifierFuncAdapter(t *testing.T) {
	called := false
	v := VerifierFunc(func(_ context.Context, label, digest string) error {
		called = true
		if label != "l" || digest != "d" {
			t.Fatalf("unexpected args %s/%s", label, digest)
		}
		return nil
	})
	if err := v.Verify(context.Background(), "l", "d"); err != nil || !called {
		t.Fatalf("adapter: err=%v called=%v", err, called)
	}
}
