package hotring

import (
	"fmt"
	"testing"
)

func TestRingEmpty(t *testing.T) {
	r := NewRing(nil, 0)
	if _, err := r.Get("k"); err != ErrEmptyRing {
		t.Fatalf("expected ErrEmptyRing, got %v", err)
	}
}

func TestRingConsistentRouting(t *testing.T) {
	r := NewRing([]string{"n1", "n2", "n3"}, 100)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		first, err := r.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 5; j++ {
			again, _ := r.Get(key)
			if again != first {
				t.Fatalf("key %q routed inconsistently: %q vs %q", key, first, again)
			}
		}
	}
}

func TestRingDeduplicatesNodes(t *testing.T) {
	r := NewRing([]string{"a", "a", "b"}, 10)
	if r.Size() != 2 {
		t.Fatalf("expected 2 members, got %d", r.Size())
	}
}

// TestRingMinimalDisruption verifies the consistent-hash property:
// removing one of N nodes remaps only ~1/N of the keys.
func TestRingMinimalDisruption(t *testing.T) {
	const nodes = 8
	const keys = 20000
	names := make([]string, 0, nodes)
	for i := 0; i < nodes; i++ {
		names = append(names, fmt.Sprintf("node-%d", i))
	}
	before := NewRing(names, 160)
	after := NewRing(names[:nodes-1], 160) // scale in by one node

	moved := 0
	for i := 0; i < keys; i++ {
		key := fmt.Sprintf("user:%d", i)
		a, _ := before.Get(key)
		b, _ := after.Get(key)
		if a != b {
			moved++
		}
	}
	ratio := float64(moved) / keys
	t.Logf("moved %d/%d keys (%.2f%%), ideal %.2f%%", moved, keys, ratio*100, 100.0/nodes)
	if ratio > 2.0/nodes {
		t.Fatalf("disruption %.2f%% exceeds 2x ideal %.2f%%", ratio*100, 100.0/nodes)
	}
}

// TestRingDistribution verifies load is spread across all nodes.
func TestRingDistribution(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	r := NewRing(names, 160)
	counts := map[string]int{}
	const keys = 50000
	for i := 0; i < keys; i++ {
		n, _ := r.Get(fmt.Sprintf("k%d", i))
		counts[n]++
	}
	ideal := keys / len(names)
	for _, n := range names {
		got := counts[n]
		if got < ideal/2 {
			t.Fatalf("node %s holds %d keys, far below ideal %d", n, got, ideal)
		}
	}
}
