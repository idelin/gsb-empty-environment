package canonkey

import (
	"encoding/json"
	"testing"
	"time"
)

func TestClockInjectionAndTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	store := NewStore(WithClock(clock), WithTTL(time.Hour), WithSweepInterval(time.Millisecond))
	defer store.Close()

	k := NewKeyer(Limits{}).MustBuildKey(req("acme", "cfg-v1", map[string]any{"x": json.Number("1")}))
	if err := store.Put(k, "v"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(k); !ok {
		t.Fatal("entry should be live before expiry")
	}
	now = now.Add(2 * time.Hour) // advance the injected clock
	if _, ok := store.Get(k); ok {
		t.Fatal("entry must expire under the injected clock")
	}
}

func TestCloseReleasesResources(t *testing.T) {
	store := NewStore(WithTTL(time.Minute), WithSweepInterval(time.Millisecond))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("sweeper goroutine was not released by Close")
	}
	// Close is idempotent; use-after-close is a defined error.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	k := NewKeyer(Limits{}).MustBuildKey(req("acme", "cfg-v1", nil))
	if err := store.Put(k, "v"); err != ErrClosed {
		t.Fatalf("put after close: %v", err)
	}
	if _, ok := store.Get(k); ok {
		t.Fatal("get after close must miss")
	}
}
