package hashring

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestController(t *testing.T, n, zones int) *Controller {
	t.Helper()
	b := NewBuilder(160)
	for _, nd := range testNodes(n, zones) {
		if err := b.Add(nd); err != nil {
			t.Fatal(err)
		}
	}
	c, err := NewController(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewControllerRequiresNodes(t *testing.T) {
	if _, err := NewController(NewBuilder(8)); err == nil {
		t.Fatal("expected error for empty initial topology")
	}
}

func TestFlipWithoutPrewarmFails(t *testing.T) {
	c := newTestController(t, 6, 3)
	if _, err := c.Flip(); err == nil {
		t.Fatal("Flip without Prewarm must fail")
	}
	if _, err := c.Prewarm(100); err == nil {
		t.Fatal("Prewarm without staged changes must fail")
	}
}

func TestSyncRemoveUnknownNodeFails(t *testing.T) {
	c := newTestController(t, 6, 3)
	if err := c.SyncRemove("ghost"); err == nil {
		t.Fatal("SyncRemove of unknown node must fail")
	}
}

func TestPrewarmPlanAccuracy(t *testing.T) {
	c := newTestController(t, 12, 3)
	if err := c.SyncRemove("n03"); err != nil {
		t.Fatal(err)
	}
	plan, err := c.Prewarm(50000)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.RemovedNodes) != 1 || plan.RemovedNodes[0] != "n03" {
		t.Fatalf("plan.RemovedNodes = %v", plan.RemovedNodes)
	}
	// Exact hash-space math and sampled cross-check must roughly agree and
	// both be near 1/12.
	if plan.HashSpaceMoved < 0.05 || plan.HashSpaceMoved > 0.12 {
		t.Fatalf("HashSpaceMoved %.4f outside expected band", plan.HashSpaceMoved)
	}
	if diff := plan.HashSpaceMoved - plan.SampleMoveFraction(); diff < -0.02 || diff > 0.02 {
		t.Fatalf("exact %.4f vs sampled %.4f disagree", plan.HashSpaceMoved, plan.SampleMoveFraction())
	}
	// Active ring must be untouched by standby staging/prewarm.
	if c.Version() != 1 || c.Active().NumNodes() != 12 {
		t.Fatalf("active ring disturbed: v%d, %d nodes", c.Version(), c.Active().NumNodes())
	}
	if _, ok := c.PendingVersion(); !ok {
		t.Fatal("no pending ring after Prewarm")
	}
}

// Acceptance boundary: while the active ring serves steady traffic, inject a
// scale-in on the standby ring; after prewarm, atomically flip; traffic
// transitions instantly with zero routing errors.
func TestAcceptanceScaleInWithConcurrentTraffic(t *testing.T) {
	c := newTestController(t, 12, 3)

	// Pre-flip key map for a fixed probe set.
	const probes = 50000
	before := make([]string, probes)
	for i := 0; i < probes; i++ {
		n, _ := c.Lookup(fmt.Sprintf("probe-%d", i))
		before[i] = n.ID
	}

	// Concurrent readers hammering the controller (steady traffic).
	var lookups, routeErrors, removedHits atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("live-%d-%d", w, i)
				n, ok := c.Lookup(key)
				ring := c.Active()
				switch {
				case !ok:
					routeErrors.Add(1) // empty route
				case !ring.Has(n.ID):
					routeErrors.Add(1) // node not in the ring version read
				case !ring.Has("n03") && !ring.Has("n07") && n.ID == "n03":
					removedHits.Add(1) // served by removed node after flip
				}
				// Replica path must stay consistent too.
				for _, rn := range c.LookupN(key, 3) {
					if !ring.Has(rn.ID) {
						routeErrors.Add(1)
					}
				}
				lookups.Add(1)
			}
		}(w)
	}

	// Inject scale-in on the standby ring while traffic runs.
	removed := []string{"n03", "n07"}
	for _, id := range removed {
		if err := c.SyncRemove(id); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := c.Prewarm(100000)
	if err != nil {
		t.Fatal(err)
	}

	// Atomic flip, timed: must be effectively instantaneous.
	start := time.Now()
	flipPlan, err := c.Flip()
	flipCost := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if flipPlan.ToVersion != 2 {
		t.Fatalf("flip plan ToVersion = %d, want 2", flipPlan.ToVersion)
	}
	if flipCost > 10*time.Millisecond {
		t.Fatalf("flip took %s, want sub-millisecond pointer swap", flipCost)
	}
	t.Logf("flip completed in %s", flipCost)

	// Let post-flip traffic run briefly, then stop readers.
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	if got := routeErrors.Load(); got != 0 {
		t.Fatalf("routing errors during cutover: %d", got)
	}
	if got := removedHits.Load(); got != 0 {
		t.Fatalf("removed node served traffic after flip: %d hits", got)
	}
	if lookups.Load() == 0 {
		t.Fatal("no lookups executed")
	}
	t.Logf("%d concurrent lookups, 0 routing errors", lookups.Load())

	// Post-flip state: version bumped, removed nodes gone.
	if c.Version() != 2 {
		t.Fatalf("version = %d, want 2", c.Version())
	}
	for _, id := range removed {
		if c.Active().Has(id) {
			t.Fatalf("removed node %s still on active ring", id)
		}
	}

	// Post-flip key map: moved fraction must match the precomputed plan.
	moved := 0
	for i := 0; i < probes; i++ {
		n, _ := c.Lookup(fmt.Sprintf("probe-%d", i))
		if n.ID != before[i] {
			moved++
			if n.ID == "n03" || n.ID == "n07" {
				t.Fatal("key moved onto removed node")
			}
		}
	}
	actual := float64(moved) / probes
	t.Logf("moved: plan(exact)=%.4f plan(sampled)=%.4f actual=%.4f",
		plan.HashSpaceMoved, plan.SampleMoveFraction(), actual)
	if diff := actual - plan.HashSpaceMoved; diff < -0.02 || diff > 0.02 {
		t.Fatalf("actual moved %.4f diverges from precomputed plan %.4f", actual, plan.HashSpaceMoved)
	}

	// Controller is reusable: another staged change + flip works.
	if err := c.SyncAdd(Node{ID: "n99", Zone: "z0", Rack: "r0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Prewarm(1000); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Flip(); err != nil {
		t.Fatal(err)
	}
	if !c.Active().Has("n99") || c.Version() != 3 {
		t.Fatalf("second flip broken: v%d, has n99=%v", c.Version(), c.Active().Has("n99"))
	}
}

// Flip must be safe under concurrent readers (run with -race).
func TestConcurrentFlipRace(t *testing.T) {
	c := newTestController(t, 8, 2)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = c.Lookup(fmt.Sprintf("k%d", i))
				_ = c.LookupN(fmt.Sprintf("k%d", i), 2)
			}
		}(w)
	}
	for round := 0; round < 20; round++ {
		id := fmt.Sprintf("x%02d", round)
		if err := c.SyncAdd(Node{ID: id, Zone: "z9", Rack: "r9"}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Prewarm(100); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Flip(); err != nil {
			t.Fatal(err)
		}
		if err := c.SyncRemove(id); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Prewarm(100); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Flip(); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if c.Active().NumNodes() != 8 {
		t.Fatalf("node count drifted: %d", c.Active().NumNodes())
	}
}
