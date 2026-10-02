package roaring

import (
	"math/rand"
	"sort"
	"testing"
)

// denseBitmaps builds two bitmaps with n fully-populated bitmap containers
// (random 50% density), n*65536 values each.
func denseBitmaps(n int) (a, b *Bitmap) {
	r := rand.New(rand.NewSource(1))
	mk := func() *Bitmap {
		bm := New()
		for k := 0; k < n; k++ {
			bc := &bitmapContainer{words: make([]uint64, bitmapWords)}
			for i := range bc.words {
				bc.words[i] = r.Uint64()
			}
			bc.card = popcountWords(bc.words)
			bm.entries = append(bm.entries, bitmapEntry{key: uint16(k), c: bc})
		}
		return bm
	}
	return mk(), mk()
}

// runBitmaps builds two bitmaps covering the entire 32-bit key space with
// run containers: 65536 containers x 65536 values = 2^32 values each.
func fullRunBitmaps() (a, b *Bitmap) {
	mk := func(offset uint16) *Bitmap {
		bm := &Bitmap{entries: make([]bitmapEntry, 0, 1<<16)}
		for k := 0; k < 1<<16; k++ {
			start := offset
			length := uint16(65535) - offset
			rc := &runContainer{iv: []runInterval{{start: start, length: length}}}
			bm.entries = append(bm.entries, bitmapEntry{key: uint16(k), c: rc})
		}
		return bm
	}
	return mk(0), mk(128)
}

func BenchmarkAndDense(b *testing.B) {
	x, y := denseBitmaps(4096) // 2 x 32 MiB, ~2.7e8 values each
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.And(y)
	}
}

func BenchmarkOrDense(b *testing.B) {
	x, y := denseBitmaps(4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Or(y)
	}
}

func BenchmarkXorDense(b *testing.B) {
	x, y := denseBitmaps(4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Xor(y)
	}
}

// BenchmarkAndFullKeySpace intersects two bitmaps that each hold all 2^32
// possible values (run-encoded): ~8.6e9 logical bit positions per op.
func BenchmarkAndFullKeySpace(b *testing.B) {
	x, y := fullRunBitmaps()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.And(y)
	}
}

func BenchmarkOrFullKeySpace(b *testing.B) {
	x, y := fullRunBitmaps()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Or(y)
	}
}

// BenchmarkAndHugeLogical simulates a >1e10 (百亿级) logical workload:
// a batch of full-key-space bitmap pairs intersected in one pass.
func BenchmarkAndHugeLogical(b *testing.B) {
	x, y := fullRunBitmaps()
	pairs := [][2]*Bitmap{{x, y}, {x, y}, {x, y}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range pairs {
			_ = p[0].And(p[1])
		}
	}
}

func BenchmarkAndSparse(b *testing.B) {
	r := rand.New(rand.NewSource(2))
	mk := func() *Bitmap {
		bm := New()
		for i := 0; i < 100000; i++ {
			bm.Add(r.Uint32())
		}
		return bm
	}
	x, y := mk(), mk()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.And(y)
	}
}

func BenchmarkSerialize(b *testing.B) {
	x, _ := denseBitmaps(512)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Serialize()
	}
}

func BenchmarkDeserializeZeroCopy(b *testing.B) {
	x, _ := denseBitmaps(512)
	buf := x.Serialize()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Deserialize(buf); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAddSorted(b *testing.B) {
	r := rand.New(rand.NewSource(4))
	ids := make([]uint32, 1000000)
	for i := range ids {
		ids[i] = r.Uint32()
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm := New()
		for _, id := range ids {
			bm.Add(id)
		}
	}
}

func BenchmarkAddRandom(b *testing.B) {
	r := rand.New(rand.NewSource(4))
	ids := make([]uint32, 20000)
	for i := range ids {
		ids[i] = r.Uint32()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm := New()
		for _, id := range ids {
			bm.Add(id)
		}
	}
}

func BenchmarkAddRange(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bm := New()
		bm.AddRange(0, 1<<28)
	}
}

func BenchmarkContains(b *testing.B) {
	x, _ := denseBitmaps(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Contains(uint32(i * 7919))
	}
}

// BenchmarkAndMixed models a realistic workload: two bitmaps of 64
// containers each, mixing array, bitmap and run encodings.
func BenchmarkAndMixed(b *testing.B) {
	r := rand.New(rand.NewSource(6))
	x := buildMixed(r, 64)
	y := buildMixed(r, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.And(y)
	}
}

func BenchmarkOrMixed(b *testing.B) {
	r := rand.New(rand.NewSource(6))
	x := buildMixed(r, 64)
	y := buildMixed(r, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Or(y)
	}
}
