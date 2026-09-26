package efano

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestStreamingBuilderMatchesBatch(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 14))
	ref := randNonDecreasing(rng, 2000, 1<<24)
	l := ChooseL(1<<24, uint64(len(ref)))

	b := NewBuilder(l)
	for _, v := range ref {
		if err := b.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	streamed := b.Build()

	batch, err := Encode(ref, l)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(streamed.MarshalBinary(), batch.MarshalBinary()) {
		t.Fatal("streaming builder output differs from batch Encode")
	}
	// Monotone violation must be reported.
	bad := NewBuilder(l)
	_ = bad.Add(10)
	if err := bad.Add(9); err == nil {
		t.Fatal("decreasing input accepted")
	}
}

func TestMerge(t *testing.T) {
	rng := rand.New(rand.NewPCG(15, 16))
	run := func(name string, aVals, bVals []uint64, l uint) {
		t.Helper()
		a, err := Encode(aVals, l)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Encode(bVals, l)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Merge(a, b)
		if err != nil {
			t.Fatalf("Merge(%s): %v", name, err)
		}
		want := append(slices.Clone(aVals), bVals...)
		slices.Sort(want)
		if got := m.Decode(); !slices.Equal(got, want) {
			t.Fatalf("Merge(%s) decode mismatch:\n got %v\nwant %v", name, got, want)
		}
		if err := m.Verify(); err != nil {
			t.Fatal(err)
		}
		// Ranks must survive the merge exactly.
		for _, q := range []uint64{0, 5, 1 << 10, 1 << 30} {
			if got, want := m.LowerBound(q), refLowerBound(want, q); got != want {
				t.Fatalf("Merge(%s) LowerBound(%d) = %d, want %d", name, q, got, want)
			}
		}
	}

	l := ChooseL(1<<20, 64)
	run("both-empty", nil, nil, l)
	run("one-empty", randNonDecreasing(rng, 100, 1<<20), nil, l)
	run("empty-second", nil, randNonDecreasing(rng, 100, 1<<20), l)
	run("disjoint", []uint64{1, 2, 3}, []uint64{10, 20, 30}, l)
	run("interleaved", []uint64{1, 3, 5, 7}, []uint64{2, 4, 6, 8}, l)
	run("cross-duplicates", []uint64{5, 5, 5}, []uint64{5, 5}, l)
	run("all-equal-both", slices.Repeat([]uint64{9}, 50), slices.Repeat([]uint64{9}, 30), l)

	// Randomized: split one sorted reference into two sorted parts.
	for trial := 0; trial < 20; trial++ {
		ref := randNonDecreasing(rng, 1+rng.IntN(500), 1<<20)
		var pa, pb []uint64
		for _, v := range ref {
			if rng.Uint64()%2 == 0 {
				pa = append(pa, v)
			} else {
				pb = append(pb, v)
			}
		}
		run("random-split", pa, pb, l)
	}

	// Mismatched low widths are rejected, not silently decoded.
	a, _ := Encode([]uint64{1, 2}, 4)
	b, _ := Encode([]uint64{1, 2}, 5)
	if _, err := Merge(a, b); err == nil {
		t.Fatal("merge with different l accepted")
	}
}

func TestMergePreservesDuplicateRanks(t *testing.T) {
	l := uint(2)
	a, _ := Encode([]uint64{4, 4, 8}, l)
	b, _ := Encode([]uint64{4, 8, 8, 12}, l)
	m, err := Merge(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if s, e := m.EqualRange(4); s != 0 || e != 3 {
		t.Fatalf("EqualRange(4) = (%d,%d), want (0,3)", s, e)
	}
	if s, e := m.EqualRange(8); s != 3 || e != 6 {
		t.Fatalf("EqualRange(8) = (%d,%d), want (3,6)", s, e)
	}
	if got := m.LowerBound(13); got != 7 {
		t.Fatalf("LowerBound(13) = %d, want 7", got)
	}
}
