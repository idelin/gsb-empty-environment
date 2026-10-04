package orset

import "testing"

var cluster = []string{"A", "B", "C"}

func newCluster() (a, b, c *Replica) {
	return New("A", cluster...), New("B", cluster...), New("C", cluster...)
}

func TestAddContainsRemove(t *testing.T) {
	a, _, _ := newCluster()
	if a.Contains("x") {
		t.Fatal("empty replica must not contain x")
	}
	a.Add("x")
	if !a.Contains("x") {
		t.Fatal("after Add, replica must contain x")
	}
	if !a.Remove("x") {
		t.Fatal("Remove of present element must report true")
	}
	if a.Contains("x") {
		t.Fatal("after Remove, replica must not contain x")
	}
	if a.Remove("x") {
		t.Fatal("Remove of absent element must report false")
	}
	if a.TombstoneCount() != 1 {
		t.Fatalf("expected 1 tombstone, got %d", a.TombstoneCount())
	}
}

func TestUniqueTags(t *testing.T) {
	a, b, _ := newCluster()
	d1 := a.Add("x")
	d2 := a.Add("x")
	d3 := b.Add("x")
	if d1 == d2 || d1 == d3 || d2 == d3 {
		t.Fatalf("tags must be unique: %v %v %v", d1, d2, d3)
	}
}

// A remove must not swallow a concurrent re-add on another replica.
func TestConcurrentReAddSurvivesRemove(t *testing.T) {
	a, b, _ := newCluster()
	a.Add("x")
	Sync(a, b)

	a.Remove("x") // observes only the original tag
	b.Add("x")    // concurrent re-add with a fresh tag
	Sync(a, b)

	if !a.Contains("x") || !b.Contains("x") {
		t.Fatalf("concurrent re-add was swallowed: A=%v B=%v", a.Elements(), b.Elements())
	}
}

// A remove must still win against tags it actually observed.
func TestObservedRemoveWins(t *testing.T) {
	a, b, _ := newCluster()
	a.Add("x")
	Sync(a, b)
	b.Remove("x") // b observed the tag, so this remove covers it
	Sync(a, b)
	if a.Contains("x") || b.Contains("x") {
		t.Fatalf("observed remove must win: A=%v B=%v", a.Elements(), b.Elements())
	}
}

func TestMergeIdempotentAndCommutative(t *testing.T) {
	a, b, _ := newCluster()
	a.Add("x")
	b.Add("y")
	Sync(a, b)
	before := a.Elements()
	a.Merge(b)
	a.Merge(b)
	if len(a.Elements()) != len(before) {
		t.Fatalf("merge not idempotent: %v -> %v", before, a.Elements())
	}

	// Commutativity: merge in both orders, compare results.
	a1, b1, _ := newCluster()
	a1.Add("x")
	b1.Add("y")
	b1.Remove("y")
	a2, b2, _ := newCluster()
	a2.Add("x")
	b2.Add("y")
	b2.Remove("y")
	a1.Merge(b1)
	b2.Merge(a2)
	if a1.Contains("x") != b2.Contains("x") || a1.Contains("y") || b2.Contains("y") {
		t.Fatal("merge results diverged between orders")
	}
}

// GC must not drop tombstones while a known replica has not acknowledged
// the remove.
func TestGCBlockedByOfflineReplica(t *testing.T) {
	a, b, c := newCluster()
	a.Add("x")
	Sync(a, b)
	Sync(a, c)

	a.Remove("x")
	Sync(a, b) // c is partitioned away

	if dropped := a.GC(); dropped != 0 {
		t.Fatalf("GC dropped %d tombstones while C has not acked", dropped)
	}
	if a.TombstoneCount() != 1 {
		t.Fatalf("tombstone must be retained, have %d", a.TombstoneCount())
	}
}

// Once every known replica has observed the remove, GC compacts history.
func TestGCAfterFullAck(t *testing.T) {
	a, b, c := newCluster()
	a.Add("x")
	Sync(a, b)
	Sync(a, c)

	a.Remove("x")
	// Full mesh so every replica sees the remove and every replica learns
	// the others' acknowledgements.
	Sync(a, b)
	Sync(a, c)
	Sync(b, c)
	Sync(a, b)
	Sync(a, c)

	if dropped := a.GC(); dropped != 1 {
		t.Fatalf("expected 1 tombstone collected, got %d", dropped)
	}
	if a.TombstoneCount() != 0 {
		t.Fatalf("expected no tombstones, have %d", a.TombstoneCount())
	}
}

// Acknowledgements propagate transitively: A learns C's ack through B.
func TestAckGossipThroughIntermediate(t *testing.T) {
	a, b, c := newCluster()
	a.Add("x")
	Sync(a, b)
	Sync(b, c) // c learns of the add via b

	a.Remove("x")
	Sync(a, b)
	Sync(b, c) // c learns of the remove via b
	Sync(b, c) // b learns c's ack
	Sync(a, b) // a learns c's ack via b

	if dropped := a.GC(); dropped != 1 {
		t.Fatalf("acks should gossip transitively, dropped %d", dropped)
	}
}

// A stale replica re-presenting a tag after GC safety is reached must not
// resurrect it: safety means no replica can still hold that tag as live.
func TestNoResurrectionAfterSafeGC(t *testing.T) {
	a, b, c := newCluster()
	a.Add("x")
	Sync(a, b)
	Sync(a, c)
	a.Remove("x")
	Sync(a, b)
	Sync(a, c)
	Sync(b, c)
	Sync(a, b)
	Sync(a, c)
	a.GC()
	b.GC()
	c.GC()

	// Late full re-sync: nothing may resurrect x.
	Sync(a, b)
	Sync(a, c)
	Sync(b, c)
	for _, r := range []*Replica{a, b, c} {
		if r.Contains("x") {
			t.Fatalf("x resurrected on replica %s", r.ID())
		}
	}
}
