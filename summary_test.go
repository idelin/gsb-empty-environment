package freqsketch

import (
	"math/rand"
	"testing"
)

type update struct {
	item int
	n    uint64
}

// makeStream builds a skewed stream: 80% of updates hit 20 hot items, the
// rest spread over a long tail of 5000 items.
func makeStream(r *rand.Rand, n int) []update {
	stream := make([]update, n)
	for i := range stream {
		item := 20 + r.Intn(5000)
		if r.Intn(100) < 80 {
			item = r.Intn(20)
		}
		stream[i] = update{item: item, n: uint64(1 + r.Intn(4))}
	}
	return stream
}

func exactCounts(stream []update) (map[int]uint64, uint64) {
	exact := make(map[int]uint64)
	var total uint64
	for _, u := range stream {
		exact[u.item] += u.n
		total += u.n
	}
	return exact, total
}

// verifyAgainstExact independently checks every reported bound against full
// exact counts.
func verifyAgainstExact(t *testing.T, s *Summary[int], exact map[int]uint64, total uint64) {
	t.Helper()
	if s.Total() != total {
		t.Fatalf("total = %d, want %d", s.Total(), total)
	}
	if s.Len() > s.Capacity() {
		t.Fatalf("len %d exceeds capacity %d", s.Len(), s.Capacity())
	}
	if total > 0 && s.MaxError() > total/uint64(s.Capacity()) {
		t.Fatalf("MaxError %d exceeds total/capacity = %d", s.MaxError(), total/uint64(s.Capacity()))
	}
	for _, est := range s.Estimates() {
		truth, seen := exact[est.Item]
		if !seen {
			t.Fatalf("item %d reported but never appeared in the stream", est.Item)
		}
		if est.Count > truth {
			t.Fatalf("item %d: lower bound %d above true count %d", est.Item, est.Count, truth)
		}
		if truth > est.Count+est.Err {
			t.Fatalf("item %d: true count %d above upper bound %d", est.Item, truth, est.Count+est.Err)
		}
		if est.Err > s.MaxError() {
			t.Fatalf("item %d: err %d exceeds MaxError %d", est.Item, est.Err, s.MaxError())
		}
		if est.Err == 0 && est.Count != truth {
			t.Fatalf("item %d: reported as exact (%d) but true count is %d", est.Item, est.Count, truth)
		}
		count, err, ok := s.Lookup(est.Item)
		if !ok || count != est.Count || err != est.Err {
			t.Fatalf("item %d: Lookup (%d,%d,%v) inconsistent with Estimates (%d,%d)",
				est.Item, count, err, ok, est.Count, est.Err)
		}
	}
}

func TestAddBoundsAgainstExactCounts(t *testing.T) {
	for _, capacity := range []int{1, 2, 7, 33, 256} {
		for _, seed := range []int64{1, 2, 3} {
			r := rand.New(rand.NewSource(seed))
			stream := makeStream(r, 20000)
			exact, total := exactCounts(stream)
			s := New[int](capacity)
			for _, u := range stream {
				s.Add(u.item, u.n)
			}
			verifyAgainstExact(t, s, exact, total)
		}
	}
}

func TestMergePreservesTotalAndLocalError(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	stream := makeStream(r, 50000)
	exact, total := exactCounts(stream)

	const blocks = 8
	const capacity = 12
	parts := make([]*Summary[int], blocks)
	var sumTotals uint64
	for b := 0; b < blocks; b++ {
		part := New[int](capacity)
		for _, u := range stream[b*len(stream)/blocks : (b+1)*len(stream)/blocks] {
			part.Add(u.item, u.n)
		}
		sumTotals += part.Total()
		parts[b] = part
	}
	if sumTotals != total {
		t.Fatalf("block totals sum to %d, want %d", sumTotals, total)
	}

	merged := parts[0]
	for b := 1; b < blocks; b++ {
		merged.Merge(parts[b])
	}
	verifyAgainstExact(t, merged, exact, total)
}

// TestMergeOrdersAndBlockings merges the same stream split into blocks in
// different orders (left fold, right fold, balanced tree) and with different
// block sizes. Estimates need not be identical across orders, but every
// reported bound must hold against exact counts.
func TestMergeOrdersAndBlockings(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	stream := makeStream(r, 60000)
	exact, total := exactCounts(stream)
	const capacity = 10

	build := func(blocks int) []*Summary[int] {
		parts := make([]*Summary[int], blocks)
		for b := 0; b < blocks; b++ {
			lo := b * len(stream) / blocks
			hi := (b + 1) * len(stream) / blocks
			part := New[int](capacity)
			for _, u := range stream[lo:hi] {
				part.Add(u.item, u.n)
			}
			parts[b] = part
		}
		return parts
	}
	leftFold := func(parts []*Summary[int]) *Summary[int] {
		acc := parts[0]
		for _, p := range parts[1:] {
			acc.Merge(p)
		}
		return acc
	}
	rightFold := func(parts []*Summary[int]) *Summary[int] {
		acc := parts[len(parts)-1]
		for i := len(parts) - 2; i >= 0; i-- {
			parts[i].Merge(acc)
			acc = parts[i]
		}
		return acc
	}
	tree := func(parts []*Summary[int]) *Summary[int] {
		level := parts
		for len(level) > 1 {
			var next []*Summary[int]
			for i := 0; i < len(level); i += 2 {
				if i+1 == len(level) {
					next = append(next, level[i])
				} else {
					level[i].Merge(level[i+1])
					next = append(next, level[i])
				}
			}
			level = next
		}
		return level[0]
	}

	for _, blocks := range []int{3, 8, 16} {
		orders := map[string]*Summary[int]{
			"left":  leftFold(build(blocks)),
			"right": rightFold(build(blocks)),
			"tree":  tree(build(blocks)),
		}
		for name, merged := range orders {
			verifyAgainstExact(t, merged, exact, total)
			if merged.Total() != total {
				t.Fatalf("blocks=%d order=%s: total %d, want %d", blocks, name, merged.Total(), total)
			}
			t.Logf("blocks=%d order=%s: monitored=%d MaxError=%d (bound=%d)",
				blocks, name, merged.Len(), merged.MaxError(), total/uint64(capacity))
		}
	}
}

// TestUnseenItemsNeverReportedExact ensures unseen items are absent from the
// summary and that anything reported with zero error is truly exact.
func TestUnseenItemsNeverReportedExact(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	stream := makeStream(r, 30000)
	exact, total := exactCounts(stream)

	s := New[int](5)
	for _, u := range stream {
		s.Add(u.item, u.n)
	}
	verifyAgainstExact(t, s, exact, total)

	reported := make(map[int]bool)
	for _, est := range s.Estimates() {
		reported[est.Item] = true
	}
	for item := 0; item < 6000; item++ {
		if _, seen := exact[item]; seen {
			continue
		}
		if reported[item] {
			t.Fatalf("unseen item %d appears in the summary", item)
		}
		if _, _, ok := s.Lookup(item); ok {
			t.Fatalf("unseen item %d returned by Lookup", item)
		}
	}
}

func TestInvalidInput(t *testing.T) {
	mustPanic := func(name string, f func()) {
		defer func() {
			if recover() == nil {
				t.Fatalf("%s: expected panic", name)
			}
		}()
		f()
	}
	mustPanic("zero capacity", func() { New[int](0) })
	mustPanic("zero count update", func() { New[int](4).Add(1, 0) })
	mustPanic("capacity mismatch", func() {
		a, b := New[int](4), New[int](5)
		a.Merge(b)
	})
}

func TestWeightedHeavyHitter(t *testing.T) {
	s := New[string](3)
	for i := 0; i < 1000; i++ {
		s.Add("hot", 3)
		s.Add("cold-a", 1)
		s.Add("cold-b", 1)
	}
	count, err, ok := s.Lookup("hot")
	if !ok {
		t.Fatal("heavy hitter evicted")
	}
	if count > 3000 || 3000 > count+err {
		t.Fatalf("hot: true count 3000 outside [%d, %d]", count, count+err)
	}
	if s.Total() != 5000 {
		t.Fatalf("total = %d, want 5000", s.Total())
	}
}
