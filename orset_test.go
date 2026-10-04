package orset

import "testing"

func sync(a, b *Set) {
	sa, sb := a.Clone(), b.Clone()
	a.Merge(sb)
	b.Merge(sa)
}

// gossipAll runs pairwise sync rounds until every replica has seen every
// other replica's latest clock (two rounds suffice for three nodes).
func gossipAll(sets ...*Set) {
	for round := 0; round < 2; round++ {
		for i := 0; i < len(sets); i++ {
			for j := i + 1; j < len(sets); j++ {
				sync(sets[i], sets[j])
			}
		}
	}
}

func TestAddRemoveContains(t *testing.T) {
	s := NewSet("A", "B")
	s.Add("x")
	if !s.Contains("x") {
		t.Fatal("x should be present after Add")
	}
	if n := s.Remove("x"); n != 1 {
		t.Fatalf("Remove returned %d, want 1", n)
	}
	if s.Contains("x") {
		t.Fatal("x should be absent after Remove")
	}
	if s.TombstoneCount() != 1 {
		t.Fatalf("TombstoneCount = %d, want 1", s.TombstoneCount())
	}
}

func TestUniqueTagsPerAdd(t *testing.T) {
	s := NewSet("A")
	t1 := s.Add("x")
	t2 := s.Add("x")
	if t1 == t2 {
		t.Fatal("tags must be unique per Add")
	}
	if len(s.LiveTags("x")) != 2 {
		t.Fatalf("LiveTags = %v, want 2 tags", s.LiveTags("x"))
	}
}

func TestRemoveOnlyObservedTags(t *testing.T) {
	a, b := NewSet("A", "B"), NewSet("B", "A")
	a.Add("x")
	sync(a, b)
	// Partition: A removes while B concurrently re-adds.
	a.Remove("x")
	b.Add("x")
	sync(a, b)
	if !a.Contains("x") || !b.Contains("x") {
		t.Fatal("concurrent re-add must survive the remove")
	}
	if got := len(a.LiveTags("x")); got != 1 {
		t.Fatalf("live tags at A = %d, want exactly 1 (no resurrection)", got)
	}
}

func TestMergeIdempotentCommutative(t *testing.T) {
	a, b := NewSet("A", "B"), NewSet("B", "A")
	a.Add("x")
	b.Add("y")
	sync(a, b)
	before := a.LiveTags("x")
	a.Merge(b.Clone())
	a.Merge(b.Clone())
	if a.Len() != 2 || len(a.LiveTags("x")) != len(before) {
		t.Fatal("merge must be idempotent")
	}
	c1, c2 := a.Clone(), b.Clone()
	c1.Merge(b.Clone())
	c2.Merge(a.Clone())
	if c1.Len() != c2.Len() || c1.TombstoneCount() != c2.TombstoneCount() {
		t.Fatal("merge must be commutative")
	}
}

func TestCompactWaitsForOfflinePeer(t *testing.T) {
	a, b, c := NewSet("A", "B", "C"), NewSet("B", "A", "C"), NewSet("C", "A", "B")
	a.Add("x")
	gossipAll(a, b, c)
	// C goes offline; A removes and syncs with B only.
	a.Remove("x")
	sync(a, b)
	if dropped := a.Compact(); dropped != 0 {
		t.Fatalf("A dropped %d tombstones while C is offline, want 0", dropped)
	}
	if dropped := b.Compact(); dropped != 0 {
		t.Fatalf("B dropped %d tombstones while C is offline, want 0", dropped)
	}
	if a.TombstoneCount() != 1 || b.TombstoneCount() != 1 {
		t.Fatal("deletion records must be retained until C acknowledges")
	}
}

func TestCompactAfterAllAcknowledge(t *testing.T) {
	a, b, c := NewSet("A", "B", "C"), NewSet("B", "A", "C"), NewSet("C", "A", "B")
	a.Add("x")
	gossipAll(a, b, c)
	a.Remove("x")
	gossipAll(a, b, c)
	if dropped := a.Compact(); dropped != 1 {
		t.Fatalf("A dropped %d tombstones, want 1", dropped)
	}
	if dropped := b.Compact(); dropped != 1 {
		t.Fatalf("B dropped %d tombstones, want 1", dropped)
	}
	if a.Contains("x") || b.Contains("x") || c.Contains("x") {
		t.Fatal("x must stay removed after compaction")
	}
}

func TestNoResurrectionAfterCompact(t *testing.T) {
	a, b, c := NewSet("A", "B", "C"), NewSet("B", "A", "C"), NewSet("C", "A", "B")
	a.Add("x")
	gossipAll(a, b, c)
	a.Remove("x")
	gossipAll(a, b, c)
	a.Compact()
	b.Compact()
	c.Compact()
	if a.TombstoneCount() != 0 || b.TombstoneCount() != 0 || c.TombstoneCount() != 0 {
		t.Fatal("tombstones should be collected once all replicas acknowledged")
	}
	// Late merge rounds must not resurrect x.
	gossipAll(a, b, c)
	if a.Contains("x") || b.Contains("x") || c.Contains("x") {
		t.Fatal("x resurrected after compaction")
	}
}
