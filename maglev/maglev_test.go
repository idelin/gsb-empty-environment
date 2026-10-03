package maglev

import (
	"fmt"
	"math"
	"testing"
	"time"
)

var testNodes = []string{"node-0", "node-1", "node-2", "node-3", "node-4"}

func buildTestTable(t *testing.T, nodes []string) *Table {
	tbl, err := Build(nodes, DefaultTableSize)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	return tbl
}

// TestEvenDistribution verifies the table slots are divided near-perfectly
// evenly among the 5 nodes: 65537/5 = 13107.4, so every node must own
// either 13107 or 13108 slots (max-min <= 1).
func TestEvenDistribution(t *testing.T) {
	tbl := buildTestTable(t, testNodes)
	dist := tbl.Distribution()
	if len(dist) != len(testNodes) {
		t.Fatalf("expected %d nodes in distribution, got %d", len(testNodes), len(dist))
	}
	total, min, max := 0, math.MaxInt32, 0
	for _, node := range testNodes {
		c := dist[node]
		total += c
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
		t.Logf("%s owns %d slots (%.4f%%)", node, c, 100*float64(c)/float64(tbl.Size()))
	}
	if total != tbl.Size() {
		t.Fatalf("slots do not add up: got %d, want %d", total, tbl.Size())
	}
	if max-min > 1 {
		t.Fatalf("distribution not near-perfect: min=%d max=%d (spread %d > 1)", min, max, max-min)
	}
}

// TestMinimalDisruption kills the 2nd node, rebuilds the table, and verifies
// the no-drift ratio matches Maglev theory: removing 1 of n backends should
// disturb only ~1/n of the table, i.e. the unchanged fraction must be
// approximately (n-1)/n = 80%. Slots owned by surviving nodes may drift
// only negligibly (the paper's "minimal disruption" property).
func TestMinimalDisruption(t *testing.T) {
	before := buildTestTable(t, testNodes)

	killed := testNodes[1]
	survivors := make([]string, 0, len(testNodes)-1)
	for _, n := range testNodes {
		if n != killed {
			survivors = append(survivors, n)
		}
	}
	after := buildTestTable(t, survivors)

	survivorSet := make(map[string]bool, len(survivors))
	for _, n := range survivors {
		survivorSet[n] = true
	}

	unchanged, survivorDrift := 0, 0
	for slot := 0; slot < before.Size(); slot++ {
		oldNode := before.Nodes()[before.Entry(slot)]
		newNode := after.Nodes()[after.Entry(slot)]
		if oldNode == newNode {
			unchanged++
		} else if survivorSet[oldNode] {
			survivorDrift++
		}
	}

	size := float64(before.Size())
	ratio := float64(unchanged) / size
	driftRatio := float64(survivorDrift) / size
	theory := float64(len(survivors)) / float64(len(testNodes)) // 0.8
	t.Logf("unchanged slots: %d/%d = %.4f%% (theory: %.4f%%)", unchanged, before.Size(), 100*ratio, 100*theory)
	t.Logf("survivor-owned slots that drifted: %d (%.4f%%)", survivorDrift, 100*driftRatio)
	if math.Abs(ratio-theory) > 0.01 {
		t.Fatalf("no-drift ratio %.4f deviates from theory %.4f by more than 1%%", ratio, theory)
	}
	if driftRatio > 0.01 {
		t.Fatalf("survivor drift %.4f%% exceeds 1%%, violates minimal disruption", 100*driftRatio)
	}
}

// TestLookupConsistency verifies Lookup agrees with the raw table entries.
func TestLookupConsistency(t *testing.T) {
	tbl := buildTestTable(t, testNodes)
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		node := tbl.Lookup(key)
		slot := int(hash1(key) % uint64(tbl.Size()))
		if want := tbl.Nodes()[tbl.Entry(slot)]; node != want {
			t.Fatalf("Lookup(%q) = %s, table says %s", key, node, want)
		}
	}
}

// TestLookupConstantTime performs a large number of lookups and checks the
// average per-lookup time stays at constant (nanosecond) scale.
func TestLookupConstantTime(t *testing.T) {
	tbl := buildTestTable(t, testNodes)
	const lookups = 2000000
	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
	}
	start := time.Now()
	for i := 0; i < lookups; i++ {
		_ = tbl.Lookup(keys[i%len(keys)])
	}
	avg := time.Since(start) / lookups
	t.Logf("%d lookups, avg %v per lookup", lookups, avg)
	if avg > 500*time.Nanosecond {
		t.Fatalf("lookup too slow: avg %v per lookup, want constant-time scale", avg)
	}
}

func BenchmarkLookup(b *testing.B) {
	tbl, err := Build(testNodes, DefaultTableSize)
	if err != nil {
		b.Fatal(err)
	}
	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%d", i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tbl.Lookup(keys[i%1024])
	}
}

func BenchmarkBuild(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Build(testNodes, DefaultTableSize); err != nil {
			b.Fatal(err)
		}
	}
}
