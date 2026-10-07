package ttlcache

import (
	"reflect"
	"testing"
	"time"
)

func TestBasicSetGetExpire(t *testing.T) {
	c := New()
	if _, err := c.Set("a", 1, 0); err != ErrInvalidTTL {
		t.Fatalf("expected ErrInvalidTTL, got %v", err)
	}
	if _, err := c.Set("a", 1, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%v, %v)", v, ok)
	}
	fired := c.Advance(10 * time.Second)
	if len(fired) != 1 || fired[0].Kind != EventExpire || fired[0].Key != "a" {
		t.Fatalf("expected one expire event for a, got %v", fired)
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("a should have expired")
	}
}

func TestUpdateGenerationsStaleExpiryCannotDeleteNewValue(t *testing.T) {
	c := New()
	gen1, _ := c.Set("k", "old", 10*time.Second)
	c.Advance(5 * time.Second)                   // t=5
	gen2, _ := c.Set("k", "new", 10*time.Second) // new generation, expires at t=15
	if gen2 <= gen1 {
		t.Fatalf("expected increasing generations, got %d then %d", gen1, gen2)
	}

	// t=11: generation 1's deadline (t=10) passes. Its expiry must not
	// remove the generation-2 value.
	fired := c.Advance(6 * time.Second)
	if len(fired) != 0 {
		t.Fatalf("gen-1 was overwritten, no expiry should fire, got %v", fired)
	}
	if v, ok := c.Get("k"); !ok || v != "new" {
		t.Fatalf("new value must survive old generation's deadline, got (%v, %v)", v, ok)
	}

	// t=15: generation 2 expires.
	fired = c.Advance(4 * time.Second)
	if len(fired) != 1 || fired[0].Gen != gen2 {
		t.Fatalf("expected expire of gen %d, got %v", gen2, fired)
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("k should have expired at t=15")
	}

	// Event replay must reconstruct the same history: replaying the log up
	// to t=11 keeps "new"; replaying to t=15 drops it.
	events := c.Events()
	if got := Replay(events, 11*time.Second); !reflect.DeepEqual(got, map[string]interface{}{"k": "new"}) {
		t.Fatalf("replay at t=11 = %v, want k=new", got)
	}
	if got := Replay(events, 15*time.Second); len(got) != 0 {
		t.Fatalf("replay at t=15 = %v, want empty", got)
	}
}

func TestStaleDeleteEventCannotDeleteNewValue(t *testing.T) {
	c := New()
	gen1, _ := c.Set("k", "v1", time.Minute)
	c.Delete("k")
	c.Set("k", "v2", time.Minute)

	// Simulate a stale delete event for gen1 arriving late (e.g. from a
	// delayed replication stream): replay must ignore it.
	events := append(c.Events(), Event{Kind: EventDelete, Key: "k", Gen: gen1, At: c.Now()})
	if got := Replay(events, c.Now()); !reflect.DeepEqual(got, map[string]interface{}{"k": "v2"}) {
		t.Fatalf("stale delete removed new value: %v", got)
	}
}

func TestSnapshotRestoreDeductsDowntime(t *testing.T) {
	c := New()
	c.Set("short", "s", 5*time.Second)
	c.Set("long", "l", 30*time.Second)
	c.Advance(2 * time.Second) // t=2: short has 3s left, long has 28s left

	snap := c.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 records, got %d", len(snap))
	}
	for _, r := range snap {
		if r.SavedAt != 2*time.Second {
			t.Fatalf("record %s SavedAt = %v, want 2s", r.Key, r.SavedAt)
		}
		want := map[string]time.Duration{"short": 3 * time.Second, "long": 28 * time.Second}
		if r.Remaining != want[r.Key] {
			t.Fatalf("record %s Remaining = %v, want %v", r.Key, r.Remaining, want[r.Key])
		}
	}

	// Process stops for 4s. "short" had 3s left: it must expire immediately
	// on restore, not be re-granted its full 5s TTL.
	r := Restore(snap, 4*time.Second)
	if _, ok := r.Get("short"); ok {
		t.Fatal("short must be expired immediately after restore")
	}
	v, ok := r.Get("long")
	if !ok || v != "l" {
		t.Fatalf("long should survive with 24s left, got (%v, %v)", v, ok)
	}

	// "long" must expire 24s after restore (28s remaining - 4s downtime),
	// proving the TTL was not restarted.
	fired := r.Advance(24 * time.Second)
	if len(fired) != 1 || fired[0].Key != "long" {
		t.Fatalf("expected long to expire 24s after restore, got %v", fired)
	}

	// The immediate expiry of "short" must appear in the restored log.
	found := false
	for _, ev := range r.Events() {
		if ev.Kind == EventExpire && ev.Key == "short" {
			found = true
		}
	}
	if !found {
		t.Fatal("restored cache must log the immediate expiry of short")
	}
}

func TestRestorePreservesGenerations(t *testing.T) {
	c := New()
	gen1, _ := c.Set("k", "v1", time.Minute)
	snap := c.Snapshot()

	r := Restore(snap, 0)
	gen2, err := r.Set("k", "v2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if gen2 <= gen1 {
		t.Fatalf("generation counter must resume above restored gen %d, got %d", gen1, gen2)
	}

	// A pre-stop expire event for gen1 replayed against the restored log
	// must not delete the gen-2 value.
	events := append([]Event{{Kind: EventExpire, Key: "k", Gen: gen1, At: time.Minute}}, r.Events()...)
	if got := Replay(events, r.Now()); !reflect.DeepEqual(got, map[string]interface{}{"k": "v2"}) {
		t.Fatalf("pre-stop expire deleted new value: %v", got)
	}
}

func TestReplayMatchesLiveState(t *testing.T) {
	c := New()
	c.Set("a", 1, 5*time.Second)
	c.Set("b", 2, 9*time.Second)
	c.Advance(3 * time.Second)
	c.Set("a", 3, 5*time.Second) // a gen 2, expires t=8
	c.Delete("b")
	c.Set("c", 4, 20*time.Second)
	c.Advance(6 * time.Second) // t=9: a gen 2 expires at t=8
	c.Set("d", 5, time.Second)
	c.Advance(500 * time.Millisecond) // t=9.5

	live := c.Items()
	replayed := Replay(c.Events(), c.Now())
	if !reflect.DeepEqual(live, replayed) {
		t.Fatalf("replay mismatch:\nlive:     %v\nreplayed: %v", live, replayed)
	}
}
