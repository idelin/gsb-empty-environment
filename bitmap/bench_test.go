package bitmap

import "testing"

func buildRange(lo, hi uint64) *Bitmap {
	b := New()
	b.AddRange(lo, hi)
	return b
}

func BenchmarkAndLarge(b *testing.B) {
	a := buildRange(0, 3500000000)
	c := buildRange(1000000000, 1<<32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		And(a, c)
	}
}

func BenchmarkOrLarge(b *testing.B) {
	a := buildRange(0, 3500000000)
	c := buildRange(1000000000, 1<<32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Or(a, c)
	}
}

func BenchmarkXorLarge(b *testing.B) {
	a := buildRange(0, 3500000000)
	c := buildRange(1000000000, 1<<32)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Xor(a, c)
	}
}
