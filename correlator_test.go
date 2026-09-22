package correlator

import (
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for deterministic tests.
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTestCorrelator(f *fakeClock, timeout time.Duration, maxPending, maxTombstones int) *Correlator {
	return New(Config{
		Timeout:       timeout,
		MaxPending:    maxPending,
		MaxTombstones: maxTombstones,
		Now:           f.now,
	})
}

func TestMatchLatency(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Second, 8, 8)

	if ev := c.HandleRequest(1, 1); ev.Kind != EventAccepted {
		t.Fatalf("request: %v", ev)
	}
	f.advance(120 * time.Millisecond)
	ev := c.HandleResponse(1, 1)
	if ev.Kind != EventMatched {
		t.Fatalf("response: %v", ev)
	}
	if ev.Latency != 120*time.Millisecond {
		t.Fatalf("latency = %v, want 120ms", ev.Latency)
	}
	if c.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", c.Pending())
	}
}

func TestOutOfOrderResponses(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Minute, 8, 8)

	for seq := uint64(1); seq <= 3; seq++ {
		c.HandleRequest(1, seq)
		f.advance(10 * time.Millisecond)
	}
	// Respond in reverse order.
	for _, seq := range []uint64{3, 2, 1} {
		f.advance(10 * time.Millisecond)
		if ev := c.HandleResponse(1, seq); ev.Kind != EventMatched {
			t.Fatalf("seq %d: %v", seq, ev)
		}
	}
}

func TestDuplicateRequestKeepsOriginalStart(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Minute, 8, 8)

	c.HandleRequest(1, 1)
	f.advance(500 * time.Millisecond)
	if ev := c.HandleRequest(1, 1); ev.Kind != EventDuplicateRequest {
		t.Fatalf("replayed request: %v", ev)
	}
	f.advance(100 * time.Millisecond)
	ev := c.HandleResponse(1, 1)
	if ev.Kind != EventMatched {
		t.Fatalf("response: %v", ev)
	}
	// Latency must be measured from the ORIGINAL start, not the replay.
	if ev.Latency != 600*time.Millisecond {
		t.Fatalf("latency = %v, want 600ms (replay must not refresh start)", ev.Latency)
	}
}

func TestTimeoutThenResponseIsLate(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Second, 8, 8)

	c.HandleRequest(1, 1)
	f.advance(2 * time.Second)
	events := c.Expire()
	if len(events) != 1 || events[0].Kind != EventExpired {
		t.Fatalf("Expire: %v", events)
	}
	if ev := c.HandleResponse(1, 1); ev.Kind != EventLate {
		t.Fatalf("response after timeout: %v, want EventLate", ev)
	}
}

func TestOrphanResponse(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Second, 8, 8)

	if ev := c.HandleResponse(1, 42); ev.Kind != EventOrphan {
		t.Fatalf("unknown response: %v, want EventOrphan", ev)
	}
}

func TestOldEpochCannotMatchNewRequest(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Minute, 8, 8)

	// Same seq, new epoch: a straggler from epoch 1 must not pair with it.
	c.HandleRequest(2, 1)
	if ev := c.HandleResponse(1, 1); ev.Kind != EventOrphan {
		t.Fatalf("old-epoch response: %v, want EventOrphan", ev)
	}
	if c.Pending() != 1 {
		t.Fatalf("pending = %d, want 1 (new request untouched)", c.Pending())
	}
	if ev := c.HandleResponse(2, 1); ev.Kind != EventMatched {
		t.Fatalf("same-epoch response: %v, want EventMatched", ev)
	}
}

func TestSeqReuseAfterCompletion(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Minute, 8, 8)

	c.HandleRequest(1, 1)
	f.advance(50 * time.Millisecond)
	c.HandleResponse(1, 1)

	// Seq 1 is reused: tracked as a fresh request, not a duplicate.
	f.advance(10 * time.Millisecond)
	if ev := c.HandleRequest(1, 1); ev.Kind != EventAccepted {
		t.Fatalf("reused seq: %v, want EventAccepted", ev)
	}
	f.advance(30 * time.Millisecond)
	ev := c.HandleResponse(1, 1)
	if ev.Kind != EventMatched || ev.Latency != 30*time.Millisecond {
		t.Fatalf("second round: %v, want matched with 30ms latency", ev)
	}
}

func TestSeqReuseAfterExpiry(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Second, 8, 8)

	c.HandleRequest(1, 1)
	f.advance(2 * time.Second)
	c.Expire() // seq 1 is now a tombstone

	// Reusing the expired seq starts a fresh request.
	if ev := c.HandleRequest(1, 1); ev.Kind != EventAccepted {
		t.Fatalf("reuse after expiry: %v, want EventAccepted", ev)
	}
	// And a response matches the NEW request, not the expired one.
	f.advance(10 * time.Millisecond)
	if ev := c.HandleResponse(1, 1); ev.Kind != EventMatched {
		t.Fatalf("response after reuse: %v, want EventMatched", ev)
	}
}

func TestBoundedPending(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Minute, 2, 2)

	c.HandleRequest(1, 1)
	c.HandleRequest(1, 2)
	if ev := c.HandleRequest(1, 3); ev.Kind != EventRejected {
		t.Fatalf("third request: %v, want EventRejected", ev)
	}
	// Completing one frees a slot.
	c.HandleResponse(1, 1)
	if ev := c.HandleRequest(1, 3); ev.Kind != EventAccepted {
		t.Fatalf("after completion: %v, want EventAccepted", ev)
	}
}

func TestBoundedTombstones(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Second, 8, 1) // only 1 tombstone retained

	c.HandleRequest(1, 1)
	c.HandleRequest(1, 2)
	f.advance(2 * time.Second)
	if events := c.Expire(); len(events) != 2 {
		t.Fatalf("Expire: %v", events)
	}
	// The oldest tombstone was evicted: its response is now an orphan,
	// while the retained one is still recognized as late.
	if ev := c.HandleResponse(1, 1); ev.Kind != EventOrphan {
		t.Fatalf("evicted tombstone: %v, want EventOrphan", ev)
	}
	if ev := c.HandleResponse(1, 2); ev.Kind != EventLate {
		t.Fatalf("retained tombstone: %v, want EventLate", ev)
	}
}

func TestCheckpointRoundTrip(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Second, 8, 8)

	c.HandleRequest(1, 2) // will expire before the checkpoint
	f.advance(2 * time.Second)
	c.Expire()
	c.HandleRequest(1, 1) // stays pending across the checkpoint
	f.advance(500 * time.Millisecond)

	data, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Restore into a fresh correlator (simulating a restart) with the
	// same logical clock.
	c2 := newTestCorrelator(f, time.Second, 8, 8)
	if err := c2.Restore(data); err != nil {
		t.Fatal(err)
	}
	if c2.Pending() != 1 {
		t.Fatalf("pending after restore = %d, want 1", c2.Pending())
	}

	// Latency is still measured from the ORIGINAL start time.
	f.advance(100 * time.Millisecond)
	ev := c2.HandleResponse(1, 1)
	if ev.Kind != EventMatched || ev.Latency != 600*time.Millisecond {
		t.Fatalf("restored match: %v, want matched with 600ms latency", ev)
	}
	// Tombstones survive too: the expired request's response is late.
	if ev := c2.HandleResponse(1, 2); ev.Kind != EventLate {
		t.Fatalf("restored tombstone: %v, want EventLate", ev)
	}
}

func TestRestoreRejectsBadCheckpoint(t *testing.T) {
	f := &fakeClock{t: time.Unix(1000, 0)}
	c := newTestCorrelator(f, time.Second, 2, 2)

	if err := c.Restore([]byte("not json")); err == nil {
		t.Fatal("garbage checkpoint: want error")
	}
	// Over the configured bound.
	over := []byte(`{"pending":[
		{"epoch":1,"seq":1,"start_unix_nano":0},
		{"epoch":1,"seq":2,"start_unix_nano":0},
		{"epoch":1,"seq":3,"start_unix_nano":0}],"tombstones":[]}`)
	if err := c.Restore(over); err == nil {
		t.Fatal("over-limit checkpoint: want error")
	}
	// Duplicate keys.
	dup := []byte(`{"pending":[
		{"epoch":1,"seq":1,"start_unix_nano":0},
		{"epoch":1,"seq":1,"start_unix_nano":0}],"tombstones":[]}`)
	if err := c.Restore(dup); err == nil {
		t.Fatal("duplicate keys: want error")
	}
}
