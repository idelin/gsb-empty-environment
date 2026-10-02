package maglev

import (
	"fmt"
	"math"
	"testing"
)

var testBackends = []string{"node-0", "node-1", "node-2", "node-3", "node-4"}

func mustBuild(t *testing.T, backends []string, size int) *Table {
	tbl, err := Build(backends, size)
	if err != nil {
		t.Fatalf("Build(%v, %d) failed: %v", backends, size, err)
	}
	return tbl
}

// TestTableSizeMustBePrime verifies the prime-size guard.
func TestTableSizeMustBePrime(t *testing.T) {
	if _, err := Build(testBackends, 65536); err == nil {
		t.Fatal("expected error for non-prime table size 65536")
	}
	if _, err := Build(nil, DefaultSize); err == nil {
		t.Fatal("expected error for empty backend list")
	}
	if !IsPrime(DefaultSize) {
		t.Fatalf("DefaultSize %d should be prime", DefaultSize)
	}
}

// TestDistributionIsNearlyPerfect checks that 5 backends split the 65537
// slots almost perfectly evenly: every backend owns either floor(M/N) or
// ceil(M/N) slots, because each backend claims at most one slot per round.
func TestDistributionIsNearlyPerfect(t *testing.T) {
	tbl := mustBuild(t, testBackends, DefaultSize)
	counts := tbl.Distribution()

	want := DefaultSize / len(testBackends) // 13107
	total := 0
	for i, c := range counts {
		total += c
		if diff := c - want; diff < 0 || diff > 1 {
			t.Errorf("backend %q owns %d slots, want %d or %d (imbalance %.4f%%)",
				testBackends[i], c, want, want+1,
				math.Abs(float64(c-want))/float64(want)*100)
		}
	}
	if total != DefaultSize {
		t.Errorf("slots total %d, want %d", total, DefaultSize)
	}
	t.Logf("slot distribution over %d backends: %v (ideal %d)", len(testBackends), counts, want)
}

// TestLookupIsDeterministic verifies stable, valid mappings.
func TestLookupIsDeterministic(t *testing.T) {
	tbl := mustBuild(t, testBackends, DefaultSize)
	valid := map[string]bool{}
	for _, b := range testBackends {
		valid[b] = true
	}
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		got := tbl.Lookup(key)
		if !valid[got] {
			t.Fatalf("Lookup(%q) = %q, not a backend", key, got)
		}
		if again := tbl.Lookup(key); again != got {
			t.Fatalf("Lookup(%q) unstable: %q then %q", key, got, again)
		}
	}
}

// TestNodeRemovalMinimalDisruption kills the 2nd node, rebuilds the table,
// and verifies that only (approximately) the removed node's share of slots
// changes owner — the minimal-disruption property of Maglev.
func TestNodeRemovalMinimalDisruption(t *testing.T) {
	before := mustBuild(t, testBackends, DefaultSize)

	killed := testBackends[1]
	survivors := make([]string, 0, len(testBackends)-1)
	for i, b := range testBackends {
		if i != 1 { // kill the 2nd node
			survivors = append(survivors, b)
		}
	}
	after := mustBuild(t, survivors, DefaultSize)

	changed := ChangedSlots(before, after)
	theoretical := 1.0 / float64(len(testBackends)) // 0.2
	t.Logf("slots changed owner: %.4f%% (theoretical minimum %.4f%%)",
		changed*100, theoretical*100)
	if changed < theoretical*0.9 || changed > theoretical*1.25 {
		t.Errorf("changed fraction %.4f outside [%.4f, %.4f]",
			changed, theoretical*0.9, theoretical*1.25)
	}

	// Among slots previously owned by surviving nodes, almost none should
	// have drifted to a different owner.
	survivorSlots, drifted := 0, 0
	for slot := 0; slot < before.Size(); slot++ {
		oldOwner := before.OwnerAtSlot(slot)
		if oldOwner == killed {
			continue
		}
		survivorSlots++
		if after.OwnerAtSlot(slot) != oldOwner {
			drifted++
		}
	}
	driftRate := float64(drifted) / float64(survivorSlots)
	t.Logf("survivor-owned slots: %d, drifted: %d (drift rate %.4f%%)",
		survivorSlots, drifted, driftRate*100)
	if driftRate > 0.01 {
		t.Errorf("slot drift rate %.4f%% exceeds 1%%; surviving slots should be stable",
			driftRate*100)
	}

	t.Logf("distribution before: %v", before.Distribution())
	t.Logf("distribution after:  %v", after.Distribution())
}

// TestKeysOfSurvivingNodesDoNotDrift simulates a large key space: after
// killing the 2nd node, keys that were mapped to surviving nodes must stay
// put (zero/minimal drift), matching Maglev's theory.
func TestKeysOfSurvivingNodesDoNotDrift(t *testing.T) {
	before := mustBuild(t, testBackends, DefaultSize)

	killed := testBackends[1]
	survivors := make([]string, 0, len(testBackends)-1)
	for i, b := range testBackends {
		if i != 1 {
			survivors = append(survivors, b)
		}
	}
	after := mustBuild(t, survivors, DefaultSize)

	const numKeys = 200000
	onKilled, onSurvivors, drifted := 0, 0, 0
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("user-session-%d", i)
		oldOwner := before.Lookup(key)
		newOwner := after.Lookup(key)
		if oldOwner == killed {
			onKilled++
		} else {
			onSurvivors++
			if newOwner != oldOwner {
				drifted++
			}
		}
	}

	driftRate := float64(drifted) / float64(onSurvivors)
	t.Logf("keys=%d  on killed node=%d (%.2f%%)  on survivors=%d  drifted=%d (drift rate %.4f%%)",
		numKeys, onKilled, float64(onKilled)/numKeys*100, onSurvivors, drifted, driftRate*100)

	if onKilled == 0 {
		t.Fatal("no keys landed on the killed node; test is vacuous")
	}
	// Maglev theory: keys of surviving nodes should not move at all.
	// Allow a tiny tolerance for hash-boundary effects.
	if driftRate > 0.01 {
		t.Errorf("drift rate %.4f%% exceeds 1%%; surviving keys should be stable", driftRate*100)
	}
}

// BenchmarkLookup demonstrates constant-time lookups.
func BenchmarkLookup(b *testing.B) {
	tbl, err := Build(testBackends, DefaultSize)
	if err != nil {
		b.Fatal(err)
	}
	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = fmt.Sprintf("bench-key-%d", i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tbl.Lookup(keys[i%len(keys)])
	}
}

// BenchmarkBuild measures table construction cost.
func BenchmarkBuild(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := Build(testBackends, DefaultSize); err != nil {
			b.Fatal(err)
		}
	}
}
