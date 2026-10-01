package migration

import (
	"fmt"
	"sync"
	"testing"
)

func buildTopologies(t *testing.T) (oldTopo, newTopo *Topology) {
	t.Helper()
	var oldNodes, newNodes []Node
	for i := 0; i < 3; i++ {
		oldNodes = append(oldNodes, NewMemNode(fmt.Sprintf("old-%d", i)))
		newNodes = append(newNodes, NewMemNode(fmt.Sprintf("new-%d", i)))
	}
	var err error
	oldTopo, err = NewTopology("Top_Old", oldNodes, 100)
	if err != nil {
		t.Fatalf("old topology: %v", err)
	}
	newTopo, err = NewTopology("Top_New", newNodes, 100)
	if err != nil {
		t.Fatalf("new topology: %v", err)
	}
	return oldTopo, newTopo
}

func newController(t *testing.T) *Controller {
	t.Helper()
	oldTopo, newTopo := buildTopologies(t)
	c, err := NewController(oldTopo)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if err := c.PublishNewTopology(newTopo); err != nil {
		t.Fatalf("PublishNewTopology: %v", err)
	}
	return c
}

// TestCanarySplitAt10Percent: after publishing Top_New and setting 10%,
// exactly the keys whose bucket < 10 are owned by Top_New; the rest stay
// on Top_Old. Routing is deterministic, not probabilistic.
func TestCanarySplitAt10Percent(t *testing.T) {
	c := newController(t)
	if err := c.SetTrafficPercent(10); err != nil {
		t.Fatalf("SetTrafficPercent: %v", err)
	}

	const n = 2000
	wantNew := 0
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key-%d", i)
		c.Set(key, fmt.Sprintf("v%d", i))
		if bucket(key) < 10 {
			wantNew++
		}
	}
	gotNew := 0
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key-%d", i)
		want := GenOld
		if bucket(key) < 10 {
			want = GenNew
		}
		if got := c.OwnerOf(key); got != want {
			t.Fatalf("key %q: owner = %v, want %v", key, got, want)
		}
		if c.OwnerOf(key) == GenNew {
			gotNew++
		}
	}
	if gotNew != wantNew {
		t.Fatalf("new-topology keys = %d, want %d", gotNew, wantNew)
	}
	t.Logf("10%% canary: %d/%d keys (%.1f%%) routed to Top_New", gotNew, n, 100*float64(gotNew)/n)
}

// TestProgressiveMigrationNoLoss: pre-populate on Top_Old, then step the
// canary 10%% -> 100%%. After every step every key must still read back
// its original value (zero loss), and at 100%% every key must be owned
// by Top_New.
func TestProgressiveMigrationNoLoss(t *testing.T) {
	c := newController(t)

	const n = 500
	for i := 0; i < n; i++ {
		c.Set(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
	}

	for p := 10; p <= 100; p += 10 {
		if err := c.VerifyNewCluster(); err != nil {
			t.Fatalf("verify new cluster at %d%%: %v", p, err)
		}
		if err := c.SetTrafficPercent(p); err != nil {
			t.Fatalf("SetTrafficPercent(%d): %v", p, err)
		}
		lost := 0
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("key-%d", i)
			v, ok := c.Get(key)
			if !ok || v != fmt.Sprintf("value-%d", i) {
				lost++
			}
		}
		if lost != 0 {
			t.Fatalf("at %d%%: %d keys lost", p, lost)
		}
	}

	for i := 0; i < n; i++ {
		if g := c.OwnerOf(fmt.Sprintf("key-%d", i)); g != GenNew {
			t.Fatalf("key-%d: owner = %v, want Top_New after 100%%", i, g)
		}
	}
}

// TestConcurrentReadWriteNoSplitBrain: while the canary percentage is
// actively shifting, a writer per key performs ordered writes and readers
// read continuously. Every observed value must be one of the values
// written so far for that key (never a stale/foreign value), and the
// final read must equal the final write.
func TestConcurrentReadWriteNoSplitBrain(t *testing.T) {
	c := newController(t)

	const keys = 64
	const writesPerKey = 200

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Shifter: 0% -> 100% in 10% steps while traffic is flowing.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for p := 0; p <= 100; p += 10 {
			_ = c.SetTrafficPercent(p)
		}
		close(stop)
	}()

	// Readers: values must always come from the key's own write sequence.
	for k := 0; k < keys; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", k)
			lastSeen := -1
			for {
				select {
				case <-stop:
					return
				default:
				}
				if v, ok := c.Get(key); ok {
					var seq int
					if _, err := fmt.Sscanf(v, "v%d", &seq); err != nil {
						t.Errorf("key %q: foreign value %q", key, v)
						return
					}
					if seq < lastSeen {
						t.Errorf("key %q: value regressed %d -> %d", key, lastSeen, seq)
						return
					}
					lastSeen = seq
				}
			}
		}(k)
	}

	// Writers: monotonically increasing values per key.
	for k := 0; k < keys; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", k)
			for seq := 0; seq < writesPerKey; seq++ {
				c.Set(key, fmt.Sprintf("v%d", seq))
			}
		}(k)
	}
	wg.Wait()

	// Final state: last write wins and is readable.
	for k := 0; k < keys; k++ {
		key := fmt.Sprintf("k%d", k)
		v, ok := c.Get(key)
		if !ok {
			t.Fatalf("key %q lost after migration", key)
		}
		if want := fmt.Sprintf("v%d", writesPerKey-1); v != want {
			t.Fatalf("key %q = %q, want %q", key, v, want)
		}
	}
}

// TestVerifyNewCluster ensures the readiness probe works and fails when
// no new topology is published.
func TestVerifyNewCluster(t *testing.T) {
	oldTopo, _ := buildTopologies(t)
	c, err := NewController(oldTopo)
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	if err := c.VerifyNewCluster(); err == nil {
		t.Fatal("expected error before publishing new topology")
	}
	_, newTopo := buildTopologies(t)
	if err := c.PublishNewTopology(newTopo); err != nil {
		t.Fatalf("PublishNewTopology: %v", err)
	}
	if err := c.VerifyNewCluster(); err != nil {
		t.Fatalf("VerifyNewCluster: %v", err)
	}
}

// TestSetTrafficPercentValidation covers boundary and ordering rules.
func TestSetTrafficPercentValidation(t *testing.T) {
	c := newController(t)
	for _, p := range []int{-1, 101} {
		if err := c.SetTrafficPercent(p); err == nil {
			t.Fatalf("percent %d: expected error", p)
		}
	}
	for _, p := range []int{0, 10, 50, 100} {
		if err := c.SetTrafficPercent(p); err != nil {
			t.Fatalf("percent %d: %v", p, err)
		}
		if got := c.TrafficPercent(); got != p {
			t.Fatalf("TrafficPercent = %d, want %d", got, p)
		}
	}
}

// TestDelete removes the key from its owning topology and registry.
func TestDelete(t *testing.T) {
	c := newController(t)
	if err := c.SetTrafficPercent(50); err != nil {
		t.Fatalf("SetTrafficPercent: %v", err)
	}
	c.Set("doomed", "x")
	if _, ok := c.Get("doomed"); !ok {
		t.Fatal("key missing before delete")
	}
	c.Delete("doomed")
	if _, ok := c.Get("doomed"); ok {
		t.Fatal("key still present after delete")
	}
}
