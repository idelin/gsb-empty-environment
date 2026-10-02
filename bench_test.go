package roaring

import (
	"math/rand"
	"sort"
	"testing"
)

// 10^10-scale simulation: two dense bitmaps of 5 billion IDs each,
// stored as RLE containers. Intersection/union cost is O(containers),
// not O(cardinality).
func buildDenseRange(start, end uint64) *Bitmap {
	b := New()
	b.AddRange(start, end)
	return b
}

func BenchmarkAnd10B(b *testing.B) {
	A := buildDenseRange(0, 5000000000)
	B := buildDenseRange(2500000000, 7500000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = A.And(B)
	}
}

func BenchmarkOr10B(b *testing.B) {
	A := buildDenseRange(0, 5000000000)
	B := buildDenseRange(2500000000, 7500000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = A.Or(B)
	}
}

func BenchmarkXor10B(b *testing.B) {
	A := buildDenseRange(0, 5000000000)
	B := buildDenseRange(2500000000, 7500000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = A.Xor(B)
	}
}

// Word-level throughput on dense bitmap containers (10M random IDs each).
func BenchmarkAndBitmapContainers(b *testing.B) {
	rng := rand.New(rand.NewSource(5))
	// Pre-sort so setup inserts are O(1) appends; the benchmark itself
	// measures word-level AND on bitmap containers.
	mk := func() *Bitmap {
		ids := make([]uint64, 10000000)
		for i := range ids {
			ids[i] = uint64(rng.Intn(1 << 28))
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		bm := New()
		for _, id := range ids {
			bm.Add(id)
		}
		return bm
	}
	A, B := mk(), mk()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = A.And(B)
	}
}

func BenchmarkAddSparse(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	ids := make([]uint64, 1000000)
	for i := range ids {
		ids[i] = uint64(rng.Intn(1 << 24))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm := New()
		for _, id := range ids {
			bm.Add(id)
		}
	}
}
