package main

import (
	"math/big"
	"testing"
)

func bigWeights(vals ...int64) []*big.Int {
	w := make([]*big.Int, len(vals))
	for i, v := range vals {
		w[i] = big.NewInt(v)
	}
	return w
}

func indices(draws []Draw) []int {
	out := make([]int, len(draws))
	for i, d := range draws {
		out[i] = d.BlockIndex
	}
	return out
}

func TestSampleReproducible(t *testing.T) {
	w := bigWeights(10, 20, 30, 40)
	a, err := Sample("seed-1", w, 3)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Sample("seed-1", w, 3)
	if err != nil {
		t.Fatal(err)
	}
	ia, ib := indices(a), indices(b)
	for i := range ia {
		if ia[i] != ib[i] {
			t.Fatalf("same seed gave different selections: %v vs %v", ia, ib)
		}
	}
}

func TestSampleWithoutReplacement(t *testing.T) {
	w := bigWeights(5, 5, 5, 5, 5)
	draws, err := Sample("seed-2", w, 5)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, d := range draws {
		if seen[d.BlockIndex] {
			t.Fatalf("block %d selected twice (with replacement!)", d.BlockIndex)
		}
		seen[d.BlockIndex] = true
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 distinct blocks, got %d", len(seen))
	}
}

// Resuming or repeating sampling with the same seed must never change
// blocks that were already selected: the k-draw result is a strict prefix
// of the (k+m)-draw result.
func TestSamplePrefixStability(t *testing.T) {
	w := bigWeights(3, 1, 4, 1, 5, 9, 2, 6)
	short, err := Sample("resume-seed", w, 3)
	if err != nil {
		t.Fatal(err)
	}
	long, err := Sample("resume-seed", w, 7)
	if err != nil {
		t.Fatal(err)
	}
	is, il := indices(short), indices(long)
	for i := range is {
		if is[i] != il[i] {
			t.Fatalf("resuming changed earlier selection: %v vs %v", is, il)
		}
	}
}

func TestSampleHugeWeights(t *testing.T) {
	// Weights beyond int64 range; total length must be handled as big.Int.
	huge := new(big.Int).Lsh(big.NewInt(1), 100) // 2^100
	w := []*big.Int{
		new(big.Int).Set(huge),
		new(big.Int).Add(huge, big.NewInt(12345)),
		big.NewInt(7),
	}
	draws, err := Sample("big-seed", w, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(draws) != 3 {
		t.Fatalf("expected 3 draws, got %d", len(draws))
	}
	seen := map[int]bool{}
	for _, d := range draws {
		if d.Random.Cmp(d.Remaining) >= 0 || d.Random.Sign() < 0 {
			t.Fatalf("random %s out of range [0, %s)", d.Random, d.Remaining)
		}
		if seen[d.BlockIndex] {
			t.Fatalf("duplicate block %d", d.BlockIndex)
		}
		seen[d.BlockIndex] = true
	}
}

func TestSampleZeroWeightNeverSelected(t *testing.T) {
	w := bigWeights(0, 10, 0, 10)
	for _, seed := range []string{"a", "b", "c", "d", "e"} {
		draws, err := Sample(seed, w, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range draws {
			if d.BlockIndex == 0 || d.BlockIndex == 2 {
				t.Fatalf("zero-weight block %d selected", d.BlockIndex)
			}
		}
	}
}

// scriptedSource feeds predetermined bytes to uniformBelow.
type scriptedSource struct {
	chunks [][]byte
}

func (s *scriptedSource) Read(p []byte) {
	if len(s.chunks) == 0 {
		for i := range p {
			p[i] = 0
		}
		return
	}
	c := s.chunks[0]
	s.chunks = s.chunks[1:]
	copy(p, c)
}

func TestUniformBelowRejectsOutOfRange(t *testing.T) {
	// Bound 3 needs 2 bits; candidates 3, 7, ... must be rejected, the
	// first in-range candidate accepted. No modulo folding may occur.
	src := &scriptedSource{chunks: [][]byte{{3}, {7}, {2}}}
	v := uniformBelow(src, big.NewInt(3))
	if v.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("expected 2 after rejecting 3 and 7, got %s", v)
	}
	if len(src.chunks) != 0 {
		t.Fatalf("expected exactly 3 candidates consumed, %d chunks left", len(src.chunks))
	}
}

func TestUniformBelowMasksExcessBits(t *testing.T) {
	// Bound 5 needs 3 bits; byte 0xFF must be masked to 0x07 = 7, which is
	// >= 5 and rejected; next byte 0x04 accepted.
	src := &scriptedSource{chunks: [][]byte{{0xFF}, {0x04}}}
	v := uniformBelow(src, big.NewInt(5))
	if v.Cmp(big.NewInt(4)) != 0 {
		t.Fatalf("expected 4, got %s", v)
	}
}

func TestUniformBelowLargeBound(t *testing.T) {
	d := NewDRBG("range-check")
	// Bound with bit length not a multiple of 8, larger than uint64.
	n := new(big.Int).Lsh(big.NewInt(1), 100)
	n.Add(n, big.NewInt(999))
	for i := 0; i < 1000; i++ {
		v := uniformBelow(d, n)
		if v.Sign() < 0 || v.Cmp(n) >= 0 {
			t.Fatalf("value %s out of range [0, %s)", v, n)
		}
	}
}
