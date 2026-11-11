package energy_test

import (
	"sort"
	"testing"

	"example.org/netsec/q0406/energy"
)

func allocMap(allocations []energy.Allocation) map[string]int64 {
	out := make(map[string]int64, len(allocations))
	for _, allocation := range allocations {
		out[allocation.SampleID] = allocation.Energy
	}
	return out
}

func assertOrderedByID(t *testing.T, allocations []energy.Allocation) {
	t.Helper()
	ids := make([]string, len(allocations))
	for index, allocation := range allocations {
		ids[index] = allocation.SampleID
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("allocations not ordered by sample id: %v", ids)
	}
}

func assertConservation(t *testing.T, allocations []energy.Allocation, budget int64, positiveWeight bool) {
	t.Helper()
	var total int64
	for _, allocation := range allocations {
		if allocation.Energy < 0 {
			t.Fatalf("negative energy for %q", allocation.SampleID)
		}
		total += allocation.Energy
	}
	if total > budget {
		t.Fatalf("spent %d exceeds budget %d", total, budget)
	}
	if positiveWeight && total != budget {
		t.Fatalf("budget not conserved: spent %d of %d", total, budget)
	}
}

func weightPositive(samples []energy.Sample) bool {
	for _, sample := range samples {
		if sample.Execs > 0 && len(sample.Coverage) > 0 {
			return true
		}
	}
	return false
}

func assertAllocationsEqual(t *testing.T, got, want []energy.Allocation) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("allocation length %d, want %d", len(got), len(want))
	}
	gotMap := allocMap(got)
	wantMap := allocMap(want)
	for id, wantEnergy := range wantMap {
		if gotMap[id] != wantEnergy {
			t.Fatalf("energy for %q = %d, want %d (got=%v want=%v)",
				id, gotMap[id], wantEnergy, gotMap, wantMap)
		}
	}
}

func mustAdd(t *testing.T, allocator *energy.Allocator, sample energy.Sample) {
	t.Helper()
	if err := allocator.Add(sample); err != nil {
		t.Fatalf("Add %q: %v", sample.ID, err)
	}
}

func dedupFeatures(features []energy.Feature) []energy.Feature {
	seen := make(map[energy.Feature]struct{}, len(features))
	out := make([]energy.Feature, 0, len(features))
	for _, feature := range features {
		if _, ok := seen[feature]; ok {
			continue
		}
		seen[feature] = struct{}{}
		out = append(out, feature)
	}
	return out
}

func oracleFrequencies(samples []energy.Sample) map[energy.Feature]int64 {
	frequency := make(map[energy.Feature]int64)
	for _, sample := range samples {
		for _, feature := range dedupFeatures(sample.Coverage) {
			frequency[feature] += sample.Execs
		}
	}
	for feature, count := range frequency {
		if count == 0 {
			delete(frequency, feature)
		}
	}
	return frequency
}

// TestManualAllocation walks through a hand-computed round.
//
// a: {1} x1, b: {1,2} x2, c: {2} x1.
// f1 = 1+2 = 3, f2 = 2+1 = 3; every sample has rarest frequency 3 and
// weight 1/3. Budget 10 gives three 10/3 shares (floor 3, remainder 1/3);
// the leftover unit breaks the tie by ID, so a and b receive it.
func TestManualAllocation(t *testing.T) {
	allocator := energy.New()
	mustAdd(t, allocator, energy.Sample{ID: "a", Coverage: []energy.Feature{1}, Execs: 1})
	mustAdd(t, allocator, energy.Sample{ID: "b", Coverage: []energy.Feature{1, 2}, Execs: 2})
	mustAdd(t, allocator, energy.Sample{ID: "c", Coverage: []energy.Feature{2}, Execs: 1})

	if got := allocator.Frequency(1); got != 3 {
		t.Fatalf("frequency(1) = %d, want 3", got)
	}
	if got := allocator.Frequency(2); got != 3 {
		t.Fatalf("frequency(2) = %d, want 3", got)
	}

	allocations, err := allocator.Allocate(10)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	assertOrderedByID(t, allocations)
	assertConservation(t, allocations, 10, true)

	got := allocMap(allocations)
	want := map[string]int64{"a": 4, "b": 3, "c": 3}
	for id, wantEnergy := range want {
		if got[id] != wantEnergy {
			t.Fatalf("energy[%s] = %d, want %d (got %v)", id, got[id], wantEnergy, got)
		}
	}

	oracle, err := energy.RecomputeAllocate(allocator.Samples(), 10)
	if err != nil {
		t.Fatalf("RecomputeAllocate: %v", err)
	}
	assertAllocationsEqual(t, allocations, oracle)
}

// TestTieBreakByID verifies remainder ordering: larger fractional remainder
// always wins; equal remainders are broken by ascending sample ID.
func TestTieBreakByID(t *testing.T) {
	t.Run("equal remainders smaller ID wins", func(t *testing.T) {
		allocator := energy.New()
		mustAdd(t, allocator, energy.Sample{ID: "a", Coverage: []energy.Feature{1}, Execs: 1})
		mustAdd(t, allocator, energy.Sample{ID: "b", Coverage: []energy.Feature{2}, Execs: 1})

		allocations, err := allocator.Allocate(5)
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		got := allocMap(allocations)
		if got["a"] != 3 || got["b"] != 2 {
			t.Fatalf("tie split = %v, want a=3 b=2", got)
		}

		// Reversing insertion order must not change the ID-based result.
		reversed := energy.New()
		mustAdd(t, reversed, energy.Sample{ID: "b", Coverage: []energy.Feature{2}, Execs: 1})
		mustAdd(t, reversed, energy.Sample{ID: "a", Coverage: []energy.Feature{1}, Execs: 1})
		reversedAllocations, err := reversed.Allocate(5)
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		assertAllocationsEqual(t, reversedAllocations, allocations)
	})

	t.Run("larger remainder beats smaller ID", func(t *testing.T) {
		// z covers a frequency-1 feature (weight 1); a covers a frequency-2
		// feature (weight 1/2). Budget 7 gives shares 14/3 and 7/3, so z has
		// the larger remainder and takes the leftover unit despite its ID.
		allocator := energy.New()
		mustAdd(t, allocator, energy.Sample{ID: "z", Coverage: []energy.Feature{1}, Execs: 1})
		mustAdd(t, allocator, energy.Sample{ID: "a", Coverage: []energy.Feature{2}, Execs: 2})

		allocations, err := allocator.Allocate(7)
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		got := allocMap(allocations)
		if got["z"] != 5 || got["a"] != 2 {
			t.Fatalf("remainder split = %v, want z=5 a=2", got)
		}
	})
}

// TestZeroWeightSamples covers samples with no new coverage and samples that
// have never executed validly: both carry weight zero and get no energy, while
// the full budget still goes to the weighted samples.
func TestZeroWeightSamples(t *testing.T) {
	allocator := energy.New()
	mustAdd(t, allocator, energy.Sample{ID: "alive", Coverage: []energy.Feature{1}, Execs: 1})
	mustAdd(t, allocator, energy.Sample{ID: "no-coverage", Coverage: nil, Execs: 3})
	mustAdd(t, allocator, energy.Sample{ID: "no-execs", Coverage: []energy.Feature{2}, Execs: 0})

	if got := allocator.Frequency(2); got != 0 {
		t.Fatalf("frequency(2) = %d, want 0", got)
	}

	allocations, err := allocator.Allocate(10)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if len(allocations) != 3 {
		t.Fatalf("all samples must appear, got %d entries", len(allocations))
	}
	got := allocMap(allocations)
	if got["alive"] != 10 || got["no-coverage"] != 0 || got["no-execs"] != 0 {
		t.Fatalf("zero-weight split = %v, want alive=10 others=0", got)
	}

	oracle, err := energy.RecomputeAllocate(allocator.Samples(), 10)
	if err != nil {
		t.Fatalf("RecomputeAllocate: %v", err)
	}
	assertAllocationsEqual(t, allocations, oracle)

	t.Run("all weights zero leaves budget unspent", func(t *testing.T) {
		idle := energy.New()
		mustAdd(t, idle, energy.Sample{ID: "x", Coverage: nil, Execs: 5})
		mustAdd(t, idle, energy.Sample{ID: "y", Coverage: []energy.Feature{9}, Execs: 0})
		all, err := idle.Allocate(42)
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		assertConservation(t, all, 42, false)
		for _, allocation := range all {
			if allocation.Energy != 0 {
				t.Fatalf("sample %q got energy %d, want 0", allocation.SampleID, allocation.Energy)
			}
		}
	})
}

// TestCorpusEvictionRestoresRarity is the motivating scenario: a hot sample
// inflates a feature frequency and starves the genuinely rare path until the
// hot sample is evicted, at which point frequencies drop immediately and the
// rare path receives the whole budget.
func TestCorpusEvictionRestoresRarity(t *testing.T) {
	allocator := energy.New()
	mustAdd(t, allocator, energy.Sample{ID: "hot", Coverage: []energy.Feature{1}, Execs: 100})
	mustAdd(t, allocator, energy.Sample{ID: "rare", Coverage: []energy.Feature{2}, Execs: 1})

	if got := allocator.Frequency(1); got != 100 || allocator.Frequency(2) != 1 {
		t.Fatalf("frequencies before eviction = %d/%d, want 100/1",
			allocator.Frequency(1), allocator.Frequency(2))
	}

	before, err := allocator.Allocate(100)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	beforeMap := allocMap(before)

	if err := allocator.Remove("hot"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := allocator.Frequency(1); got != 0 {
		t.Fatalf("frequency(1) after eviction = %d, want 0", got)
	}
	if got := allocator.Frequency(2); got != 1 {
		t.Fatalf("frequency(2) after eviction = %d, want 1", got)
	}
	if _, ok := allocator.Get("hot"); ok {
		t.Fatal("evicted sample still present")
	}

	after, err := allocator.Allocate(100)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	afterMap := allocMap(after)
	if afterMap["rare"] != 100 {
		t.Fatalf("rare path energy after eviction = %d, want 100", afterMap["rare"])
	}
	if afterMap["rare"] <= beforeMap["rare"] {
		t.Fatalf("rare path did not gain energy after eviction: before=%d after=%d",
			beforeMap["rare"], afterMap["rare"])
	}

	oracle, err := energy.RecomputeAllocate(allocator.Samples(), 100)
	if err != nil {
		t.Fatalf("RecomputeAllocate: %v", err)
	}
	assertAllocationsEqual(t, after, oracle)
}
