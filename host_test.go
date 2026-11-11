package netsec

import (
	"sync"
	"testing"
	"time"
)

// dataRecord captures application-data sends and the real addresses used.
type dataRecord struct {
	path    Path
	payload []byte
}

// TestHostRestrictsSendingUntilActive proves application data is blocked before
// validation and, once allowed, the data callback is invoked with exactly the
// active path's local/peer addresses (no cross-path leakage).
func TestHostRestrictsSendingUntilActive(t *testing.T) {
	h := newHarness(t, 2, time.Second)

	var dataMu sync.Mutex
	var sends []dataRecord
	sendData := func(p Path, payload []byte) error {
		dataMu.Lock()
		defer dataMu.Unlock()
		sends = append(sends, dataRecord{path: p, payload: append([]byte(nil), payload...)})
		return nil
	}
	host := NewHost(h.v, sendData)

	alpha := pathFor(t, 2, "10.0.0.1:4000", "192.0.2.1:443")
	beta := pathFor(t, 2, "10.0.0.2:4000", "192.0.2.1:443")

	if err := h.v.AddCandidate(alpha, h.base.Add(3*time.Second)); err != nil {
		t.Fatalf("AddCandidate alpha: %v", err)
	}
	if err := h.v.AddCandidate(beta, h.base.Add(3*time.Second)); err != nil {
		t.Fatalf("AddCandidate beta: %v", err)
	}

	// Neither validated yet: data callback must never run.
	for _, p := range []Path{alpha, beta} {
		if err := host.SendData(p, []byte("hi")); err == nil {
			t.Fatalf("SendData on unvalidated %v must fail", p)
		}
	}
	dataMu.Lock()
	if len(sends) != 0 {
		dataMu.Unlock()
		t.Fatalf("data sent before validation: %+v", sends)
	}
	dataMu.Unlock()

	// Validate beta only; it becomes active under the default latest strategy.
	probes := h.rec.snapshot()
	betaNonce := probes[1].nonce
	if err := h.v.HandleResponse(2, beta.Local, beta.Peer, betaNonce); err != nil {
		t.Fatalf("validate beta: %v", err)
	}

	// alpha still pending: blocked. beta active: delivered with its addresses.
	if err := host.SendData(alpha, []byte("blocked")); err == nil {
		t.Fatal("SendData on pending alpha must fail")
	}
	payload := []byte("application bytes")
	if err := host.SendData(beta, payload); err != nil {
		t.Fatalf("SendData beta: %v", err)
	}

	dataMu.Lock()
	if len(sends) != 1 {
		dataMu.Unlock()
		t.Fatalf("data sends = %d, want 1", len(sends))
	}
	got := sends[0]
	dataMu.Unlock()

	if got.path != beta {
		t.Fatalf("data callback path = %+v, want beta %+v", got.path, beta)
	}
	if got.path.Local != beta.Local || got.path.Peer != beta.Peer {
		t.Fatalf("data addressed to %v -> %v, want %v -> %v",
			got.path.Local, got.path.Peer, beta.Local, beta.Peer)
	}
	if string(got.payload) != string(payload) {
		t.Fatalf("payload = %q, want %q", got.payload, payload)
	}
}

// TestFakeClockFiresInDeadlineOrder exercises the fake clock directly:
// multiple timers scheduled out of order fire in time order, stopped timers
// never run, and resets re-arm against the simulated clock.
func TestFakeClockFiresInDeadlineOrder(t *testing.T) {
	start := time.Unix(1000, 0)
	clock := NewFakeClock(start)

	var fired []int
	clock.AfterFunc(300*time.Millisecond, func() { fired = append(fired, 300) })
	stopped := clock.AfterFunc(150*time.Millisecond, func() { fired = append(fired, 150) })
	reset := clock.AfterFunc(500*time.Millisecond, func() { fired = append(fired, 100) })

	if !stopped.Stop() {
		t.Fatal("Stop on a pending timer should report true")
	}
	if stopped.Stop() {
		t.Fatal("second Stop should report false")
	}
	// Re-arm the 500ms timer so its original slot is replaced by a 100ms slot.
	reset.Reset(100 * time.Millisecond)

	clock.Advance(time.Second)

	want := []int{100, 300}
	if len(fired) != len(want) {
		t.Fatalf("fired = %v, want %v", fired, want)
	}
	for i := range want {
		if fired[i] != want[i] {
			t.Fatalf("fired = %v, want %v", fired, want)
		}
	}
	if !clock.Now().Equal(start.Add(time.Second)) {
		t.Fatalf("clock now = %v, want %v", clock.Now(), start.Add(time.Second))
	}
	if clock.Pending() != 0 {
		t.Fatalf("pending timers = %d, want 0", clock.Pending())
	}
}

// TestFakeClockAdvanceWithoutTimers confirms time still jumps when nothing is
// scheduled, and a timer scheduled during a callback inside Advance fires in
// the same call if its deadline is within the interval.
func TestFakeClockAdvanceWithoutTimers(t *testing.T) {
	start := time.Unix(0, 0)
	clock := NewFakeClock(start)
	clock.Advance(250 * time.Millisecond)
	if !clock.Now().Equal(start.Add(250 * time.Millisecond)) {
		t.Fatalf("now = %v", clock.Now())
	}

	var chain []int
	var t1 Timer
	t1 = clock.AfterFunc(100*time.Millisecond, func() {
		chain = append(chain, 1)
		// Schedule another timer 50ms later; still inside the 300ms window.
		clock.AfterFunc(50*time.Millisecond, func() { chain = append(chain, 2) })
	})
	_ = t1
	clock.Advance(300 * time.Millisecond)
	want := []int{1, 2}
	if len(chain) != len(want) {
		t.Fatalf("chain = %v, want %v", chain, want)
	}
	for i := range want {
		if chain[i] != want[i] {
			t.Fatalf("chain = %v, want %v", chain, want)
		}
	}
}
