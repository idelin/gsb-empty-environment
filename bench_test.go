package bitmap

import "testing"

// BenchmarkContainerAndDense measures a single bitmap-container AND:
// 1024 uint64 operations over 8 KiB, the core of all batch ops.
func BenchmarkContainerAndDense(b *testing.B) {
	x := &bitmapContainer{}
	y := &bitmapContainer{}
	x.addRange(0, 0xFFFF)
	y.addRange(0x8000, 0xFFFF)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = intersectBitmaps(x, y)
	}
}

// BenchmarkContainerAndRun measures run-container intersection, which is
// O(number of runs) rather than O(container size).
func BenchmarkContainerAndRun(b *testing.B) {
	x := &runContainer{runs: []interval{{0, 0xFFFF}}}
	y := &runContainer{runs: []interval{{0x8000, 0xFFFF}}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = intersectRuns(x, y)
	}
}

// BenchmarkAndDense100M simulates AND over two bitmaps holding 10^8 bits
// each (dense ranges, RLE-compressed).
func BenchmarkAndDense100M(b *testing.B) {
	x := New()
	y := New()
	x.AddRange(0, 100_000_000)
	y.AddRange(50_000_000, 150_000_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = And(x, y)
	}
}

// BenchmarkOrDense100M is the union counterpart of BenchmarkAndDense100M.
func BenchmarkOrDense100M(b *testing.B) {
	x := New()
	y := New()
	x.AddRange(0, 100_000_000)
	y.AddRange(50_000_000, 150_000_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Or(x, y)
	}
}

// BenchmarkAndFullSpace measures AND over two bitmaps covering the entire
// 2^32 key space (~4.3x10^9 bits), all RLE-compressed into single runs.
func BenchmarkAndFullSpace(b *testing.B) {
	x := New()
	y := New()
	x.AddRange(0, 1<<32)
	y.AddRange(1<<31, 3<<31)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = And(x, y)
	}
}

// BenchmarkAndDense10B simulates AND at 10^10 (百亿) bits. A uint32-keyed
// bitmap tops out at 2^32 bits, so the 10^10-bit space is simulated as its
// equivalent 152588 RLE container pairs, intersected back to back. This
// measures exactly the container-level work a 百亿-scale AND performs.
func BenchmarkAndDense10B(b *testing.B) {
	const containers = 152588 // 10^10 bits / 65536 bits per container
	xs := make([]container, containers)
	ys := make([]container, containers)
	for i := range xs {
		xs[i] = &runContainer{runs: []interval{{0, 0xFFFF}}}
		ys[i] = &runContainer{runs: []interval{{0x8000, 0xFFFF}}}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < containers; j++ {
			_ = and(xs[j], ys[j])
		}
	}
}

// BenchmarkAndSparseRandom measures AND over bitmap containers filled with
// random bits (worst case: no RLE compression possible).
func BenchmarkAndSparseRandom(b *testing.B) {
	x := New()
	y := New()
	// 10 containers x 20000 random bits each -> bitmap containers.
	for i := 0; i < 10; i++ {
		base := uint32(i) << 16
		for j := 0; j < 20000; j++ {
			x.Add(base + uint32(j*3%65536))
			y.Add(base + uint32(j*5%65536))
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = And(x, y)
	}
}

// BenchmarkAddSparse measures point-insert throughput.
func BenchmarkAddSparse(b *testing.B) {
	bm := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm.Add(uint32(i) * 2654435761)
	}
}

// BenchmarkContains measures point-query throughput.
func BenchmarkContains(b *testing.B) {
	bm := New()
	bm.AddRange(0, 1_000_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bm.Contains(uint32(i % 1_000_000))
	}
}

// BenchmarkAndFewRanges measures AND when each bitmap holds only a few
// dense ranges inside a 4.3x10^9-id space -- the classic compressible
// workload, where batch ops complete in microseconds.
func BenchmarkAndFewRanges(b *testing.B) {
	x := New()
	y := New()
	x.AddRange(1_000_000, 1_100_000)
	x.AddRange(1_000_000_000, 1_000_100_000)
	x.AddRange(4_000_000_000, 4_000_100_000)
	y.AddRange(1_050_000, 1_150_000)
	y.AddRange(1_000_050_000, 1_000_150_000)
	y.AddRange(4_000_050_000, 4_000_150_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = And(x, y)
	}
}

// BenchmarkOrFewRanges is the union counterpart of BenchmarkAndFewRanges.
func BenchmarkOrFewRanges(b *testing.B) {
	x := New()
	y := New()
	x.AddRange(1_000_000, 1_100_000)
	x.AddRange(1_000_000_000, 1_000_100_000)
	y.AddRange(1_050_000, 1_150_000)
	y.AddRange(1_000_050_000, 1_000_150_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Or(x, y)
	}
}
