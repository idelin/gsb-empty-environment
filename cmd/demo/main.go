// Command demo shows the weighted fair queuing scheduler in action.
//
// Three independent scenarios are demonstrated:
//   - saturated tenants are served in proportion to 1:2:3 weights,
//   - changing a weight changes only subsequent selections (1:1 -> 1:2),
//   - cancelling a waiting task removes it without consuming a slot.
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"wfq"
)

const concurrency = 4

// meter runs a saturated workload for dur, pulsing a gate in batches so
// every slot refill is a scheduler decision governed by the weights.
type meter struct {
	s        *wfq.Scheduler
	tick     chan struct{}
	inflight int32
}

func newMeter(tenants map[string]int) *meter {
	s, err := wfq.New(wfq.Config{Concurrency: concurrency, Tenants: tenants})
	if err != nil {
		panic(err)
	}
	return &meter{s: s, tick: make(chan struct{})}
}

// prefill enqueues a fixed, generously large backlog for one tenant
// synchronously, so tenants are provably saturated for the whole window.
func (m *meter) prefill(name string, counter *int64, n int) {
	fn := func(context.Context) error {
		atomic.AddInt64(counter, 1)
		atomic.AddInt32(&m.inflight, 1)
		<-m.tick
		atomic.AddInt32(&m.inflight, -1)
		return nil
	}
	for i := 0; i < n; i++ {
		if _, err := m.s.Submit(name, fn); err != nil {
			panic(err)
		}
	}
}

func (m *meter) pump(dur time.Duration) {
	deadline := time.After(dur)
loop:
	for {
		for atomic.LoadInt32(&m.inflight) < concurrency {
			select {
			case <-deadline:
				break loop
			default:
			}
			time.Sleep(10 * time.Microsecond)
		}
		for i := 0; i < concurrency; i++ {
			select {
			case m.tick <- struct{}{}:
			case <-deadline:
				break loop
			}
		}
	}
}

// drain lets every queued/in-flight task finish and returns once the
// scheduler reports an empty queue and no in-flight work.
func (m *meter) drain() {
	for {
		st := m.s.Stats()
		if st.Queued == 0 && st.Inflight == 0 {
			return
		}
		if st.Inflight > 0 {
			select {
			case m.tick <- struct{}{}:
			default:
				time.Sleep(20 * time.Microsecond)
			}
		} else {
			time.Sleep(20 * time.Microsecond)
		}
	}
}

func (m *meter) stop() {
	// Finish tasks currently in flight, then unblock everything queued so
	// the mandatory drain is cheap.
	for atomic.LoadInt32(&m.inflight) != 0 {
		m.tick <- struct{}{}
	}
	close(m.tick)
	m.s.Shutdown()
}

func main() {
	// Scenario 1: saturated 1:2:3 weights.
	fmt.Println("scenario 1: saturated tenants, weights tier-a:b:c = 1:2:3")
	m1 := newMeter(map[string]int{"tier-a": 1, "tier-b": 2, "tier-c": 3})
	var a1, b1, c1 int64
	const backlog = 20000
	m1.prefill("tier-a", &a1, backlog/6)
	m1.prefill("tier-b", &b1, 2*backlog/6)
	m1.prefill("tier-c", &c1, 3*backlog/6)
	m1.pump(120 * time.Millisecond)
	m1.stop()
	total := a1 + b1 + c1
	fmt.Printf("  served: tier-a=%d tier-b=%d tier-c=%d\n", a1, b1, c1)
	fmt.Printf("  shares: tier-a=%.2f tier-b=%.2f tier-c=%.2f (want 0.17 / 0.33 / 0.50)\n",
		float64(a1)/float64(total), float64(b1)/float64(total), float64(c1)/float64(total))
	fmt.Printf("  peak concurrent running tasks=%d (limit=%d)\n",
		m1.s.Stats().PeakInflight, concurrency)

	// Scenario 2: weights 1:1 first; change to 1:2 and measure only the
	// later window, proving dynamic weights affect subsequent selection.
	fmt.Println()
	fmt.Println("scenario 2: same scheduler, weights tier-a:b change 1:1 -> 1:2")
	m2 := newMeter(map[string]int{"tier-a": 1, "tier-b": 1})
	var a2, b2 int64
	// Window 1: equal queues, equal weights -> 1:1, then fully drain.
	m2.prefill("tier-a", &a2, 3000)
	m2.prefill("tier-b", &b2, 3000)
	m2.pump(60 * time.Millisecond)
	// Fully drain window 1 before changing the weight.
	m2.drain()
	beforeA, beforeB := atomic.LoadInt64(&a2), atomic.LoadInt64(&b2)

	// Same scheduler: change the weight and refill proportional to the new
	// 1:2 ratio so both tenants are saturated during window 2.
	if err := m2.s.UpdateWeight("tier-b", 2); err != nil {
		panic(err)
	}
	fmt.Println(">>> tier-b weight changed 1 -> 2 after window 1 drained")
	m2.prefill("tier-a", &a2, 3000)
	m2.prefill("tier-b", &b2, 6000)
	m2.pump(120 * time.Millisecond)
	m2.stop()
	da, db := a2-beforeA, b2-beforeB
	d := da + db
	fmt.Printf("  window 1 (weights 1:1): tier-a=%d tier-b=%d\n", beforeA, beforeB)
	fmt.Printf("  window 2 (weights 1:2): tier-a=%d tier-b=%d, shares %.2f / %.2f (want 0.33 / 0.67)\n",
		da, db, float64(da)/float64(d), float64(db)/float64(d))

	// Scenario 3: cancel a waiting task while all slots are busy; it must
	// never run and must not consume capacity.
	fmt.Println()
	fmt.Println("scenario 3: cancelling a waiting task consumes no slot")
	s, err := wfq.New(wfq.Config{
		Concurrency: concurrency,
		Tenants:     map[string]int{"tier-a": 1, "tier-b": 2},
	})
	if err != nil {
		panic(err)
	}
	hold := make(chan struct{})
	started := make(chan struct{}, concurrency)
	for i := 0; i < concurrency; i++ {
		name := "tier-a"
		if i%2 == 1 {
			name = "tier-b"
		}
		if _, err := s.Submit(name, func(context.Context) error {
			started <- struct{}{}
			<-hold
			return nil
		}); err != nil {
			panic(err)
		}
	}
	for i := 0; i < concurrency; i++ {
		<-started
	}
	h, err := s.Submit("tier-a", func(context.Context) error {
		fmt.Println("!!! cancelled task ran (should not happen)")
		return nil
	})
	if err != nil {
		panic(err)
	}
	h.Cancel()
	fmt.Println(">>> all slots busy; cancelled a waiting tier-a task")

	queued, err := s.Submit("tier-b", func(context.Context) error { return nil })
	if err != nil {
		panic(err)
	}
	close(hold)
	select {
	case <-queued.Done():
		fmt.Println(">>> freed slot went to the queued tier-b task, not the cancelled one")
	case <-time.After(2 * time.Second):
		panic("queued task was not served")
	}
	s.Shutdown()
	st := s.Stats()
	fmt.Printf("  stats: submitted=%d succeeded=%d failed=%d cancelled=%d peak=%d\n",
		st.Submitted, st.Succeeded, st.Failed, st.Cancelled, st.PeakInflight)
	fmt.Println("scheduler shut down cleanly; no new tasks accepted after Close")
}
