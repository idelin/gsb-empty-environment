package prefetch

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestBoundedScheduleExploration drives a fixed scenario through every
// completion ordering of the gated loads (a bounded exploration of the
// schedule space) and asserts the invariants at every controlled step:
//
//   - prefetch concurrency never exceeds maxTotal-fgReserve (quota)
//   - total concurrency never exceeds maxTotal
//   - a foreground request starts on its reserved slot even while
//     prefetch is in flight and more prefetch is queued (no starvation)
//   - a real request for a prefetch-in-flight key merges into one load
func TestBoundedScheduleExploration(t *testing.T) {
	const (
		maxTotal  = 2
		fgReserve = 1
		pfCap     = maxTotal - fgReserve
	)
	orders := [][]string{{"p1", "f1"}, {"f1", "p1"}}
	for _, order := range orders {
		t.Run(fmt.Sprintf("complete_%s_then_%s", order[0], order[1]), func(t *testing.T) {
			gs := newGateSource()
			c := NewCache(RealClock(), 8, 8, 0)
			s := NewScheduler(gs, c, maxTotal, fgReserve)
			defer s.Close()

			checkQuotas := func(what string) {
				t.Helper()
				st := s.Stats()
				if st.PFRunning > pfCap {
					t.Fatalf("%s: prefetch quota exceeded: %+v", what, st)
				}
				if st.FGRunning+st.PFRunning > maxTotal {
					t.Fatalf("%s: total quota exceeded: %+v", what, st)
				}
			}

			s.Prefetch(0, "p1", "p2")
			waitFor(t, "p1 started", func() bool { return gs.startedCount("p1") == 1 })
			checkQuotas("after prefetch")
			if n := gs.startedCount("p2"); n != 0 {
				t.Fatalf("p2 must wait for a prefetch slot, started %d", n)
			}

			type got struct {
				v   Value
				err error
			}
			f1Res := make(chan got, 1)
			go func() {
				v, err := s.Get(context.Background(), "f1")
				f1Res <- got{v, err}
			}()
			p1Res := make(chan got, 1)
			go func() {
				v, err := s.Get(context.Background(), "p1") // merges with prefetch
				p1Res <- got{v, err}
			}()

			// No gate opened: f1 must still start (reserved slot).
			waitFor(t, "f1 started on reserved slot", func() bool { return gs.startedCount("f1") == 1 })
			checkQuotas("foreground joined")
			if n := gs.startedCount("p2"); n != 0 {
				t.Fatal("p2 still must not start")
			}

			for _, k := range order {
				gs.open(k)
				waitFor(t, k+" completed", func() bool {
					gs.mu.Lock()
					defer gs.mu.Unlock()
					return gs.done[k] == 1
				})
				checkQuotas("after " + k)
			}

			for _, ch := range []chan got{f1Res, p1Res} {
				select {
				case r := <-ch:
					if r.err != nil {
						t.Fatalf("foreground Get failed: %v", r.err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("foreground waiter starved")
				}
			}
			if n := gs.startedCount("p1"); n != 1 {
				t.Fatalf("p1 must be loaded exactly once (merged), got %d", n)
			}

			// Slots free: queued prefetch p2 resumes within its quota.
			waitFor(t, "p2 started", func() bool { return gs.startedCount("p2") == 1 })
			checkQuotas("p2 resumed")
			gs.open("p2")
			waitFor(t, "drained", func() bool { return s.Stats().Tasks == 0 })
			checkQuotas("drained")
		})
	}
}
