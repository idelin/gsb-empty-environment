package readrepair

import (
	"sort"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func values(vs []Version) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Value)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestVClockCompare(t *testing.T) {
	a := VClock{"n0": 2, "n1": 1}
	b := VClock{"n0": 1, "n1": 1}
	if !a.Dominates(b) || b.Dominates(a) || a.Equal(b) {
		t.Fatal("dominance broken")
	}
	c := VClock{"n0": 1, "n1": 2}
	if !a.Concurrent(c) || !c.Concurrent(a) {
		t.Fatal("concurrency broken")
	}
	if !a.Equal(a.Copy()) {
		t.Fatal("equality broken")
	}
}

// Concurrent writes on different replicas must survive both the read
// result and the subsequent read repair on every node.
func TestConcurrentBranchesSurviveReadAndRepair(t *testing.T) {
	n0, n1, n2 := NewNode("n0"), NewNode("n1"), NewNode("n2")
	n0.Put("k", Version{Value: "a", Clock: VClock{"n0": 1}})
	n1.Put("k", Version{Value: "b", Clock: VClock{"n1": 1}})

	c := &Coordinator{Nodes: []*Node{n0, n1, n2}, R: 3}
	res := c.Read("k")

	if got := res.Siblings(); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("read lost a concurrent branch, got %v", got)
	}
	if _, ok := res.Value(); ok {
		t.Fatal("Value() must not pick a winner among siblings")
	}

	for _, n := range []*Node{n0, n1, n2} {
		node := n
		waitFor(t, "repair of "+node.ID, func() bool {
			return equalStrings(values(node.state("k")), []string{"a", "b"})
		})
	}
}

// A tombstone with a newer clock dominates older values; a stale value
// arriving later must never resurrect the key.
func TestTombstoneNotOverwrittenByOldValue(t *testing.T) {
	n0, n1, n2 := NewNode("n0"), NewNode("n1"), NewNode("n2")
	for _, n := range []*Node{n0, n1, n2} {
		n.Put("k", Version{Value: "x", Clock: VClock{"n0": 1}})
	}
	// Delete lands only on n0.
	n0.Put("k", Version{Tombstone: true, Clock: VClock{"n0": 2}})

	c := &Coordinator{Nodes: []*Node{n0, n1, n2}, R: 3}
	res := c.Read("k")
	if _, ok := res.Value(); ok {
		t.Fatal("deleted key must not return a value")
	}
	if len(res.Versions) != 1 || !res.Versions[0].Tombstone {
		t.Fatalf("expected single tombstone, got %+v", res.Versions)
	}

	for _, n := range []*Node{n1, n2} {
		node := n
		waitFor(t, "tombstone repair of "+node.ID, func() bool {
			st := node.state("k")
			return len(st) == 1 && st[0].Tombstone
		})
	}

	// A delayed stale write (old clock) reaches a repaired replica.
	n1.Put("k", Version{Value: "x", Clock: VClock{"n0": 1}})
	st := n1.state("k")
	if len(st) != 1 || !st[0].Tombstone {
		t.Fatalf("stale value resurrected the key: %+v", st)
	}
}

// The repair target may be updated concurrently after the coordinator
// read it but before the repair lands. The conditional repair must merge
// instead of overwriting: the target keeps its new branch and gains the
// repaired ones.
func TestRepairMergesWithConcurrentTargetUpdate(t *testing.T) {
	n0, n1, n2 := NewNode("n0"), NewNode("n1"), NewNode("n2")
	n0.Put("k", Version{Value: "a", Clock: VClock{"n0": 1}})
	n1.Put("k", Version{Value: "b", Clock: VClock{"n1": 1}})

	// Hold n2's repair at the merge point until the concurrent Put has
	// fully landed, deterministically exercising "repair arrives after
	// the target was updated".
	releaseRepair := make(chan struct{})
	n2.HookRepair = func() { <-releaseRepair }

	c := &Coordinator{Nodes: []*Node{n0, n1, n2}, R: 3}
	resCh := make(chan ReadResult, 1)
	go func() { resCh <- c.Read("k") }()
	res := <-resCh
	if got := res.Siblings(); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("read lost a branch, got %v", got)
	}

	// n2 is updated concurrently while the repair is still blocked.
	n2.Put("k", Version{Value: "c", Clock: VClock{"n2": 1}})
	close(releaseRepair)

	waitFor(t, "conditional repair on n2", func() bool {
		return equalStrings(values(n2.state("k")), []string{"a", "b", "c"})
	})
}

// A slow replica's response arrives after the read has already returned.
// The late response must still be folded into the repair payload so its
// concurrent branch reaches every replica.
func TestLateResponseMergedIntoRepair(t *testing.T) {
	n0, n1, n2 := NewNode("n0"), NewNode("n1"), NewNode("n2")
	n0.Put("k", Version{Value: "a", Clock: VClock{"n0": 1}})
	n1.Put("k", Version{Value: "a", Clock: VClock{"n0": 1}})
	// n2 holds a concurrent branch and is slow to respond.
	n2.Put("k", Version{Value: "b", Clock: VClock{"n2": 1}})
	n2.Latency = func() time.Duration { return 100 * time.Millisecond }

	c := &Coordinator{Nodes: []*Node{n0, n1, n2}, R: 2}
	start := time.Now()
	res := c.Read("k")
	if elapsed := time.Since(start); elapsed > 80*time.Millisecond {
		t.Fatalf("read blocked on slow replica: %v", elapsed)
	}
	if got := res.Siblings(); !equalStrings(got, []string{"a"}) {
		t.Fatalf("quorum read should return fast responses, got %v", got)
	}

	// The late branch "b" must not be lost: every replica converges to
	// both siblings, and none of them is left with only "a".
	for _, n := range []*Node{n0, n1, n2} {
		node := n
		waitFor(t, "convergence of "+node.ID, func() bool {
			return equalStrings(values(node.state("k")), []string{"a", "b"})
		})
	}
}
