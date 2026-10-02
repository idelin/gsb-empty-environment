package hotring

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func nodeNames(n int) []string {
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		names = append(names, fmt.Sprintf("node-%d", i))
	}
	return names
}

func sampleKeys(n int) []string {
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		keys = append(keys, fmt.Sprintf("key-%d", i))
	}
	return keys
}

func TestFlipRequiresWarmedStandby(t *testing.T) {
	c := NewController(nodeNames(4), 64)
	if _, err := c.Flip(); err != ErrNoStandby {
		t.Fatalf("flip without standby: expected ErrNoStandby, got %v", err)
	}
	c.PrepareStandby(nodeNames(3))
	if _, err := c.Flip(); err != ErrNoStandby {
		t.Fatalf("flip without warmup: expected ErrNoStandby, got %v", err)
	}
}

func TestWarmupPrecomputesMigration(t *testing.T) {
	oldTopo := nodeNames(6)
	newTopo := nodeNames(5) // scale-in: node-5 removed
	c := NewController(oldTopo, 160)
	keys := sampleKeys(20000)

	c.PrepareStandby(newTopo)
	report, err := c.Warmup(keys)
	if err != nil {
		t.Fatal(err)
	}

	// Independently recompute the expected impact.
	wantGain := map[string]int{}
	wantMoved := 0
	before, after := NewRing(oldTopo, 160), NewRing(newTopo, 160)
	for _, k := range keys {
		a, _ := before.Get(k)
		b, _ := after.Get(k)
		if a != b {
			wantMoved++
			wantGain[b]++
		}
	}
	if report.MovedKeys != wantMoved {
		t.Fatalf("MovedKeys = %d, want %d", report.MovedKeys, wantMoved)
	}
	for node, n := range wantGain {
		if report.GainedBy[node] != n {
			t.Fatalf("GainedBy[%s] = %d, want %d", node, report.GainedBy[node], n)
		}
	}
	// The removed node must have lost every key it held.
	if report.LostBy["node-5"] == 0 {
		t.Fatal("expected removed node-5 to lose keys")
	}
	t.Logf("warmup report: %d/%d keys move (%.2f%%)",
		report.MovedKeys, report.SampledKeys, report.MovedRatio()*100)
}

func TestFlipSwitchesRouting(t *testing.T) {
	oldTopo := nodeNames(6)
	newTopo := nodeNames(5)
	c := NewController(oldTopo, 160)
	keys := sampleKeys(5000)

	c.PrepareStandby(newTopo)
	if _, err := c.Warmup(keys); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Flip(); err != nil {
		t.Fatal(err)
	}

	// After the flip, every route must match the new topology exactly.
	newRing := NewRing(newTopo, 160)
	for _, k := range keys {
		got, err := c.Route(k)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := newRing.Get(k)
		if got != want {
			t.Fatalf("post-flip route(%q) = %q, want %q", k, got, want)
		}
	}
	if c.StandbyRing() != nil {
		t.Fatal("standby should be cleared after flip")
	}
	flips, last := c.Stats()
	if flips != 1 || last == 0 {
		t.Fatalf("stats = (%d, %d), want (1, >0)", flips, last)
	}
}

// TestAtomicFlipUnderTraffic is the acceptance scenario: while the
// active ring serves full traffic, a scale-in change is injected into
// the standby ring, warmed up, and flipped in. Concurrent readers must
// never observe an error or a node outside old∪new topology — zero
// transient misrouting.
func TestAtomicFlipUnderTraffic(t *testing.T) {
	oldTopo := nodeNames(6)
	newTopo := nodeNames(5)
	c := NewController(oldTopo, 160)

	valid := map[string]bool{}
	for _, n := range append(append([]string{}, oldTopo...), newTopo...) {
		valid[n] = true
	}

	var stop int32
	var routes, routeErrs, badNodes int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; atomic.LoadInt32(&stop) == 0; i++ {
				node, err := c.Route(fmt.Sprintf("w%d-key-%d", id, i))
				if err != nil {
					atomic.AddInt64(&routeErrs, 1)
					continue
				}
				if !valid[node] {
					atomic.AddInt64(&badNodes, 1)
				}
				atomic.AddInt64(&routes, 1)
			}
		}(w)
	}

	// Inject the scale-in change into the standby ring while traffic flows.
	c.PrepareStandby(newTopo)
	if _, err := c.Warmup(sampleKeys(10000)); err != nil {
		t.Fatal(err)
	}

	// Atomic pointer flip — traffic switches instantly.
	start := time.Now()
	if _, err := c.Flip(); err != nil {
		t.Fatal(err)
	}
	flipCost := time.Since(start)

	// Let post-flip traffic flow briefly, then stop readers.
	time.Sleep(20 * time.Millisecond)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()

	if routeErrs != 0 {
		t.Fatalf("observed %d routing errors during flip", routeErrs)
	}
	if badNodes != 0 {
		t.Fatalf("observed %d routes to nodes outside old∪new topology", badNodes)
	}
	if routes == 0 {
		t.Fatal("no traffic was routed")
	}
	t.Logf("%d routes served across flip, 0 errors, 0 misroutes; flip took %s",
		routes, flipCost)

	// Post-flip routing must exactly match the new ring.
	newRing := NewRing(newTopo, 160)
	for i := 0; i < 1000; i++ {
		k := fmt.Sprintf("post-%d", i)
		got, _ := c.Route(k)
		want, _ := newRing.Get(k)
		if got != want {
			t.Fatalf("post-flip route(%q) = %q, want %q", k, got, want)
		}
	}
}

// TestFlipIsMillisecondLevel verifies the pointer flip itself is fast.
func TestFlipIsMillisecondLevel(t *testing.T) {
	c := NewController(nodeNames(64), 160)
	c.PrepareStandby(nodeNames(63))
	if _, err := c.Warmup(sampleKeys(100000)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := c.Flip(); err != nil {
		t.Fatal(err)
	}
	cost := time.Since(start)
	t.Logf("flip completed in %s", cost)
	if cost > 10*time.Millisecond {
		t.Fatalf("flip took %s, want millisecond-level", cost)
	}
}

// TestPrepareStandbyDoesNotDisturbActive ensures building a standby
// ring never changes active-ring routing.
func TestPrepareStandbyDoesNotDisturbActive(t *testing.T) {
	topo := nodeNames(6)
	c := NewController(topo, 160)
	keys := sampleKeys(2000)
	before := map[string]string{}
	for _, k := range keys {
		n, _ := c.Route(k)
		before[k] = n
	}
	c.PrepareStandby(nodeNames(3)) // drastic change staged
	for _, k := range keys {
		n, _ := c.Route(k)
		if n != before[k] {
			t.Fatalf("active route for %q changed from %q to %q during standby build", k, before[k], n)
		}
	}
}
