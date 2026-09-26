package bitvec

import (
	"math/rand"
	"testing"
)

// naive is the reference bit array every query is checked against.
type naive []bool

func (n naive) rank(i int) int {
	c := 0
	for j := 0; j < i && j < len(n); j++ {
		if n[j] {
			c++
		}
	}
	return c
}

func (n naive) select1(k int) (int, bool) {
	c := 0
	for j := 0; j < len(n); j++ {
		if n[j] {
			if c == k {
				return j, true
			}
			c++
		}
	}
	return 0, false
}

func (n naive) ones() int { return n.rank(len(n)) }

func checkAll(t *testing.T, bits naive, m *Mapped) {
	t.Helper()
	if got := m.TotalOnes(); got != uint64(bits.ones()) {
		t.Fatalf("len=%d TotalOnes=%d, want %d", len(bits), got, bits.ones())
	}
	for i := 0; i <= len(bits); i++ {
		if got, want := m.Rank1(uint64(i), nil), uint64(bits.rank(i)); got != want {
			t.Fatalf("len=%d Rank1(%d)=%d, want %d", len(bits), i, got, want)
		}
	}
	// Rank past the end clamps to the total (unified boundary).
	if got := m.Rank1(uint64(len(bits)) + 1000, nil); got != uint64(bits.ones()) {
		t.Fatalf("len=%d Rank1(end+1000)=%d, want %d", len(bits), got, bits.ones())
	}
	ones := bits.ones()
	for k := 0; k < ones; k++ {
		pos, ok := m.Select1(uint64(k), nil)
		wp, wok := bits.select1(k)
		if !ok || !wok || pos != uint64(wp) {
			t.Fatalf("len=%d Select1(%d)=(%d,%v), want (%d,%v)", len(bits), k, pos, ok, wp, wok)
		}
	}
	// select of a non-existent rank must report absence.
	if _, ok := m.Select1(uint64(ones), nil); ok {
		t.Fatalf("len=%d Select1(%d) succeeded, want not-found", len(bits), ones)
	}
	if _, ok := m.Select1(uint64(ones) + 999, nil); ok {
		t.Fatalf("len=%d Select1(%d) succeeded, want not-found", len(bits), ones+999)
	}
}

// TestExhaustiveShort enumerates every bit string of length 0..13 and
// checks rank/select against the naive array at every position.
func TestExhaustiveShort(t *testing.T) {
	for n := 0; n <= 13; n++ {
		for pat := 0; pat < 1<<n; pat++ {
			bits := make(naive, n)
			for i := 0; i < n; i++ {
				bits[i] = pat>>i&1 == 1
			}
			m, err := Map(Encode(bits))
			if err != nil {
				t.Fatalf("len=%d pat=%x: %v", n, pat, err)
			}
			checkAll(t, bits, m)
		}
	}
}

// TestWordBlockBoundaries checks consistency with the naive array at
// 64-bit word, 512-bit block, and superblock boundaries.
func TestWordBlockBoundaries(t *testing.T) {
	lengths := []int{1, 63, 64, 65, 127, 128, 129, 511, 512, 513, 1023, 1024, 1025,
		16383, 16384, 16385, 16447, 16448}
	rng := rand.New(rand.NewSource(42))
	for _, n := range lengths {
		for _, density := range []float64{0.0, 0.02, 0.5, 1.0} {
			bits := make(naive, n)
			for i := range bits {
				bits[i] = rng.Float64() < density
			}
			m, err := Map(Encode(bits))
			if err != nil {
				t.Fatalf("len=%d density=%v: %v", n, density, err)
			}
			checkAll(t, bits, m)
		}
	}
}

// TestAllZero: select has no result, rank at the end equals the total.
func TestAllZero(t *testing.T) {
	for _, n := range []int{0, 1, 512, 100000} {
		m, err := Map(Encode(make(naive, n)))
		if err != nil {
			t.Fatal(err)
		}
		if m.TotalOnes() != 0 {
			t.Fatalf("len=%d TotalOnes=%d, want 0", n, m.TotalOnes())
		}
		if _, ok := m.Select1(0, nil); ok {
			t.Fatalf("len=%d Select1(0) on all-zero bitmap succeeded", n)
		}
		if got := m.Rank1(uint64(n), nil); got != 0 {
			t.Fatalf("len=%d Rank1(end)=%d, want 0", n, got)
		}
	}
}

// TestRankAtEndEqualsTotal over random bitmaps of varying size.
func TestRankAtEndEqualsTotal(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 50; trial++ {
		n := rng.Intn(40000)
		bits := make(naive, n)
		for i := range bits {
			bits[i] = rng.Intn(4) == 0
		}
		m, err := Map(Encode(bits))
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Rank1(uint64(n), nil); got != m.TotalOnes() {
			t.Fatalf("len=%d Rank1(end)=%d, TotalOnes=%d", n, got, m.TotalOnes())
		}
	}
}

// TestQueryCostBounded verifies that rank/select touch O(1) blocks and
// a small, bounded number of bytes — never the whole bitmap.
func TestQueryCostBounded(t *testing.T) {
	n := 1 << 20
	rng := rand.New(rand.NewSource(1))
	bits := make(naive, n)
	for i := 0; i < 20000; i++ {
		bits[rng.Intn(n)] = true
	}
	buf := Encode(bits)
	m, err := Map(buf)
	if err != nil {
		t.Fatal(err)
	}
	var mx Metrics
	var maxBlocks, maxBytes uint64
	for q := 0; q < 2000; q++ {
		mx.Reset()
		m.Rank1(uint64(rng.Intn(n+1)), &mx)
		if mx.BlocksTouched > maxBlocks {
			maxBlocks = mx.BlocksTouched
		}
		if mx.BytesRead > maxBytes {
			maxBytes = mx.BytesRead
		}
		mx.Reset()
		if _, ok := m.Select1(uint64(rng.Intn(int(m.TotalOnes()))), &mx); !ok {
			t.Fatal("select failed")
		}
		if mx.BlocksTouched > maxBlocks {
			maxBlocks = mx.BlocksTouched
		}
		if mx.BytesRead > maxBytes {
			maxBytes = mx.BytesRead
		}
	}
	if maxBlocks > 4 {
		t.Fatalf("query touched %d blocks, want <= 4", maxBlocks)
	}
	// A query may read one superblock entry, <= 31 directory entries for
	// the rank base, one select sample, <= SelectRate directory entries
	// while scanning to the target block, and one block payload — still
	// far below the whole bitmap.
	if limit := uint64(8 + 31*dirEntrySize + sampleEntrySize + SelectRate*dirEntrySize + BlockBytes); maxBytes > limit {
		t.Fatalf("query read %d bytes, want <= %d (bitmap is %d bytes)", maxBytes, limit, len(buf))
	}
	t.Logf("max per-query cost: %d blocks, %d bytes (serialized bitmap: %d bytes)",
		maxBlocks, maxBytes, len(buf))
}

// TestLayoutOccupancy reports real byte occupancy and confirms sparse
// bitmaps compress well below the raw bit size.
func TestLayoutOccupancy(t *testing.T) {
	n := 1 << 20

	zero, err := Map(Encode(make(naive, n)))
	if err != nil {
		t.Fatal(err)
	}
	if l := zero.Layout(); l.Data != 0 {
		t.Fatalf("all-zero bitmap has %d data bytes, want 0", l.Data)
	}

	rng := rand.New(rand.NewSource(2))
	sparse := make(naive, n)
	for i := 0; i < 3000; i++ {
		sparse[rng.Intn(n)] = true
	}
	sm, err := Map(Encode(sparse))
	if err != nil {
		t.Fatal(err)
	}
	if got, raw := sm.Layout().Total, n/8; got >= raw {
		t.Fatalf("sparse bitmap occupies %d bytes, raw bits need %d", got, raw)
	}

	dense := make(naive, n)
	for i := range dense {
		dense[i] = rng.Intn(2) == 0
	}
	dm, err := Map(Encode(dense))
	if err != nil {
		t.Fatal(err)
	}
	// Dense random data cannot compress below the entropy bound.
	if got := dm.Layout().Data; got < n/8 {
		t.Fatalf("dense bitmap data section %d bytes < raw %d", got, n/8)
	}
	t.Logf("1M bits: zero=%dB sparse(3k ones)=%dB dense=%dB raw=%dB",
		zero.Layout().Total, sm.Layout().Total, dm.Layout().Total, n/8)
}
