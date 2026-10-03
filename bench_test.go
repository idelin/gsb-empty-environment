package roaring

import (
	"math/rand"
	"testing"
)

// Simulated 百亿级: the full uint32 space (65536 run containers,
// ≈4.29e9 values per bitmap; a 1e10 bitmap shards the same way ~3x).
func BenchmarkAndDenseFullSpace(b *testing.B) {
	const containers = 65536
	bm1, bm2 := New(), New()
	for k := 0; k < containers; k++ {
		bm1.AddRange(uint16(k), 0, 65535)
		bm2.AddRange(uint16(k), 1000, 65535)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out := bm1.And(bm2); out.Cardinality() == 0 {
			b.Fatal("empty intersection")
		}
	}
}

func BenchmarkOrDenseFullSpace(b *testing.B) {
	const containers = 65536
	bm1, bm2 := New(), New()
	for k := 0; k < containers; k++ {
		bm1.AddRange(uint16(k), 0, 60000)
		bm2.AddRange(uint16(k), 1000, 65535)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out := bm1.Or(bm2); out.Cardinality() == 0 {
			b.Fatal("empty union")
		}
	}
}

func BenchmarkAndBitmapContainers(b *testing.B) {
	const containers = 512
	bm1, bm2 := New(), New()
	rnd := rand.New(rand.NewSource(4))
	for k := 0; k < containers; k++ {
		for i := 0; i < 10000; i++ {
			bm1.Add(uint32(k)<<16 | uint32(rnd.Intn(65536)))
			bm2.Add(uint32(k)<<16 | uint32(rnd.Intn(65536)))
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if out := bm1.And(bm2); out.Cardinality() == 0 {
			b.Fatal("empty intersection")
		}
	}
}

func BenchmarkAndSparse(b *testing.B) {
	bm1, bm2 := New(), New()
	rnd := rand.New(rand.NewSource(3))
	for i := 0; i < 100000; i++ {
		bm1.Add(uint32(rnd.Intn(1 << 30)))
		bm2.Add(uint32(rnd.Intn(1 << 30)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bm1.And(bm2)
	}
}
