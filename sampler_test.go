package wsample

import (
	"math/big"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// recordingSource plays back scripted values and records the interval
// size requested by every Intn call. When the script is exhausted it
// returns 0; callers only use results from fully scripted runs.
type recordingSource struct {
	vals     []int64
	requests []int64
}

func (s *recordingSource) Intn(n int64) int64 {
	s.requests = append(s.requests, n)
	if len(s.vals) == 0 {
		return 0
	}
	v := s.vals[0]
	s.vals = s.vals[1:]
	if v < 0 || v >= n {
		panic("scripted value out of range")
	}
	return v
}

func setKey(ids []string) string {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// enumerateDistribution enumerates every possible sequence of random
// integers the source could produce (each Intn(n) call ranging over
// [0, n)) and returns the exact probability of every sampled set.
func enumerateDistribution(build func(src Source) *Sampler, k int) map[string]*big.Rat {
	dist := map[string]*big.Rat{}
	var rec func(prefix []int64, denom *big.Int)
	rec = func(prefix []int64, denom *big.Int) {
		src := &recordingSource{vals: append([]int64(nil), prefix...)}
		s := build(src)
		sample, err := s.Sample(k)
		if err != nil {
			panic(err)
		}
		if len(prefix) == k {
			if len(sample) != k || hasDup(sample) {
				panic("sample has wrong length or contains duplicates")
			}
			mass := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Set(denom))
			key := setKey(sample)
			if cur, ok := dist[key]; ok {
				mass.Add(mass, cur)
			}
			dist[key] = mass
			return
		}
		n := src.requests[len(prefix)]
		next := new(big.Int).Mul(denom, big.NewInt(n))
		for r := int64(0); r < n; r++ {
			rec(append(prefix, r), next)
		}
	}
	rec(nil, big.NewInt(1))
	return dist
}

func hasDup(ids []string) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return true
		}
		seen[id] = true
	}
	return false
}

// exactDistribution computes the theoretical distribution of sampled
// sets under successive weighted sampling without replacement.
func exactDistribution(weights map[string]int64, k int) map[string]*big.Rat {
	dist := map[string]*big.Rat{}
	var total int64
	for _, w := range weights {
		total += w
	}
	var rec func(chosen []string, remaining map[string]int64, remTotal int64, prob *big.Rat)
	rec = func(chosen []string, remaining map[string]int64, remTotal int64, prob *big.Rat) {
		if len(chosen) == k {
			key := setKey(chosen)
			if cur, ok := dist[key]; ok {
				prob = new(big.Rat).Add(prob, cur)
			}
			dist[key] = prob
			return
		}
		for id, w := range remaining {
			next := map[string]int64{}
			for id2, w2 := range remaining {
				if id2 != id {
					next[id2] = w2
				}
			}
			p := new(big.Rat).Mul(prob, big.NewRat(w, remTotal))
			rec(append(chosen, id), next, remTotal-w, p)
		}
	}
	rec(nil, weights, total, big.NewRat(1, 1))
	return dist
}

func checkDistribution(t *testing.T, build func(src Source) *Sampler, weights map[string]int64, k int) {
	t.Helper()
	got := enumerateDistribution(build, k)
	want := exactDistribution(weights, k)
	if len(got) != len(want) {
		t.Fatalf("k=%d: got %d distinct outcomes, want %d", k, len(got), len(want))
	}
	sum := new(big.Rat)
	for key, wantP := range want {
		gotP, ok := got[key]
		if !ok {
			t.Fatalf("k=%d: outcome %q never sampled", k, key)
		}
		if gotP.Cmp(wantP) != 0 {
			t.Errorf("k=%d: P(%s) = %s, want %s", k, key, gotP, wantP)
		}
		sum.Add(sum, gotP)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		t.Errorf("k=%d: probabilities sum to %s, want 1", k, sum)
	}
}

func samplerFromEvents(events []Event) func(src Source) *Sampler {
	return func(src Source) *Sampler {
		s := New(src)
		for _, ev := range events {
			if err := s.Apply(ev); err != nil {
				panic(err)
			}
		}
		return s
	}
}

func TestExactProbabilities(t *testing.T) {
	events := []Event{
		{Op: OpAdd, ID: "a", Weight: 1},
		{Op: OpAdd, ID: "b", Weight: 2},
		{Op: OpAdd, ID: "c", Weight: 3},
	}
	weights := map[string]int64{"a": 1, "b": 2, "c": 3}
	for k := 1; k <= 3; k++ {
		checkDistribution(t, samplerFromEvents(events), weights, k)
	}
}

func TestExactProbabilitiesAfterRemove(t *testing.T) {
	events := []Event{
		{Op: OpAdd, ID: "a", Weight: 1},
		{Op: OpAdd, ID: "b", Weight: 2},
		{Op: OpAdd, ID: "c", Weight: 3},
		{Op: OpRemove, ID: "b"},
	}
	weights := map[string]int64{"a": 1, "c": 3}
	for k := 1; k <= 2; k++ {
		checkDistribution(t, samplerFromEvents(events), weights, k)
	}
}

func TestRemoveLeavesNoResidualWeight(t *testing.T) {
	src := NewSplitmix64(7)
	s := New(src)
	for _, ev := range []Event{
		{Op: OpAdd, ID: "a", Weight: 5},
		{Op: OpAdd, ID: "b", Weight: 3},
		{Op: OpAdd, ID: "c", Weight: 2},
	} {
		if err := s.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Remove("b"); err != nil {
		t.Fatal(err)
	}
	if got := s.Total(); got != 7 {
		t.Errorf("Total() = %d, want 7", got)
	}
	if _, ok := s.Weight("b"); ok {
		t.Error("removed id still has a weight")
	}
	if err := s.Remove("b"); err != ErrUnknownID {
		t.Errorf("second Remove = %v, want ErrUnknownID", err)
	}
	for i := 0; i < 200; i++ {
		sample, err := s.Sample(2)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range sample {
			if id == "b" {
				t.Fatal("removed id sampled")
			}
		}
	}
	if err := s.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("c"); err != nil {
		t.Fatal(err)
	}
	if got := s.Total(); got != 0 {
		t.Errorf("Total() after removing all = %d, want 0", got)
	}
	if _, err := s.Sample(1); err != ErrTooManyRequested {
		t.Errorf("Sample on empty set = %v, want ErrTooManyRequested", err)
	}
}

func TestStateRestoreReproducesSequence(t *testing.T) {
	src := NewSplitmix64(99)
	s := New(src)
	for _, ev := range []Event{
		{Op: OpAdd, ID: "a", Weight: 1},
		{Op: OpAdd, ID: "b", Weight: 2},
		{Op: OpAdd, ID: "c", Weight: 3},
		{Op: OpAdd, ID: "d", Weight: 4},
	} {
		if err := s.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Sample(2); err != nil {
		t.Fatal(err)
	}
	snap := src.State()
	first, err := s.Sample(3)
	if err != nil {
		t.Fatal(err)
	}

	// Restore in place.
	src.SetState(snap)
	second, err := s.Sample(3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("after restore got %v, want %v", second, first)
	}

	// Restore into a fresh source and sampler.
	src2 := NewSplitmix64(0)
	src2.SetState(snap)
	s2 := New(src2)
	for _, ev := range []Event{
		{Op: OpAdd, ID: "a", Weight: 1},
		{Op: OpAdd, ID: "b", Weight: 2},
		{Op: OpAdd, ID: "c", Weight: 3},
		{Op: OpAdd, ID: "d", Weight: 4},
	} {
		if err := s2.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	third, err := s2.Sample(3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, third) {
		t.Errorf("fresh source from exported state got %v, want %v", third, first)
	}
}

func TestSameSeedSameSequence(t *testing.T) {
	draw := func() []string {
		s := New(NewSplitmix64(1234))
		for i, id := range []string{"a", "b", "c", "d", "e"} {
			if err := s.Add(id, int64(i+1)); err != nil {
				t.Fatal(err)
			}
		}
		var all []string
		for i := 0; i < 5; i++ {
			sample, err := s.Sample(3)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, sample...)
		}
		return all
	}
	if a, b := draw(), draw(); !reflect.DeepEqual(a, b) {
		t.Errorf("same seed produced different sequences: %v vs %v", a, b)
	}
}

func TestValidation(t *testing.T) {
	s := New(nil)
	if err := s.Add("x", 0); err != ErrNonPositiveWeight {
		t.Errorf("Add zero weight = %v", err)
	}
	if err := s.Add("x", -3); err != ErrNonPositiveWeight {
		t.Errorf("Add negative weight = %v", err)
	}
	if err := s.Add("x", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("x", 1); err != ErrDuplicateID {
		t.Errorf("duplicate Add = %v", err)
	}
	if err := s.Remove("y"); err != ErrUnknownID {
		t.Errorf("Remove unknown = %v", err)
	}
	if err := s.Apply(Event{Op: Op(42), ID: "x"}); err == nil {
		t.Error("unknown op accepted")
	}
	if _, err := s.Sample(-1); err != ErrNegativeK {
		t.Errorf("Sample(-1) = %v", err)
	}
	if _, err := s.Sample(2); err != ErrTooManyRequested {
		t.Errorf("Sample(2) with 1 item = %v", err)
	}
	got, err := s.Sample(0)
	if err != nil || len(got) != 0 {
		t.Errorf("Sample(0) = %v, %v", got, err)
	}
}
