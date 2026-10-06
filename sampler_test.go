package wsample

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// scriptSource replays a fixed script of random integers. It panics if the
// sampler ever requests a value outside [0, n), which would indicate a bug
// in the remaining-weight bookkeeping.
type scriptSource struct {
	vals []int64
	pos  int
}

func (s *scriptSource) Int63n(n int64) int64 {
	if s.pos >= len(s.vals) {
		panic("script exhausted")
	}
	v := s.vals[s.pos]
	s.pos++
	if v < 0 || v >= n {
		panic(fmt.Sprintf("scripted value %d out of range [0,%d)", v, n))
	}
	return v
}

type item struct {
	id string
	w  int64
}

func buildSampler(items []item, script []int64) *Sampler {
	s := New(&scriptSource{vals: script})
	for _, it := range items {
		if err := s.Add(it.id, it.w); err != nil {
			panic(err)
		}
	}
	return s
}

func totalWeight(items []item) int64 {
	var w int64
	for _, it := range items {
		w += it.w
	}
	return w
}

// remainingAfter replays a prefix script on a fresh sampler and returns the
// total weight still available for the next draw.
func remainingAfter(items []item, prefix []int64) int64 {
	s := buildSampler(items, prefix)
	got, err := s.Sample(len(prefix))
	if err != nil {
		panic(err)
	}
	picked := make(map[string]bool, len(got))
	for _, id := range got {
		picked[id] = true
	}
	var rem int64
	for _, it := range items {
		if !picked[it.id] {
			rem += it.w
		}
	}
	return rem
}

// enumerate runs Sample(k) for every possible script of random integers and
// returns the exact probability of each unordered outcome set. A script
// picks r uniformly from [0, remaining) at each draw, so every leaf
// contributes 1 over the product of the remaining totals along its path.
func enumerate(items []item, k int) map[string]*big.Rat {
	probs := make(map[string]*big.Rat)
	var rec func(prefix []int64, denom int64)
	rec = func(prefix []int64, denom int64) {
		if len(prefix) == k {
			s := buildSampler(items, prefix)
			got, err := s.Sample(k)
			if err != nil {
				panic(err)
			}
			if len(got) != k {
				panic("short sample")
			}
			seen := make(map[string]bool, k)
			for _, id := range got {
				if seen[id] {
					panic("duplicate item in sample")
				}
				seen[id] = true
			}
			sorted := append([]string(nil), got...)
			sort.Strings(sorted)
			key := strings.Join(sorted, ",")
			p := big.NewRat(1, denom)
			if acc, ok := probs[key]; ok {
				acc.Add(acc, p)
			} else {
				probs[key] = p
			}
			return
		}
		rem := remainingAfter(items, prefix)
		for r := int64(0); r < rem; r++ {
			next := append(append([]int64(nil), prefix...), r)
			rec(next, denom*rem)
		}
	}
	rec(nil, 1)
	return probs
}

// exactSetProb computes the exact probability of drawing a given unordered
// set: the sum over all its orderings of the product of successive
// remaining-weight ratios.
func exactSetProb(items []item, set []string) *big.Rat {
	wOf := make(map[string]int64, len(items))
	for _, it := range items {
		wOf[it.id] = it.w
	}
	W := totalWeight(items)
	result := big.NewRat(0, 1)
	permute(set, func(perm []string) {
		p := big.NewRat(1, 1)
		rem := W
		for _, id := range perm {
			p.Mul(p, big.NewRat(wOf[id], rem))
			rem -= wOf[id]
		}
		result.Add(result, p)
	})
	return result
}

func permute(s []string, f func([]string)) {
	var rec func(i int)
	rec = func(i int) {
		if i == len(s) {
			f(s)
			return
		}
		for j := i; j < len(s); j++ {
			s[i], s[j] = s[j], s[i]
			rec(i + 1)
			s[i], s[j] = s[j], s[i]
		}
	}
	rec(0)
}

func subsets(items []item, k int) [][]string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	var out [][]string
	var rec func(start int, cur []string)
	rec = func(start int, cur []string) {
		if len(cur) == k {
			out = append(out, append([]string(nil), cur...))
			return
		}
		for i := start; i < len(ids); i++ {
			rec(i+1, append(cur, ids[i]))
		}
	}
	rec(0, nil)
	return out
}

// checkExact enumerates every script and asserts the observed counts match
// the exact probabilities as exact rational numbers.
func checkExact(t *testing.T, items []item, k int) {
	t.Helper()
	probs := enumerate(items, k)
	want := make(map[string]bool)
	sum := big.NewRat(0, 1)
	for _, set := range subsets(items, k) {
		key := strings.Join(set, ",")
		want[key] = true
		exact := exactSetProb(items, set)
		got, ok := probs[key]
		if !ok {
			got = big.NewRat(0, 1)
		}
		sum.Add(sum, got)
		if got.Cmp(exact) != 0 {
			t.Errorf("set {%s}: got probability %s, want %s",
				key, got.RatString(), exact.RatString())
		}
	}
	for key := range probs {
		if !want[key] {
			t.Errorf("impossible outcome set {%s} has probability %s", key, probs[key].RatString())
		}
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("probabilities sum to %s, want 1", sum.RatString())
	}
}

func TestExactProbabilities(t *testing.T) {
	checkExact(t, []item{{"a", 1}, {"b", 2}, {"c", 3}}, 1)
	checkExact(t, []item{{"a", 1}, {"b", 2}, {"c", 3}}, 2)
	checkExact(t, []item{{"a", 1}, {"b", 2}, {"c", 3}}, 3)
	checkExact(t, []item{{"a", 1}, {"b", 1}, {"c", 2}, {"d", 4}}, 2)
	checkExact(t, []item{{"a", 5}, {"b", 1}, {"c", 1}}, 2)
}

func TestRemoveLeavesNoResidualWeight(t *testing.T) {
	s := New(NewSplitMix64(7))
	for _, it := range []item{{"a", 1}, {"b", 2}, {"c", 3}} {
		if err := s.Add(it.id, it.w); err != nil {
			t.Fatal(err)
		}
	}
	if !s.Remove("b") {
		t.Fatal("Remove(b) = false, want true")
	}
	if got := s.Total(); got != 4 {
		t.Fatalf("Total after remove = %d, want 4", got)
	}
	if _, ok := s.Weight("b"); ok {
		t.Fatal("Weight(b) present after remove")
	}
	if s.Remove("b") {
		t.Fatal("second Remove(b) = true, want false")
	}
	// Exact distribution over the survivors proves no residual weight of b
	// remains anywhere in the index structure.
	checkExact(t, []item{{"a", 1}, {"c", 3}}, 2)

	// Re-adding with a different weight must start clean.
	if err := s.Add("b", 5); err != nil {
		t.Fatal(err)
	}
	if got := s.Total(); got != 9 {
		t.Fatalf("Total after re-add = %d, want 9", got)
	}
	if w, _ := s.Weight("b"); w != 5 {
		t.Fatalf("Weight(b) = %d, want 5", w)
	}
	checkExact(t, []item{{"a", 1}, {"b", 5}, {"c", 3}}, 2)
}

func TestUpdateWeightLeavesNoResidual(t *testing.T) {
	s := New(NewSplitMix64(3))
	s.Add("a", 1)
	s.Add("b", 2)
	if err := s.Add("a", 4); err != nil {
		t.Fatal(err)
	}
	if got := s.Total(); got != 6 {
		t.Fatalf("Total = %d, want 6", got)
	}
	if w, _ := s.Weight("a"); w != 4 {
		t.Fatalf("Weight(a) = %d, want 4", w)
	}
	checkExact(t, []item{{"a", 4}, {"b", 2}}, 2)
}

func TestStateRestoreReproducesSuccessiveResults(t *testing.T) {
	items := []item{{"a", 1}, {"b", 2}, {"c", 3}, {"d", 4}}

	rng1 := NewSplitMix64(99)
	s1 := New(rng1)
	for _, it := range items {
		s1.Add(it.id, it.w)
	}
	if _, err := s1.Sample(2); err != nil {
		t.Fatal(err)
	}
	state := rng1.State()
	var cont1 [][]string
	for i := 0; i < 5; i++ {
		got, err := s1.Sample(3)
		if err != nil {
			t.Fatal(err)
		}
		cont1 = append(cont1, got)
	}

	rng2 := NewSplitMix64(0)
	rng2.SetState(state)
	s2 := New(rng2)
	for _, it := range items {
		s2.Add(it.id, it.w)
	}
	for i := 0; i < 5; i++ {
		got, err := s2.Sample(3)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, ",") != strings.Join(cont1[i], ",") {
			t.Fatalf("draw %d after restore = %v, want %v", i, got, cont1[i])
		}
	}
}

func TestInjectedMathRandSource(t *testing.T) {
	s := New(rand.New(rand.NewSource(42)))
	for _, it := range []item{{"a", 1}, {"b", 2}, {"c", 3}} {
		s.Add(it.id, it.w)
	}
	got, err := s.Sample(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("duplicate %q in %v", id, got)
		}
		seen[id] = true
	}
}

func TestSampleAllAndDistinct(t *testing.T) {
	s := New(NewSplitMix64(1))
	ids := []string{"a", "b", "c", "d", "e"}
	for i, id := range ids {
		s.Add(id, int64(i+1))
	}
	for trial := 0; trial < 50; trial++ {
		got, err := s.Sample(len(ids))
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, id := range got {
			if seen[id] {
				t.Fatalf("duplicate %q in %v", id, got)
			}
			seen[id] = true
		}
		if len(seen) != len(ids) {
			t.Fatalf("got %v, want all %v", got, ids)
		}
	}
}

func TestStreamingEvents(t *testing.T) {
	s := New(NewSplitMix64(11))
	events := []Event{
		{ID: "a", Weight: 1},
		{ID: "b", Weight: 2},
		{ID: "c", Weight: 3},
		{ID: "b", Remove: true},
		{ID: "c", Weight: 6}, // update
		{ID: "ghost", Remove: true},
	}
	for _, ev := range events {
		if err := s.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.Total(); got != 7 {
		t.Fatalf("Total = %d, want 7", got)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
	checkExact(t, []item{{"a", 1}, {"c", 6}}, 2)
}

func TestErrors(t *testing.T) {
	s := New(nil)
	if err := s.Add("a", 0); err != ErrBadWeight {
		t.Fatalf("Add zero weight err = %v, want ErrBadWeight", err)
	}
	if err := s.Add("b", -3); err != ErrBadWeight {
		t.Fatalf("Add negative weight err = %v, want ErrBadWeight", err)
	}
	s.Add("a", 1)
	if _, err := s.Sample(2); err != ErrTooMany {
		t.Fatalf("Sample(2) err = %v, want ErrTooMany", err)
	}
	if _, err := s.Sample(-1); err != ErrTooMany {
		t.Fatalf("Sample(-1) err = %v, want ErrTooMany", err)
	}
	got, err := s.Sample(0)
	if err != nil || len(got) != 0 {
		t.Fatalf("Sample(0) = %v, %v", got, err)
	}
}

func TestGrowAndSlotReuse(t *testing.T) {
	s := New(NewSplitMix64(5))
	var sum int64
	for i := 0; i < 50; i++ {
		w := int64(i%5 + 1)
		sum += w
		if err := s.Add(fmt.Sprintf("id%d", i), w); err != nil {
			t.Fatal(err)
		}
	}
	if s.Total() != sum {
		t.Fatalf("Total = %d, want %d", s.Total(), sum)
	}
	// Remove half, forcing slot recycling on subsequent adds.
	var removed int64
	for i := 0; i < 50; i += 2 {
		w, _ := s.Weight(fmt.Sprintf("id%d", i))
		removed += w
		s.Remove(fmt.Sprintf("id%d", i))
	}
	if s.Total() != sum-removed {
		t.Fatalf("Total after removes = %d, want %d", s.Total(), sum-removed)
	}
	for i := 100; i < 130; i++ {
		s.Add(fmt.Sprintf("id%d", i), 2)
	}
	if got, want := s.Total(), sum-removed+60; got != want {
		t.Fatalf("Total after reuse = %d, want %d", got, want)
	}
	got, err := s.Sample(10)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("duplicate %q", id)
		}
		seen[id] = true
		if _, ok := s.Weight(id); !ok {
			t.Fatalf("sampled removed item %q", id)
		}
	}
}
