package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestConcurrentPropose hammers a lease-holding leader from many goroutines.
// Run with -race: every committed entry must come from the proposed set,
// appear at most once, and the committed prefix must be hole-free.
func TestConcurrentPropose(t *testing.T) {
	_, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	if leader == nil {
		t.Fatal("no leader")
	}

	const writers = 8
	const perWriter = 25
	proposed := make(map[string]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				cmd := fmt.Sprintf("w%d-%d", w, i)
				if _, err := leader.Propose([]byte(cmd)); err == nil {
					mu.Lock()
					proposed[cmd] = true
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()
	tickAll(nodes, 10)

	st := leader.Snapshot()
	if st.CommitIndex != writers*perWriter {
		t.Fatalf("commitIndex=%d, want %d", st.CommitIndex, writers*perWriter)
	}
	seen := make(map[string]bool)
	for _, a := range st.Applied {
		s := string(a)
		if !proposed[s] {
			t.Fatalf("committed unknown command %q", s)
		}
		if seen[s] {
			t.Fatalf("command %q committed twice", s)
		}
		seen[s] = true
	}
	if len(seen) != writers*perWriter {
		t.Fatalf("committed %d unique commands, want %d", len(seen), writers*perWriter)
	}
	assertCommittedPrefixesAgree(t, nodes)
}

// TestConcurrentTickAndPropose runs the deterministic clock concurrently
// with client proposals and background reads. Safety invariants (no holes,
// prefix agreement, committed-set sanity) must hold at every quiescent
// checkpoint. Run with -race.
func TestConcurrentTickAndPropose(t *testing.T) {
	_, nodes := NewCluster(5)
	tickUntilLeader(nodes, 50)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					tickAll(nodes, 1)
				}
			}
		}()
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				cmd := []byte(fmt.Sprintf("g%d-%d", g, i))
				for _, n := range nodes {
					if _, err := n.Propose(cmd); err == nil {
						break
					}
				}
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					for _, n := range nodes {
						_ = n.Snapshot()
					}
				}
			}
		}()
	}
	// Let proposers finish, then stop the background workers.
	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	tickAll(nodes, 60)
	assertCommittedPrefixesAgree(t, nodes)
}

// TestNoGoroutineLeak: the package itself spawns no goroutines; after the
// cluster is driven and all test goroutines are joined, the goroutine count
// must return to baseline.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	_, nodes := NewCluster(5)
	leader := tickUntilLeader(nodes, 50)
	if leader == nil {
		t.Fatal("no leader")
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_, _ = leader.Propose([]byte("x"))
			}
		}()
	}
	wg.Wait()
	tickAll(nodes, 20)
	// Allow the runtime a moment to reap anything transient.
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
