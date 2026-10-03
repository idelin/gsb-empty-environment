package bitmap

import (
	"math/rand"
	"testing"
)

// denseBitmaps 直接构造两个各含 n 个 BitmapContainer 的位图
// （每容器 65536 个值；n=1526 时即约 1 亿元素，n=152588 时约百亿）。
func denseBitmaps(n int) (a, b *Bitmap) {
	a, b = New(), New()
	mk := func() *bitmapContainer {
		bc := &bitmapContainer{card: 1 << 16}
		for i := range bc.words {
			bc.words[i] = ^uint64(0)
		}
		return bc
	}
	for k := 0; k < n; k++ {
		a.keys = append(a.keys, uint16(k))
		a.ctrs = append(a.ctrs, mk())
		a.gens = append(a.gens, 1)
		b.keys = append(b.keys, uint16(k))
		b.ctrs = append(b.ctrs, mk())
		b.gens = append(b.gens, 1)
	}
	a.gen, b.gen = 1, 1
	return a, b
}

func runContainers(n int) (a, b *Bitmap) {
	a, b = New(), New()
	for k := uint64(0); k < uint64(n); k++ {
		a.AddRange(k<<16, (k+1)<<16)
		b.AddRange(k<<16, (k+1)<<16)
	}
	return a, b
}

func BenchmarkAndDense(b *testing.B) {
	x, y := denseBitmaps(512) // 2 x 3355 万元素
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		And(x, y)
	}
}

func BenchmarkOrDense(b *testing.B) {
	x, y := denseBitmaps(512)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Or(x, y)
	}
}

func BenchmarkXorDense(b *testing.B) {
	x, y := denseBitmaps(512)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Xor(x, y)
	}
}

func BenchmarkAndRunContainers(b *testing.B) {
	x, y := runContainers(512)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		And(x, y)
	}
}

func BenchmarkAndSparse(b *testing.B) {
	rnd := rand.New(rand.NewSource(9))
	x, y := New(), New()
	for i := 0; i < 100000; i++ {
		x.Add(uint32(rnd.Intn(1 << 28)))
		y.Add(uint32(rnd.Intn(1 << 28)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		And(x, y)
	}
}

func BenchmarkSnapshot(b *testing.B) {
	bm, _ := runContainers(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := bm.Snapshot()
		s.Release()
	}
}

func BenchmarkAddDense(b *testing.B) {
	bm := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm.Add(uint32(i % (1 << 22)))
	}
}
