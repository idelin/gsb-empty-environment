package bitmap

import (
	"strconv"
	"sync"
	"testing"
)

// BenchmarkContainerAndBitmapBitmap: intersection of two full 65536-bit
// containers — the microsecond-scale core of bitmap AND.
func BenchmarkContainerAndBitmapBitmap(b *testing.B) {
	ca := &bitmapContainer{}
	cb := &bitmapContainer{}
	for i := 0; i < bitmapWordCount; i++ {
		ca.words[i] = 0xAAAAAAAAAAAAAAAA
		cb.words[i] = 0xCCCCCCCCCCCCCCCC
	}
	ca.recount()
	cb.recount()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		andContainers(ca, cb)
	}
}

func BenchmarkContainerOrBitmapBitmap(b *testing.B) {
	ca := &bitmapContainer{}
	cb := &bitmapContainer{}
	for i := 0; i < bitmapWordCount; i++ {
		ca.words[i] = 0xAAAAAAAAAAAAAAAA
		cb.words[i] = 0xCCCCCCCCCCCCCCCC
	}
	ca.recount()
	cb.recount()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		orContainers(ca, cb)
	}
}

func BenchmarkContainerAndRunRun(b *testing.B) {
	ca := &runContainer{runs: []interval{{0, 30000}, {40000, 20000}}}
	cb := &runContainer{runs: []interval{{10000, 30000}, {50000, 15535}}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		andContainers(ca, cb)
	}
}

// BenchmarkBitmapAndDense10B simulates 10^10-element bitmaps: dense ranges
// stored as RLE containers (tiny memory), intersected wholesale.
func BenchmarkBitmapAndDense10B(b *testing.B) {
	const total = uint64(10000000000) // 10^10 IDs
	a := New()
	a.AddRange(0, total-1)
	c := New()
	c.AddRange(total/2, total+total/2-1)
	b.Logf("containers: a=%d c=%d", a.Stats().Containers, c.Stats().Containers)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := a.And(c)
		if r.Cardinality() != total/2 {
			b.Fatalf("bad intersection")
		}
	}
}

// BenchmarkBitmapAndSparse: two bitmaps of 1M scattered IDs each.
func BenchmarkBitmapAndSparse(b *testing.B) {
	a, c := New(), New()
	// Deterministic pseudo-random sparse IDs.
	for i := uint64(0); i < 1000000; i++ {
		a.Add((i * 7919) % (1 << 32))
		c.Add((i*104729 + 5000) % (1 << 32))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.And(c)
	}
}

// BenchmarkBitmapOrSparse: union of two 1M-ID sparse bitmaps.
func BenchmarkBitmapOrSparse(b *testing.B) {
	a, c := New(), New()
	for i := uint64(0); i < 1000000; i++ {
		a.Add((i * 7919) % (1 << 32))
		c.Add((i*104729 + 5000) % (1 << 32))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Or(c)
	}
}

// BenchmarkConcurrentAdd: N nodes racing inserts into one bitmap.
func BenchmarkConcurrentAdd(b *testing.B) {
	for _, nodes := range []int{1, 4, 16} {
		b.Run("nodes="+strconv.Itoa(nodes), func(b *testing.B) {
			bm := New()
			b.ResetTimer()
			var wg sync.WaitGroup
			per := b.N / nodes
			if per < 1 {
				per = 1
			}
			for n := 0; n < nodes; n++ {
				wg.Add(1)
				go func(node int) {
					defer wg.Done()
					nd := bm.Node(node)
					base := uint64(node * per)
					for i := 0; i < per; i++ {
						nd.Add(base + uint64(i))
					}
				}(n)
			}
			wg.Wait()
		})
	}
}

// BenchmarkAddRange: dense range insertion (RLE fast path).
func BenchmarkAddRange(b *testing.B) {
	bm := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm.AddRange(uint64(i)*1000000, uint64(i)*1000000+999999)
	}
}
