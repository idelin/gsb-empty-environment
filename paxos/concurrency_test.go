package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestConcurrentProposersConsistency hammers a 5-node cluster from many
// goroutines spread across all nodes and asserts the safety invariants:
// every successful proposal is committed at its returned slot, no two
// proposals share a slot, and after a final repair every node converges
// to the same contiguous committed prefix.
func TestConcurrentProposersConsistency(t *testing.T) {
	_, nodes := newCluster(5)
	const workers = 25
	type result struct {
		slot  int
		value string
	}
	var mu sync.Mutex
	var successes []result
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			value := fmt.Sprintf("value-%02d", w)
			slot, err := nodes[w%len(nodes)].Propose(value)
			if err != nil {
				return // lost arbitration; acceptable under contention
			}
			mu.Lock()
			successes = append(successes, result{slot, value})
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	if len(successes) < workers/2 {
		t.Fatalf("only %d/%d proposals succeeded; arbitration livelock suspected", len(successes), workers)
	}
	// No two successful proposals may share a slot.
	seen := make(map[int]string)
	for _, r := range successes {
		if prev, dup := seen[r.slot]; dup {
			t.Fatalf("slot %d committed twice: %q and %q", r.slot, prev, r.value)
		}
		seen[r.slot] = r.value
	}
	// Force a final leader to repair and converge the cluster.
	finalSlot, err := nodes[2].Propose("final")
	if err != nil {
		t.Fatalf("final Propose: %v", err)
	}
	for i, n := range nodes {
		if got := n.CommitIndex(); got != finalSlot {
			t.Fatalf("node %d commitIndex = %d, want %d", i, got, finalSlot)
		}
		// Committed prefix must be contiguous and identical everywhere.
		for slot := 1; slot <= finalSlot; slot++ {
			e, ok := n.LogEntry(slot)
			if !ok || !e.Committed {
				t.Fatalf("node %d slot %d not committed: %+v", i, slot, e)
			}
			if want, known := seen[slot]; known && e.Value != want {
				t.Fatalf("node %d slot %d = %q, want %q", i, slot, e.Value, want)
			}
		}
	}
	// Every acknowledged value must be present at its slot on all nodes.
	for _, r := range successes {
		for i, n := range nodes {
			e, _ := n.LogEntry(r.slot)
			if e.Value != r.value {
				t.Fatalf("node %d slot %d = %q, want acknowledged %q", i, r.slot, e.Value, r.value)
			}
		}
	}
}

// TestConcurrentSameNodeProposals verifies that concurrent proposals on a
// single node are serialized: unique slots, contiguous committed prefix.
func TestConcurrentSameNodeProposals(t *testing.T) {
	_, nodes := newCluster(3)
	leader := nodes[0]
	const proposals = 20
	var wg sync.WaitGroup
	slots := make([]int, proposals)
	for i := 0; i < proposals; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			slot, err := leader.Propose(fmt.Sprintf("v%d", i))
			if err != nil {
				t.Errorf("Propose: %v", err)
				return
			}
			slots[i] = slot
		}(i)
	}
	wg.Wait()
	uniq := make(map[int]bool)
	for _, s := range slots {
		if s <= 0 {
			t.Fatalf("invalid slot %d", s)
		}
		if uniq[s] {
			t.Fatalf("slot %d assigned twice", s)
		}
		uniq[s] = true
	}
	if got := leader.CommitIndex(); got != proposals {
		t.Fatalf("commitIndex = %d, want %d (gap-free commit)", got, proposals)
	}
}

// TestNoGoroutineLeak asserts the engine is fully synchronous: after all
// operations complete, no goroutines remain.
func TestNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()
	_, nodes := newCluster(5)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = nodes[i%5].Propose(fmt.Sprintf("leak-check-%d", i))
		}(i)
	}
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > baseline {
		t.Fatalf("goroutine leak: baseline %d, now %d", baseline, got)
	}
}
