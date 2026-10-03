package quota

import (
	"testing"
	"time"
)

// consume keeps calling Allow until n tokens are consumed or the deadline
// passes, retrying while a renewal heartbeat is in flight.
func consume(w *Worker, n int64, deadline time.Time) int64 {
	var got int64
	for got < n && time.Now().Before(deadline) {
		if w.Allow() {
			got++
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	return got
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// Acceptance boundary: 3 workers share a 3000 QPS pool evenly.
func TestInitialEvenSplit(t *testing.T) {
	m := NewMaster(3000, time.Hour)
	m.RegisterWorkers("A", "B", "C")

	for _, id := range []string{"A", "B", "C"} {
		if g := m.Grant(id); g != 1000 {
			t.Fatalf("worker %s grant = %d, want 1000", id, g)
		}
	}
	if total := m.TotalGrants(); total != 3000 {
		t.Fatalf("total grants = %d, want 3000", total)
	}
}

// Acceptance boundary: worker A bursts through its 1000 quota; a batched
// heartbeat reallocates idle quota from the idle worker C.
func TestBurstTriggersReallocation(t *testing.T) {
	m := NewMaster(3000, time.Hour)
	m.RegisterWorkers("A", "B", "C")
	a := m.NewWorker("A")
	b := m.NewWorker("B")
	c := m.NewWorker("C")
	defer a.Close()
	defer b.Close()
	defer c.Close()

	// B is lightly loaded so that C is the most-idle donor.
	if got := consume(b, 100, time.Now().Add(time.Second)); got != 100 {
		t.Fatalf("B consumed %d, want 100", got)
	}
	// B reports its usage, so the master knows C is the idlest worker.
	m.Heartbeat(HeartbeatRequest{WorkerID: "B", Used: 100, Want: 0})

	// A bursts and exhausts its full 1000-token share.
	deadline := time.Now().Add(2 * time.Second)
	if got := consume(a, 1000, deadline); got != 1000 {
		t.Fatalf("A consumed %d of its initial grant, want 1000", got)
	}

	// The asynchronous heartbeat should top A up from idle C.
	waitFor(t, "A's grant to grow beyond 1000", func() bool {
		return m.Grant("A") > 1000
	})
	if g := m.Grant("C"); g >= 1000 {
		t.Fatalf("C's grant = %d, want < 1000 after donating idle quota", g)
	}
	if total := m.TotalGrants(); total != 3000 {
		t.Fatalf("total grants = %d, want invariant 3000", total)
	}

	// A can keep serving traffic well beyond its original 1000 share.
	if got := consume(a, 500, time.Now().Add(2*time.Second)); got != 500 {
		t.Fatalf("A consumed %d beyond its original share, want 500", got)
	}
	t.Logf("after rebalancing: A=%d B=%d C=%d (total=%d)",
		m.Grant("A"), m.Grant("B"), m.Grant("C"), m.TotalGrants())
}

// Renewal is batched and asynchronous: consuming 1500 tokens must cost
// only a handful of heartbeat RPCs, not one RPC per token.
func TestBatchedHeartbeatKeepsRPCLow(t *testing.T) {
	m := NewMaster(3000, time.Hour)
	m.RegisterWorkers("A", "B", "C")
	a := m.NewWorker("A")
	defer a.Close()
	defer m.NewWorker("B").Close()
	defer m.NewWorker("C").Close()

	const tokens = 1500
	if got := consume(a, tokens, time.Now().Add(3*time.Second)); got != tokens {
		t.Fatalf("A consumed %d, want %d", got, tokens)
	}
	waitFor(t, "in-flight heartbeats to settle", func() bool {
		return m.HeartbeatCount() == a.RPCCount()
	})

	rpcs := a.RPCCount()
	if rpcs == 0 {
		t.Fatal("expected at least one renewal heartbeat")
	}
	if rpcs > 5 {
		t.Fatalf("A sent %d heartbeat RPCs for %d tokens, want batching (<= 5)", rpcs, tokens)
	}
	t.Logf("consumed %d tokens with only %d heartbeat RPCs", tokens, rpcs)
}

// When every worker is busy there is no idle quota to reclaim: a renewal
// must not create tokens out of thin air.
func TestNoReallocationWhenPoolExhausted(t *testing.T) {
	m := NewMaster(3000, time.Hour)
	m.RegisterWorkers("A", "B", "C")

	// B and C report full consumption of their shares.
	m.Heartbeat(HeartbeatRequest{WorkerID: "B", Used: 1000, Want: 0})
	m.Heartbeat(HeartbeatRequest{WorkerID: "C", Used: 1000, Want: 0})

	// A has also consumed its share and asks for more.
	resp := m.Heartbeat(HeartbeatRequest{WorkerID: "A", Used: 1000, Want: 1000})
	if resp.Grant != 1000 {
		t.Fatalf("A's grant = %d, want 1000 (no idle quota anywhere)", resp.Grant)
	}
	if total := m.TotalGrants(); total != 3000 {
		t.Fatalf("total grants = %d, want invariant 3000", total)
	}
}

// The window slides: consumption counters reset, so reclaimed quota
// becomes available to its original owner again in the next window.
func TestWindowRotationRestoresIdleQuota(t *testing.T) {
	m := NewMaster(3000, 50*time.Millisecond)
	m.RegisterWorkers("A", "B", "C")

	// Window 1: C is idle, A bursts and absorbs C's share.
	m.Heartbeat(HeartbeatRequest{WorkerID: "A", Used: 1000, Want: 1000})
	if g := m.Grant("A"); g != 2000 {
		t.Fatalf("window 1: A's grant = %d, want 2000", g)
	}

	// Window 2: B and C report fresh usage; their idle quota is no
	// longer reclaimable, so A's renewal finds nothing to take.
	time.Sleep(60 * time.Millisecond)
	m.Heartbeat(HeartbeatRequest{WorkerID: "B", Used: 1000, Want: 0})
	m.Heartbeat(HeartbeatRequest{WorkerID: "C", Used: 1000, Want: 0})
	resp := m.Heartbeat(HeartbeatRequest{WorkerID: "A", Used: 2000, Want: 1000})
	if resp.Grant != 2000 {
		t.Fatalf("window 2: A's grant = %d, want 2000 (C is busy now)", resp.Grant)
	}
	if total := m.TotalGrants(); total != 3000 {
		t.Fatalf("total grants = %d, want invariant 3000", total)
	}
}
