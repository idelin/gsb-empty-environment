package roaring

import (
	"math/rand"
	"testing"
)

// BenchmarkIntersectBitmapContainers measures AND on dense data stored in
// bitmap containers (10M IDs per bitmap).
func BenchmarkIntersectBitmapContainers(b *testing.B) {
	x, y := New(), New()
	for v := uint32(0); v < 20_000_000; v += 2 {
		x.Add(v)
	}
	for v := uint32(1); v < 20_000_000; v += 2 {
		y.Add(v)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := x.And(y)
		_ = r.Cardinality()
	}
}

// BenchmarkUnionBitmapContainers measures OR on dense bitmap containers.
func BenchmarkUnionBitmapContainers(b *testing.B) {
	x, y := New(), New()
	for v := uint32(0); v < 20_000_000; v += 2 {
		x.Add(v)
	}
	for v := uint32(1); v < 20_000_000; v += 2 {
		y.Add(v)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := x.Or(y)
		_ = r.Cardinality()
	}
}

// BenchmarkIntersectRunContainers measures AND across a key space of
// billions of IDs, stored compactly in run containers.
func BenchmarkIntersectRunContainers(b *testing.B) {
	x := New()
	x.AddRange(0, 3_000_000_000)
	y := New()
	y.AddRange(1_000_000_000, MaxValue)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := x.And(y)
		_ = r.Cardinality()
	}
}

// BenchmarkUnionRunContainers measures OR across billions of IDs in RLE form.
func BenchmarkUnionRunContainers(b *testing.B) {
	x := New()
	x.AddRange(0, 3_000_000_000)
	y := New()
	y.AddRange(1_000_000_000, MaxValue)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := x.Or(y)
		_ = r.Cardinality()
	}
}

// BenchmarkIntersectSparse measures AND on small sparse sets (array containers).
func BenchmarkIntersectSparse(b *testing.B) {
	rng := rand.New(rand.NewSource(3))
	x, y := New(), New()
	for i := 0; i < 1000; i++ {
		x.Add(uint32(rng.Intn(1 << 22)))
		y.Add(uint32(rng.Intn(1 << 22)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := x.And(y)
		_ = r.Cardinality()
	}
}

// BenchmarkAddDense measures dense range insertion.
func BenchmarkAddDense(b *testing.B) {
	for i := 0; i < b.N; i++ {
		bm := New()
		bm.AddRange(0, 10_000_000)
		_ = bm.Cardinality()
	}
}
