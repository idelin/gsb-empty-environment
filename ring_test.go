package hashring

import (
	"fmt"
	"testing"
)

func testNodes(n, zones int) []Node {
	out := make([]Node, n)
	for i := range out {
		out[i] = Node{
			ID:   fmt.Sprintf("n%02d", i),
			Zone: fmt.Sprintf("z%d", i%zones),
			Rack: fmt.Sprintf("r%d", i%(zones*2)),
		}
	}
	return out
}

func buildRing(t *testing.T, nodes []Node, version uint64) *Ring {
	t.Helper()
	b := NewBuilder(128)
	for _, n := range nodes {
		if err := b.Add(n); err != nil {
			t.Fatal(err)
		}
	}
	return b.Build(version)
}

func TestLookupDeterministicAndMember(t *testing.T) {
	r := buildRing(t, testNodes(10, 3), 1)
	for i := 0; i < 5000; i++ {
		key := fmt.Sprintf("key-%d", i)
		n1, ok := r.Lookup(key)
		if !ok {
			t.Fatalf("no owner for %q", key)
		}
		n2, _ := r.Lookup(key)
		if n1.ID != n2.ID {
			t.Fatalf("non-deterministic lookup for %q: %s vs %s", key, n1.ID, n2.ID)
		}
		if !r.Has(n1.ID) {
			t.Fatalf("lookup returned unknown node %q", n1.ID)
		}
	}
}

func TestLookupEmptyRing(t *testing.T) {
	r := NewBuilder(8).Build(1)
	if _, ok := r.Lookup("k"); ok {
		t.Fatal("empty ring must not return an owner")
	}
	if got := r.LookupN("k", 3); got != nil {
		t.Fatalf("empty ring LookupN = %v, want nil", got)
	}
}

func TestLookupNDistinctAndTopologyAware(t *testing.T) {
	r := buildRing(t, testNodes(9, 3), 1)
	for i := 0; i < 2000; i++ {
		key := fmt.Sprintf("repl-%d", i)
		ns := r.LookupN(key, 3)
		if len(ns) != 3 {
			t.Fatalf("LookupN(%q,3) returned %d nodes", key, len(ns))
		}
		seen := map[string]bool{}
		zones := map[string]bool{}
		for _, n := range ns {
			if seen[n.ID] {
				t.Fatalf("duplicate replica %s for %q", n.ID, key)
			}
			seen[n.ID] = true
			zones[n.Zone] = true
		}
		if len(zones) != 3 {
			t.Fatalf("replicas for %q not spread across 3 zones: %v", key, ns)
		}
	}
}

func TestLookupNClampsToNodeCount(t *testing.T) {
	r := buildRing(t, testNodes(2, 2), 1)
	if got := r.LookupN("k", 5); len(got) != 2 {
		t.Fatalf("LookupN clamp: got %d, want 2", len(got))
	}
}

func TestBuilderRemove(t *testing.T) {
	b := NewBuilder(8)
	_ = b.Add(Node{ID: "a"})
	if !b.Remove("a") {
		t.Fatal("Remove(a) = false, want true")
	}
	if b.Remove("a") {
		t.Fatal("second Remove(a) = true, want false")
	}
}

// Removing 1 of N nodes should move ~1/N of the hash space, and only to
// surviving nodes.
func TestMinimalDisruptionOnRemoval(t *testing.T) {
	nodes := testNodes(12, 3)
	full := buildRing(t, nodes, 1)
	b := FromRing(full, 128)
	b.Remove("n05")
	shrunk := b.Build(2)

	moved := 0
	const total = 100000
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("disrupt-%d", i)
		o, _ := full.Lookup(key)
		n, _ := shrunk.Lookup(key)
		if o.ID != n.ID {
			moved++
			if n.ID == "n05" {
				t.Fatal("key moved onto removed node")
			}
		}
	}
	frac := float64(moved) / total
	if frac < 0.05 || frac > 0.12 {
		t.Fatalf("moved fraction %.4f outside expected ~1/12 band", frac)
	}
}
