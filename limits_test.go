package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWatchCountLimit(t *testing.T) {
	a := newTestAuthorizer(t, WithLimits(Limits{MaxWatches: 2, EventsPerSecond: 0}))
	ctx := context.Background()

	w1, err := a.Watch(ctx, "tenantA", "tenantA/a/", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Watch(ctx, "tenantA", "tenantA/b/", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Watch(ctx, "tenantA", "tenantA/c/", 0); !errors.Is(err, ErrWatchLimitExceeded) {
		t.Fatalf("third watch: got %v, want ErrWatchLimitExceeded", err)
	}

	// Limits are per tenant: tenantB still has its own budget.
	wb, err := a.Watch(ctx, "tenantB", "tenantB/", 0)
	if err != nil {
		t.Fatalf("tenantB watch: %v", err)
	}

	if err := w1.Close(); err != nil {
		t.Fatal(err)
	}
	w3, err := a.Watch(ctx, "tenantA", "tenantA/c/", 0)
	if err != nil {
		t.Fatalf("watch after slot freed: %v", err)
	}

	w3.Close()
	wb.Close()
}

func TestEventRateLimit(t *testing.T) {
	clock := newFakeClock()
	// One token per second with a burst of 2.
	a := newTestAuthorizer(t,
		WithClock(clock),
		WithLimits(Limits{MaxWatches: 4, EventsPerSecond: 1, EventBurst: 2}),
	)
	w, err := a.Watch(context.Background(), "tenantA", "tenantA/", 0)
	if err != nil {
		t.Fatal(err)
	}

	// First two events consume the burst.
	if _, err := a.Publish("tenantA", OpPut, "tenantA/1", nil); err != nil {
		t.Fatalf("burst event 1: %v", err)
	}
	if _, err := a.Publish("tenantA", OpPut, "tenantA/2", nil); err != nil {
		t.Fatalf("burst event 2: %v", err)
	}
	// The third event has no token and must be rate limited.
	if _, err := a.Publish("tenantA", OpPut, "tenantA/3", nil); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("third event: got %v, want ErrRateLimited", err)
	}

	// The rate-limited event still enters history, but never reaches the
	// watch channel.
	if got := keys(drain(w.Events())); len(got) != 2 {
		t.Fatalf("watch received %d events, want 2: %v", len(got), got)
	}
	dropped, err := a.DroppedEvents("tenantA")
	if err != nil || dropped != 1 {
		t.Fatalf("dropped = %d (err %v), want 1", dropped, err)
	}

	// After a refill the budget allows another delivery.
	clock.advance(time.Second)
	if _, err := a.Publish("tenantA", OpPut, "tenantA/4", nil); err != nil {
		t.Fatalf("post-refill event: %v", err)
	}
	if got := keys(drain(w.Events())); len(got) != 1 || got[0] != "tenantA/4" {
		t.Fatalf("post-refill watch = %v", got)
	}

	// Zero rate disables limiting entirely.
	a.SetLimits(Limits{MaxWatches: 4, EventsPerSecond: 0})
	for i := 0; i < 10; i++ {
		if _, err := a.Publish("tenantA", OpPut, "tenantA/unlimited", nil); err != nil {
			t.Fatalf("unlimited publish %d: %v", i, err)
		}
	}

	w.Close()
}

func TestFullChannelDropsAndCounts(t *testing.T) {
	a := newTestAuthorizer(t,
		WithLimits(Limits{MaxWatches: 4, EventsPerSecond: 0}),
		WithChannelCapacity(1),
	)
	w, err := a.Watch(context.Background(), "tenantA", "tenantA/", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Channel capacity is 1; the second publish cannot enqueue but must
	// not block the service.
	if _, err := a.Publish("tenantA", OpPut, "tenantA/1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Publish("tenantA", OpPut, "tenantA/2", nil); err != nil {
		t.Fatalf("full channel must not fail Publish: %v", err)
	}
	dropped, _ := a.DroppedEvents("tenantA")
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}

	first := <-w.Events()
	if first.Key != "tenantA/1" {
		t.Fatalf("first delivered key = %q", first.Key)
	}
	w.Close()
}
