package wquantile

import (
	"math/rand"
	"sort"
	"testing"
)

// modelQuantile computes the quantile from the expanded raw samples:
// the smallest value whose cumulative weight first reaches q*total.
func modelQuantile(samples []int64, q float64) (int64, bool) {
	if len(samples) == 0 {
		return 0, false
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	thr := q * float64(len(sorted))
	for i, v := range sorted {
		if float64(i+1) >= thr {
			return v, true
		}
	}
	return sorted[len(sorted)-1], true
}

func expand(entries []Entry) []int64 {
	var out []int64
	for _, e := range entries {
		for i := uint64(0); i < e.Weight; i++ {
			out = append(out, e.Value)
		}
	}
	return out
}

var qs = []float64{0, 0.01, 0.1, 0.25, 0.5, 0.75, 0.9, 0.99, 1}

func checkAgainstModel(t *testing.T, m *Multiset, samples []int64) {
	t.Helper()
	var total uint64
	for range samples {
		total++
	}
	if m.Total() != total {
		t.Fatalf("total weight: got %d want %d", m.Total(), total)
	}
	for _, q := range qs {
		got, err := m.Quantile(q)
		want, ok := modelQuantile(samples, q)
		if !ok {
			if err != ErrEmpty {
				t.Fatalf("q=%v: expected ErrEmpty, got %v (%v)", q, got, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("q=%v: unexpected error %v", q, err)
		}
		if got != want {
			t.Fatalf("q=%v: got %d want %d (samples %v)", q, got, want, samples)
		}
	}
}

// TestQuantileExpandedModel drives random adds/removes with small weights
// and cross-checks every quantile against the expanded sample sequence.
func TestQuantileExpandedModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	m := NewMultiset()
	counts := map[int64]uint64{}
	var samples []int64
	for step := 0; step < 4000; step++ {
		v := int64(rng.Intn(21) - 10) // values in [-10,10]: heavy duplication
		if rng.Intn(100) < 60 || len(samples) == 0 {
			w := uint64(rng.Intn(5)) // includes zero weight
			m.Add(v, w)
			counts[v] += w
			for i := uint64(0); i < w; i++ {
				samples = append(samples, v)
			}
		} else {
			// Remove a random live value with random weight.
			var live []int64
			for val, c := range counts {
				if c > 0 {
					live = append(live, val)
				}
			}
			if len(live) == 0 {
				continue
			}
			val := live[rng.Intn(len(live))]
			w := uint64(rng.Intn(int(counts[val])) + 1)
			if err := m.Remove(val, w); err != nil {
				t.Fatalf("remove %d x%d: %v", val, w, err)
			}
			counts[val] -= w
			// Remove w copies of val from the expanded model.
			for i := 0; i < len(samples) && w > 0; {
				if samples[i] == val {
					samples = append(samples[:i], samples[i+1:]...)
					w--
				} else {
					i++
				}
			}
		}
		if step%17 == 0 {
			checkAgainstModel(t, m, samples)
		}
	}
	checkAgainstModel(t, m, samples)
}

func TestEndpointsAndDuplicates(t *testing.T) {
	m := NewMultiset()
	entries := []Entry{{Value: 5, Weight: 3}, {Value: -2, Weight: 1}, {Value: 5, Weight: 2}, {Value: 9, Weight: 4}}
	for _, e := range entries {
		m.Add(e.Value, e.Weight)
	}
	samples := expand([]Entry{{Value: -2, Weight: 1}, {Value: 5, Weight: 5}, {Value: 9, Weight: 4}})
	checkAgainstModel(t, m, samples)
	if got, _ := m.Quantile(0); got != -2 {
		t.Fatalf("q=0: got %d want -2", got)
	}
	if got, _ := m.Quantile(1); got != 9 {
		t.Fatalf("q=1: got %d want 9", got)
	}
	// Out-of-range q clamps to the endpoints.
	if got, _ := m.Quantile(-1); got != -2 {
		t.Fatalf("q=-1: got %d want -2", got)
	}
	if got, _ := m.Quantile(2); got != 9 {
		t.Fatalf("q=2: got %d want 9", got)
	}
}

func TestZeroWeight(t *testing.T) {
	m := NewMultiset()
	m.Add(7, 0) // no-op
	if m.Total() != 0 || m.Len() != 0 {
		t.Fatalf("zero-weight add changed the set: total=%d len=%d", m.Total(), m.Len())
	}
	if _, err := m.Quantile(0.5); err != ErrEmpty {
		t.Fatalf("empty set: got %v want ErrEmpty", err)
	}
	if err := m.Remove(7, 0); err != nil {
		t.Fatalf("zero-weight remove: %v", err)
	}
	m.Add(7, 2)
	if err := m.Remove(7, 2); err != nil {
		t.Fatalf("remove all: %v", err)
	}
	if m.Total() != 0 || m.Len() != 0 {
		t.Fatalf("set not empty after full removal")
	}
	if _, err := m.Quantile(0); err != ErrEmpty {
		t.Fatalf("drained set: got %v want ErrEmpty", err)
	}
}

func TestRemoveInsufficient(t *testing.T) {
	m := NewMultiset()
	if err := m.Remove(3, 1); err != ErrInsufficientWeight {
		t.Fatalf("remove from empty: got %v", err)
	}
	m.Add(3, 2)
	if err := m.Remove(3, 3); err != ErrInsufficientWeight {
		t.Fatalf("remove too much: got %v", err)
	}
	if m.Total() != 2 {
		t.Fatalf("failed removal changed total: %d", m.Total())
	}
}

func TestEmptySet(t *testing.T) {
	m := NewMultiset()
	for _, q := range qs {
		if _, err := m.Quantile(q); err != ErrEmpty {
			t.Fatalf("q=%v: got %v want ErrEmpty", q, err)
		}
	}
}

func TestLoadEntriesRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	m := NewMultiset()
	var samples []int64
	for i := 0; i < 500; i++ {
		v := int64(rng.Intn(40) - 20)
		w := uint64(rng.Intn(4))
		m.Add(v, w)
		for j := uint64(0); j < w; j++ {
			samples = append(samples, v)
		}
	}
	// Snapshot via Entries and rebuild, including shuffled order.
	entries := m.Entries()
	for i := range entries {
		j := rng.Intn(i + 1)
		entries[i], entries[j] = entries[j], entries[i]
	}
	m2 := NewMultiset()
	m2.LoadEntries(entries)
	checkAgainstModel(t, m2, samples)
}
