package shard

import (
	"errors"
	"math/rand"
	"testing"

	"geocell/grid"
)

var beijing = grid.LatLng{Lat: 39.9042, Lng: 116.4074}

func mustCell(t *testing.T, ll grid.LatLng, res int) grid.Cell {
	t.Helper()
	c, err := grid.LatLngToCell(ll, res)
	if err != nil {
		t.Fatalf("LatLngToCell: %v", err)
	}
	return c
}

func TestSharderAncestor(t *testing.T) {
	sh, err := NewSharder(3)
	if err != nil {
		t.Fatalf("NewSharder: %v", err)
	}
	c := mustCell(t, beijing, 8)
	got, err := sh.Of(c)
	if err != nil {
		t.Fatalf("Of: %v", err)
	}
	want, _ := c.Parent(3)
	if got != want {
		t.Fatalf("shard = %s, want ancestor %s", got, want)
	}
	// siblings (children of the same res-3 cell) share the shard
	children, err := want.Children(4)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	for _, child := range children {
		s, err := sh.Of(child)
		if err != nil || s != got {
			t.Fatalf("sibling shard mismatch: %s != %s (%v)", s, got, err)
		}
	}
	// a cell at exactly the shard resolution maps to itself
	self, err := sh.Of(want)
	if err != nil || self != want {
		t.Fatalf("shard of res-equal cell = %s, want %s (%v)", self, want, err)
	}
	// cell coarser than the shard resolution is rejected
	coarser, _ := c.Parent(2)
	if _, err := sh.Of(coarser); !errors.Is(err, ErrCellTooCoarse) {
		t.Fatalf("expected ErrCellTooCoarse, got %v", err)
	}
	if _, err := NewSharder(-1); err == nil {
		t.Fatal("expected error for negative shard resolution")
	}
	if _, err := NewSharder(grid.MaxRes + 1); err == nil {
		t.Fatal("expected error for shard resolution > MaxRes")
	}
}

// TestShardLocality verifies the key locality property: all cells of a
// radius-3 ring at res 8 fall into very few res-3 shards.
func TestShardLocality(t *testing.T) {
	sh, _ := NewSharder(3)
	c := mustCell(t, beijing, 8)
	ring, err := grid.KRing(c, 3)
	if err != nil {
		t.Fatalf("KRing: %v", err)
	}
	shards := map[grid.Cell]int{}
	for _, cell := range ring {
		s, err := sh.Of(cell)
		if err != nil {
			t.Fatalf("Of: %v", err)
		}
		shards[s]++
	}
	if len(shards) == 0 {
		t.Fatal("no shards")
	}
	// a radius-3 patch of res-8 cells is ~1/2400 of a res-3 shard's width,
	// so it can touch at most a handful of shards near shard corners
	if len(shards) > 7 {
		t.Fatalf("ring touches %d shards, locality broken", len(shards))
	}
	total := 0
	for _, n := range shards {
		total += n
	}
	if total != len(ring) {
		t.Fatalf("shard accounting lost cells: %d != %d", total, len(ring))
	}
}

func TestRingOwnerDeterministic(t *testing.T) {
	r := NewRing(128)
	nodes := []NodeID{"n1", "n2", "n3", "n4"}
	for _, n := range nodes {
		r.AddNode(n)
	}
	if r.Size() != 4 {
		t.Fatalf("Size = %d, want 4", r.Size())
	}
	counts := map[NodeID]int{}
	const keys = 20000
	for i := 0; i < keys; i++ {
		key := uint64(i)*2654435761 + 97
		a, ok := r.Owner(key)
		if !ok {
			t.Fatal("no owner")
		}
		b, _ := r.Owner(key)
		if a != b {
			t.Fatalf("non-deterministic owner for key %d", key)
		}
		counts[a]++
	}
	// rough balance: each node should hold 25% +/- 15%
	for _, n := range nodes {
		frac := float64(counts[n]) / keys
		if frac < 0.10 || frac > 0.40 {
			t.Fatalf("node %s holds %.2f of keys, unbalanced", n, frac)
		}
	}
	if _, ok := NewRing(8).Owner(1); ok {
		t.Fatal("empty ring must report no owner")
	}
}

// TestRingMinimalMovement verifies the consistent-hashing guarantee: when a
// node leaves, exactly the shards it owned are remapped, and they spread
// across the survivors.
func TestRingMinimalMovement(t *testing.T) {
	r := NewRing(128)
	nodes := []NodeID{"a", "b", "c", "d", "e"}
	for _, n := range nodes {
		r.AddNode(n)
	}
	rng := rand.New(rand.NewSource(1))
	const keys = 20000
	sample := make([]uint64, keys)
	before := make([]NodeID, keys)
	for i := range sample {
		sample[i] = rng.Uint64()
		before[i], _ = r.Owner(sample[i])
	}
	r.RemoveNode("c")
	moved, movedFromC := 0, 0
	toCounts := map[NodeID]int{}
	for i, key := range sample {
		after, _ := r.Owner(key)
		if after == "c" {
			t.Fatalf("removed node still owns key")
		}
		if after != before[i] {
			moved++
			if before[i] == "c" {
				movedFromC++
			} else {
				t.Fatalf("key moved from %s to %s: only the removed node's shards may move",
					before[i], after)
			}
			toCounts[after]++
		}
	}
	if moved == 0 {
		t.Fatal("no keys moved after removing a node")
	}
	if moved != movedFromC {
		t.Fatalf("moved=%d but only %d came from removed node", moved, movedFromC)
	}
	frac := float64(moved) / keys
	if frac < 0.05 || frac > 0.60 {
		t.Fatalf("moved fraction %.3f outside sane bounds", frac)
	}
	if len(toCounts) < 2 {
		t.Fatalf("evacuated shards went to only %d node(s)", len(toCounts))
	}
	// re-adding is symmetric: node count restored
	r.AddNode("c")
	if r.Size() != 5 {
		t.Fatalf("Size = %d after re-add, want 5", r.Size())
	}
}

func TestRouteBatchGroupsByShard(t *testing.T) {
	ring := NewRing(64)
	for _, n := range []NodeID{"x", "y", "z"} {
		ring.AddNode(n)
	}
	sh, _ := NewSharder(3)
	rt := NewRouter(sh, ring)

	c := mustCell(t, beijing, 8)
	cells, err := grid.KRing(c, 3)
	if err != nil {
		t.Fatalf("KRing: %v", err)
	}
	batches, err := rt.RouteBatch(cells)
	if err != nil {
		t.Fatalf("RouteBatch: %v", err)
	}
	// every cell appears exactly once, in a batch of its own shard, on the
	// node that owns that shard
	seen := map[grid.Cell]int{}
	totalBatches := 0
	for node, bs := range batches {
		totalBatches += len(bs)
		for _, b := range bs {
			owner, _ := ring.Owner(uint64(b.Shard))
			if owner != node {
				t.Fatalf("batch routed to %s but shard owner is %s", node, owner)
			}
			for _, cell := range b.Cells {
				seen[cell]++
				s, _ := sh.Of(cell)
				if s != b.Shard {
					t.Fatalf("cell %s in wrong shard batch", cell)
				}
			}
		}
	}
	if len(seen) != len(cells) {
		t.Fatalf("routed %d unique cells, want %d", len(seen), len(cells))
	}
	for cell, n := range seen {
		if n != 1 {
			t.Fatalf("cell %s routed %d times", cell, n)
		}
	}
	if totalBatches > 7 {
		t.Fatalf("%d batches for one k-ring, locality broken", totalBatches)
	}
	// Route agrees with RouteBatch
	n0, err := rt.Route(c)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	s0, _ := sh.Of(c)
	owner, _ := ring.Owner(uint64(s0))
	if n0 != owner {
		t.Fatalf("Route = %s, ring owner = %s", n0, owner)
	}
}

func TestRouteNoNodes(t *testing.T) {
	sh, _ := NewSharder(2)
	rt := NewRouter(sh, NewRing(16))
	c := mustCell(t, beijing, 8)
	if _, err := rt.Route(c); err != ErrNoNodes {
		t.Fatalf("expected ErrNoNodes, got %v", err)
	}
	if _, err := rt.RouteBatch([]grid.Cell{c}); err != ErrNoNodes {
		t.Fatalf("expected ErrNoNodes, got %v", err)
	}
}
