package roaring

import (
	"math/rand"
	"testing"
)

// denseBitmap builds a bitmap with n fully populated containers
// (n * 65536 values).
func denseBitmap(n int, offset uint32) *Bitmap {
	b := New()
	for k := 0; k < n; k++ {
		lo := (offset + uint32(k)) << 16
		b.AddRange(lo, lo+0xFFFF)
	}
	return b
}

// sparseBitmap builds a bitmap with n values spread over the key space.
func sparseBitmap(rng *rand.Rand, n int) *Bitmap {
	b := New()
	xs := make([]uint32, n)
	for i := range xs {
		xs[i] = rng.Uint32()
	}
	b.AddMany(xs)
	return b
}

func BenchmarkAddSparse(b *testing.B) {
	bm := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm.Add(uint32(i) * 2654435761)
	}
}

func BenchmarkAddRangeDense(b *testing.B) {
	bm := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm.AddRange(0, 1<<20)
	}
}

func BenchmarkContains(b *testing.B) {
	bm := denseBitmap(64, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm.Contains(uint32(i))
	}
}

func benchmarkBinaryOp(b *testing.B, containers int, op func(a, x *Bitmap) *Bitmap) {
	a := denseBitmap(containers, 0)
	x := denseBitmap(containers, uint32(containers/2))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op(a, x)
	}
}

func BenchmarkAndDense(b *testing.B) {
	benchmarkBinaryOp(b, 1024, func(a, x *Bitmap) *Bitmap { return a.And(x) })
}
func BenchmarkOrDense(b *testing.B) {
	benchmarkBinaryOp(b, 1024, func(a, x *Bitmap) *Bitmap { return a.Or(x) })
}
func BenchmarkXorDense(b *testing.B) {
	benchmarkBinaryOp(b, 1024, func(a, x *Bitmap) *Bitmap { return a.Xor(x) })
}

func BenchmarkParallelAndDense(b *testing.B) {
	a := denseBitmap(1024, 0)
	x := denseBitmap(1024, 512)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.ParallelAnd(x, 8)
	}
}

func BenchmarkParallelOrDense(b *testing.B) {
	a := denseBitmap(1024, 0)
	x := denseBitmap(1024, 512)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.ParallelOr(x, 8)
	}
}

func BenchmarkQueuePushPop(b *testing.B) {
	q := NewLFQueue(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Push(i)
		q.Pop()
	}
}

func BenchmarkQueueMPMC(b *testing.B) {
	q := NewLFQueue(1024)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				q.Pop()
			}
		}
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for !q.Push(i) {
		}
	}
	b.StopTimer()
	close(done)
}
