package bitmapckpt

import (
	"math/rand"
	"testing"
)

// buildDenseRunBitmap simulates a bitmap holding ~n values as fully dense
// run containers (the RLE best case: 10^10 values need only ~152k tiny
// containers instead of 1.25 GB of raw bits).
func buildDenseRunBitmap(n uint64) *Bitmap {
	bm := New()
	full := n / containerSize
	for k := uint64(0); k < full; k++ {
		bm.AddRange(k<<16, (k+1)<<16)
	}
	if rem := n % containerSize; rem > 0 {
		bm.AddRange(full<<16, full<<16+rem)
	}
	return bm
}

// BenchmarkAndTenBillionVsSparse intersects a ~10^10-value bitmap with a
// sparse one: the sparse side drives the loop, so the operation finishes in
// microseconds regardless of the dense side's magnitude.
func BenchmarkAndTenBillionVsSparse(b *testing.B) {
	dense := buildDenseRunBitmap(10000000000)
	sparse := New()
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 10000; i++ {
		sparse.Add(uint64(r.Intn(1 << 40)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dense.And(sparse)
	}
}

// BenchmarkAndTenBillionRunContainers intersects two ~10^10-value bitmaps
// made of run containers: ~152k interval intersections per operation.
func BenchmarkAndTenBillionRunContainers(b *testing.B) {
	x := buildDenseRunBitmap(10000000000)
	y := buildDenseRunBitmap(10000000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.And(y)
	}
}

// BenchmarkAndDenseBitmapContainers measures raw word-level intersection of
// two bitmap containers covering 65536 values each.
func BenchmarkAndDenseBitmapContainers(b *testing.B) {
	x := New()
	y := New()
	x.AddRange(0, 65536)
	y.AddRange(0, 65536)
	x.Optimize() // force run; convert to bitmap via OR with a dense bitmap
	z := New()
	for i := 0; i < 5000; i++ {
		z.Add(uint64(i * 13 % 65536))
	}
	xb := x.Or(z) // key 0 becomes a full bitmap container
	yb := y.Or(z)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = xb.And(yb)
	}
}

// BenchmarkOrTenBillionRunContainers unions two ~10^10-value run bitmaps.
func BenchmarkOrTenBillionRunContainers(b *testing.B) {
	x := buildDenseRunBitmap(10000000000)
	y := buildDenseRunBitmap(10000000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Or(y)
	}
}

// BenchmarkXorDenseBitmapContainers measures word-level XOR on dense
// containers.
func BenchmarkXorDenseBitmapContainers(b *testing.B) {
	x := New()
	y := New()
	for i := 0; i < 20000; i++ {
		x.Add(uint64(i * 3 % 65536))
		y.Add(uint64(i * 7 % 65536))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Xor(y)
	}
}

// BenchmarkIncrementalSnapshotPause measures the stop-the-world pause of an
// incremental snapshot on a 1M-container bitmap where only 100 containers
// were modified since the last snapshot.
func BenchmarkIncrementalSnapshotPause(b *testing.B) {
	bm := buildDenseRunBitmap(65536 * 100000) // 100k containers
	bm.Snapshot()                             // reset dirty set
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for k := 0; k < 100; k++ {
			bm.Add(uint64(k*7919) + 1)
		}
		b.StartTimer()
		_ = bm.Snapshot()
	}
}

// BenchmarkCheckpointFull measures a full checkpoint on 100k containers.
func BenchmarkCheckpointFull(b *testing.B) {
	bm := buildDenseRunBitmap(65536 * 100000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bm.Checkpoint()
	}
}

// BenchmarkAdd measures single-value insert throughput.
func BenchmarkAdd(b *testing.B) {
	bm := New()
	r := rand.New(rand.NewSource(1))
	vals := make([]uint64, b.N)
	for i := range vals {
		vals[i] = uint64(r.Uint32())
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm.Add(vals[i])
	}
}
