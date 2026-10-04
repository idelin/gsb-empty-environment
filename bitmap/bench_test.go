package bitmap

import "testing"

func densePair() (*Bitmap, *Bitmap) {
	a := New()
	a.AddRange(0, 5000000000)
	c := New()
	c.AddRange(2500000000, 7500000000)
	return a, c
}

func BenchmarkAndDense5e9(b *testing.B) {
	a, c := densePair()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.And(c)
	}
}

func BenchmarkOrDense5e9(b *testing.B) {
	a, c := densePair()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.Or(c)
	}
}

func BenchmarkXorDense5e9(b *testing.B) {
	a, c := densePair()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.Xor(c)
	}
}

func BenchmarkAndSparseHugeSpace(b *testing.B) {
	a, c := New(), New()
	for i := uint64(0); i < 100000; i++ {
		a.Add(i * 99991)
		c.Add(i*99991 + 7)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.And(c)
	}
}

func BenchmarkAddRangeDense(b *testing.B) {
	for i := 0; i < b.N; i++ {
		bm := New()
		bm.AddRange(0, 1000000000)
	}
}

func BenchmarkAddSparse(b *testing.B) {
	bm := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm.Add(uint64(i) * 7919)
	}
}
