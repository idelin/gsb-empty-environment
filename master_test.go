package quota

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func totalGranted(infos []LeaseInfo) int64 {
	var sum int64
	for _, i := range infos {
		sum += i.Granted
	}
	return sum
}

func leaseOf(infos []LeaseInfo, id string) LeaseInfo {
	for _, i := range infos {
		if i.WorkerID == id {
			return i
		}
	}
	return LeaseInfo{WorkerID: id}
}

func TestRegisterFairSplit(t *testing.T) {
	m := NewMaster(3000, 3, time.Minute)
	for _, id := range []string{"A", "B", "C"} {
		resp, err := m.Register(id)
		if err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
		if resp.Additional != 1000 {
			t.Fatalf("worker %s got %d, want 1000", id, resp.Additional)
		}
	}
	if got := totalGranted(m.Snapshot()); got != 3000 {
		t.Fatalf("total granted = %d, want 3000", got)
	}
	if _, err := m.Register("A"); err == nil {
		t.Fatal("duplicate register should fail")
	}
}

func TestHeartbeatUnknownWorker(t *testing.T) {
	m := NewMaster(3000, 3, time.Minute)
	_, err := m.Heartbeat(HeartbeatRequest{WorkerID: "ghost", Want: 1})
	if !errors.Is(err, ErrUnknownWorker) {
		t.Fatalf("err = %v, want ErrUnknownWorker", err)
	}
}

// The core acceptance scenario: A bursts and exhausts its share, the
// Master pulls idle quota from C and hands it to A.
func TestReclaimFromIdlePeer(t *testing.T) {
	m := NewMaster(3000, 3, time.Minute)
	for _, id := range []string{"A", "B", "C"} {
		if _, err := m.Register(id); err != nil {
			t.Fatal(err)
		}
	}
	// B is moderately busy; C is completely idle, hence the reclaim donor.
	if _, err := m.Heartbeat(HeartbeatRequest{WorkerID: "B", Used: 600, Want: 0}); err != nil {
		t.Fatal(err)
	}
	// A burned its whole 1000-share and asks for a 500-token batch.
	resp, err := m.Heartbeat(HeartbeatRequest{WorkerID: "A", Used: 1000, Want: 500})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Additional != 500 {
		t.Fatalf("A got %d additional tokens, want 500", resp.Additional)
	}
	snap := m.Snapshot()
	if got := leaseOf(snap, "A").Granted; got != 1500 {
		t.Fatalf("A granted = %d, want 1500", got)
	}
	if got := leaseOf(snap, "C").Granted; got != 500 {
		t.Fatalf("C granted = %d, want 500 (reclaimed for A)", got)
	}
	if got := leaseOf(snap, "B").Granted; got != 1000 {
		t.Fatalf("B granted = %d, want 1000 (untouched)", got)
	}
	if got := totalGranted(snap); got != 3000 {
		t.Fatalf("total granted = %d, pool invariant violated (want <= 3000)", got)
	}
}

// Reclaim must never touch tokens a peer has already consumed.
func TestReclaimSkipsUsedTokens(t *testing.T) {
	m := NewMaster(3000, 3, time.Minute)
	for _, id := range []string{"A", "B", "C"} {
		if _, err := m.Register(id); err != nil {
			t.Fatal(err)
		}
	}
	// B consumed 800 of its 1000 (only 200 reclaimable); C is fully busy.
	for _, hb := range []HeartbeatRequest{
		{WorkerID: "B", Used: 800, Want: 0},
		{WorkerID: "C", Used: 1000, Want: 0},
	} {
		if _, err := m.Heartbeat(hb); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := m.Heartbeat(HeartbeatRequest{WorkerID: "A", Used: 1000, Want: 900})
	if err != nil {
		t.Fatal(err)
	}
	// Free pool is empty and only B's 200 unused tokens are reclaimable.
	if resp.Additional != 200 {
		t.Fatalf("A got %d, want 200 (only B's unused balance)", resp.Additional)
	}
	snap := m.Snapshot()
	if got := leaseOf(snap, "B").Granted; got != 800 {
		t.Fatalf("B granted = %d, want 800 (used tokens untouchable)", got)
	}
	if got := leaseOf(snap, "C").Granted; got != 1000 {
		t.Fatalf("C granted = %d, want 1000 (fully consumed lease untouchable)", got)
	}
}

func TestLeaseExpiryReturnsTokens(t *testing.T) {
	var mu sync.Mutex
	cur := time.Now()
	m := NewMaster(3000, 3, time.Second)
	m.SetClock(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return cur
	})
	advance := func(d time.Duration) {
		mu.Lock()
		cur = cur.Add(d)
		mu.Unlock()
	}
	for _, id := range []string{"A", "B", "C"} {
		if _, err := m.Register(id); err != nil {
			t.Fatal(err)
		}
	}
	// A and B keep heartbeating; C dies silently and its lease lapses.
	advance(500 * time.Millisecond)
	for _, id := range []string{"A", "B"} {
		if _, err := m.Heartbeat(HeartbeatRequest{WorkerID: id, Want: 0}); err != nil {
			t.Fatal(err)
		}
	}
	advance(600 * time.Millisecond) // C's lease (t+1s) lapses; A/B valid to t+1.5s
	resp, err := m.Heartbeat(HeartbeatRequest{WorkerID: "A", Used: 1000, Want: 500})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Additional != 500 {
		t.Fatalf("A got %d, want 500 from C's expired lease", resp.Additional)
	}
	snap := m.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 live leases after expiry, got %d", len(snap))
	}
	if got := leaseOf(snap, "A").Granted; got != 1500 {
		t.Fatalf("A granted = %d, want 1500", got)
	}
}

// Concurrent heartbeats must never push total granted beyond the pool.
func TestNeverOverAllocate(t *testing.T) {
	m := NewMaster(3000, 3, time.Minute)
	ids := []string{"A", "B", "C"}
	for _, id := range ids {
		if _, err := m.Register(id); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		for k := 0; k < 8; k++ {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					if _, err := m.Heartbeat(HeartbeatRequest{WorkerID: id, Used: 10, Want: 500}); err != nil {
						t.Error(err)
						return
					}
				}
			}(id)
		}
	}
	wg.Wait()
	if got := totalGranted(m.Snapshot()); got > 3000 {
		t.Fatalf("total granted = %d, exceeds pool of 3000", got)
	}
}
