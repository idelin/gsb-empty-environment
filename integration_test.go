package quota

import (
	"strings"
	"testing"
	"time"
)

// directHB adapts Master to Heartbeater in-process (no network).
type directHB struct {
	m       *Master
	initial bool
}

func (d *directHB) Heartbeat(req HeartbeatRequest) (HeartbeatResponse, error) {
	if d.initial {
		d.initial = false
		return d.m.Register(req.WorkerID)
	}
	return d.m.Heartbeat(req)
}

// burst drives w until target tokens are consumed, retrying while a
// renewal is in flight (Allow fails closed when the balance hits zero).
func burst(t *testing.T, w *Worker, target int64) int64 {
	t.Helper()
	var consumed int64
	for attempts := 0; consumed < target; attempts++ {
		if attempts > 200000 {
			t.Fatalf("worker stalled after %d tokens", consumed)
		}
		if w.Allow() {
			consumed++
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	w.Wait()
	return consumed
}

// Full acceptance scenario: three workers split a 3000 QPS pool; A
// bursts, exhausts its 1000 share, and its batched heartbeats pull idle
// quota away from C. Runs against the in-process transport.
func TestBurstRebalancesAcrossCluster(t *testing.T) {
	master := NewMaster(3000, 3, 10*time.Second)
	mk := func(id string) *Worker {
		return NewWorker(id, &directHB{m: master, initial: true}, 200, 0.2)
	}
	A, B, C := mk("A"), mk("B"), mk("C")
	for _, w := range []*Worker{A, B, C} {
		if err := w.Register(); err != nil {
			t.Fatalf("register: %v", err)
		}
	}

	burst(t, B, 200) // B moderately busy; C stays completely idle.
	B.Flush()        // B reports its usage, so only C looks reclaimable-idle.
	consumed := burst(t, A, 1500)

	if consumed != 1500 {
		t.Fatalf("A consumed %d, want 1500 (its initial share was only 1000)", consumed)
	}
	snap := master.Snapshot()
	a, b, c := leaseOf(snap, "A"), leaseOf(snap, "B"), leaseOf(snap, "C")
	if a.Granted <= 1000 {
		t.Fatalf("A granted = %d, want > 1000 after rebalancing", a.Granted)
	}
	if c.Granted >= 1000 {
		t.Fatalf("C granted = %d, want < 1000 (idle quota reclaimed)", c.Granted)
	}
	if b.Granted != 1000 {
		t.Fatalf("B granted = %d, want 1000 (active peer untouched)", b.Granted)
	}
	if got := totalGranted(snap); got > 3000 {
		t.Fatalf("total granted = %d, pool invariant violated", got)
	}
	// Batching: ~1700 tokens moved with only a handful of RPCs.
	if hb := master.Stats(); hb == 0 || hb > 30 {
		t.Fatalf("master served %d heartbeats, want a small non-zero count", hb)
	}
	t.Logf("A consumed %d tokens; master heartbeats=%d; leases=%v", consumed, master.Stats(), snap)
}

// The same scenario over a real TCP socket through the HTTP transport.
// Skipped in sandboxes that forbid binding listeners.
func TestBurstRebalancesOverTCP(t *testing.T) {
	master := NewMaster(3000, 3, 10*time.Second)
	ln, err := ServeHTTP(master, "127.0.0.1:0")
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("sandbox forbids listeners: %v", err)
		}
		t.Fatal(err)
	}
	defer ln.Close()
	mk := func(id string) *Worker {
		return NewWorker(id, DialMaster(ln.Addr().String()), 200, 0.2)
	}
	A, C := mk("A"), mk("C")
	for _, w := range []*Worker{A, C, mk("B")} {
		if err := w.Register(); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	burst(t, A, 1500)
	snap := master.Snapshot()
	if a := leaseOf(snap, "A"); a.Granted <= 1000 {
		t.Fatalf("A granted = %d, want > 1000 after rebalancing", a.Granted)
	}
	if c := leaseOf(snap, "C"); c.Granted >= 1000 {
		t.Fatalf("C granted = %d, want < 1000 (idle quota reclaimed)", c.Granted)
	}
}

// Local consumption must dominate RPC traffic: below the watermark no
// heartbeat is sent at all.
func TestLocalConsumptionDominatesRPC(t *testing.T) {
	master := NewMaster(3000, 3, time.Minute)
	mk := func(id string) *Worker { return NewWorker(id, &directHB{m: master, initial: true}, 200, 0.2) }
	A := mk("A")
	for _, id := range []string{"B", "C"} {
		if err := mk(id).Register(); err != nil {
			t.Fatal(err)
		}
	}
	if err := A.Register(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 700; i++ { // balance stays above the 20% watermark
		if !A.Allow() {
			t.Fatalf("Allow() = false at %d with local tokens left", i)
		}
	}
	A.Wait()
	if hb := master.Stats(); hb != 0 {
		t.Fatalf("heartbeats = %d, want 0: local tokens should suffice", hb)
	}
}

// The HTTP transport surfaces connection failures as errors.
func TestDialMasterUnreachable(t *testing.T) {
	h := DialMaster("127.0.0.1:1") // nothing listening
	if _, err := h.Heartbeat(HeartbeatRequest{WorkerID: "X"}); err == nil {
		t.Fatal("expected connection error")
	}
}
