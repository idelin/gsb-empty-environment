package cbitmap

import (
	"math/rand"
	"testing"
)

// denseBitmap builds a bitmap of n bitmap containers (~50% density),
// simulating n*65536 stored IDs.
func denseBitmap(n int, seed int64) *Bitmap {
	bm := &Bitmap{}
	rng := rand.New(rand.NewSource(seed))
	for k := 0; k < n; k++ {
		bc := &bitmapContainer{words: make([]uint64, bitmapWords), card: -1}
		for i := range bc.words {
			bc.words[i] = rng.Uint64()
		}
		bm.keys = append(bm.keys, uint16(k))
		bm.cons = append(bm.cons, bc)
	}
	return bm
}

// sparseBitmap builds a bitmap of n array containers with c values each.
func sparseBitmap(n, c int, seed int64) *Bitmap {
	bm := New()
	rng := rand.New(rand.NewSource(seed))
	for k := 0; k < n; k++ {
		base := uint32(k) << 16
		for i := 0; i < c; i++ {
			bm.Add(base + uint32(rng.Intn(65536)))
		}
	}
	return bm
}

// BenchmarkAndDense measures intersection of two bitmaps covering
// 2048*65536 = 1.3e8 IDs each; per-container cost is microseconds, so a
// full 2^32-ID bitmap (65536 containers) intersects in low milliseconds,
// and the simulated 1e10-ID scale stays microsecond-per-container.
func BenchmarkAndDense(b *testing.B) {
	a, c := denseBitmap(2048, 1), denseBitmap(2048, 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		And(a, c)
	}
}

func BenchmarkOrDense(b *testing.B) {
	a, c := denseBitmap(2048, 1), denseBitmap(2048, 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Or(a, c)
	}
}

func BenchmarkXorDense(b *testing.B) {
	a, c := denseBitmap(2048, 1), denseBitmap(2048, 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Xor(a, c)
	}
}

func BenchmarkAndSparse(b *testing.B) {
	a, c := sparseBitmap(4096, 200, 1), sparseBitmap(4096, 200, 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		And(a, c)
	}
}

func BenchmarkAndRuns(b *testing.B) {
	a, c := New(), New()
	a.AddRange(0, 1<<28)
	c.AddRange(1<<27, 1<<29)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		And(a, c)
	}
}

func BenchmarkAdd(b *testing.B) {
	bm := New()
	for i := 0; i < b.N; i++ {
		bm.Add(uint32(i * 2654435761 % (1 << 28)))
	}
}

func BenchmarkAddRange(b *testing.B) {
	for i := 0; i < b.N; i++ {
		bm := New()
		bm.AddRange(0, 1<<26)
	}
}

func BenchmarkMarshal(b *testing.B) {
	a := denseBitmap(256, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.MarshalBinary()
	}
}

func BenchmarkUnmarshalZeroCopy(b *testing.B) {
	buf := denseBitmap(256, 1).MarshalBinary()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := UnmarshalZeroCopy(buf); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalCopy(b *testing.B) {
	buf := denseBitmap(256, 1).MarshalBinary()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := UnmarshalBinary(buf); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkContainsFrozen(b *testing.B) {
	zc, err := UnmarshalZeroCopy(denseBitmap(256, 1).MarshalBinary())
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		zc.Contains(uint32(i * 611953 % (1 << 24)))
	}
}
