package cidx

import (
	"math"
	"math/rand"
	"testing"
)

// naive is the independent reference: a plain prefix-sum array.
type naive struct {
	w []uint64
}

func (nv *naive) total() uint64 {
	var s uint64
	for _, v := range nv.w {
		s += v
	}
	return s
}

func (nv *naive) prefix(i int) uint64 {
	var s uint64
	for _, v := range nv.w[:i] {
		s += v
	}
	return s
}

func (nv *naive) select_(t uint64) int {
	if t >= nv.total() {
		return len(nv.w)
	}
	var run uint64
	for i, v := range nv.w {
		if run+v > t {
			return i
		}
		run += v
	}
	return len(nv.w)
}

func checkAgainstNaive(t *testing.T, ix *Index, nv *naive) {
	t.Helper()
	if ix.Total() != nv.total() {
		t.Fatalf("total: got %d want %d", ix.Total(), nv.total())
	}
	n := len(nv.w)
	probes := []int{0, 1, n / 3, n / 2, n - 1, n}
	for _, p := range probes {
		if p < 0 || p > n {
			continue
		}
		if got, want := ix.Prefix(p), nv.prefix(p); got != want {
			t.Fatalf("prefix(%d): got %d want %d", p, got, want)
		}
	}
	total := nv.total()
	targets := []uint64{0, 1, total / 2, total - 1, total, total + 7}
	for i := 0; i < 8; i++ {
		targets = append(targets, uint64(rand.Int63n(int64(max(total, 1))+1)))
	}
	for _, tg := range targets {
		if got, want := ix.Select(tg), nv.select_(tg); got != want {
			t.Fatalf("select(%d): got %d want %d", tg, got, want)
		}
	}
}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// TestRandomAgainstNaive cross-checks random Set/BatchAdd sequences against
// an independent plain prefix array, and verifies the compressed index only
// touches O(1) blocks per query and re-encodes only affected blocks.
func TestRandomAgainstNaive(t *testing.T) {
	rand.Seed(20261001)
	const n = 1000
	ix := New(n)
	nv := &naive{w: make([]uint64, n)}
	for step := 0; step < 600; step++ {
		switch rand.Intn(2) {
		case 0: // point set
			i := rand.Intn(n)
			v := uint64(rand.Int63n(1 << 20))
			nix, err := ix.Set(i, v)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			ix = nix
			nv.w[i] = v
		case 1: // batch range increments
			var ops []RangeOp
			for k := 0; k < 1+rand.Intn(3); k++ {
				lo := rand.Intn(n)
				hi := lo + rand.Intn(n-lo) + 1
				d := int64(rand.Intn(2001)) - 1000 // may be negative
				ops = append(ops, RangeOp{Lo: lo, Hi: hi, Delta: d})
			}
			nix, rep, err := ix.BatchAdd(ops)
			if err != nil {
				continue // rejected: old version must be untouched
			}
			// apply to naive (validated ok by the index)
			for _, op := range ops {
				for i := op.Lo; i < op.Hi; i++ {
					nv.w[i] = uint64(int64(nv.w[i]) + op.Delta)
				}
			}
			// re-encode range must cover exactly the affected blocks
			loB, hiB := n, 0
			for _, op := range ops {
				if b := op.Lo / BlockSize; b < loB {
					loB = b
				}
				if b := (op.Hi - 1) / BlockSize; b+1 > hiB {
					hiB = b + 1
				}
			}
			if rep.ReencodeLo > loB || rep.ReencodeHi < hiB {
				t.Fatalf("re-encode range [%d,%d) misses affected [%d,%d)",
					rep.ReencodeLo, rep.ReencodeHi, loB, hiB)
			}
			if rep.BlocksTouched > hiB-loB {
				t.Fatalf("re-encoded %d blocks, more than affected %d", rep.BlocksTouched, hiB-loB)
			}
			ix = nix
		}
		if step%50 == 0 {
			checkAgainstNaive(t, ix, nv)
		}
	}
	checkAgainstNaive(t, ix, nv)

	// Queries must not expand the whole sequence: O(1) blocks each.
	if _, st := ix.PrefixStats(n / 2); st.BlocksAccessed > 1 {
		t.Fatalf("prefix touched %d blocks", st.BlocksAccessed)
	}
	if _, st := ix.SelectStats(ix.Total() / 2); st.BlocksAccessed > 1 {
		t.Fatalf("select touched %d blocks", st.BlocksAccessed)
	}
}

// TestZeroPlateau: zero-weight runs must never be selected.
func TestZeroPlateau(t *testing.T) {
	w := []uint64{0, 0, 0, 5, 0, 0, 3, 0, 0}
	ix := New(len(w))
	for i, v := range w {
		var err error
		if ix, err = ix.Set(i, v); err != nil {
			t.Fatal(err)
		}
	}
	for target := uint64(0); target < 5; target++ {
		if got := ix.Select(target); got != 3 {
			t.Fatalf("select(%d)=%d, want 3 (zero plateau [0,3) skipped)", target, got)
		}
	}
	for target := uint64(5); target < 8; target++ {
		if got := ix.Select(target); got != 6 {
			t.Fatalf("select(%d)=%d, want 6", target, got)
		}
	}
	if got := ix.Select(8); got != len(w) {
		t.Fatalf("select(total)=%d, want past-the-end %d", got, len(w))
	}
}

// TestPastEnd: target equal to (or beyond) the total returns n by convention.
func TestPastEnd(t *testing.T) {
	ix := New(10)
	var err error
	if ix, err = ix.Set(4, 9); err != nil {
		t.Fatal(err)
	}
	if got := ix.Select(9); got != 10 {
		t.Fatalf("select(total)= %d, want 10", got)
	}
	if got := ix.Select(1000); got != 10 {
		t.Fatalf("select(>total)=%d, want 10", got)
	}
	// empty/zero index: everything is past-the-end
	z := New(5)
	if got := z.Select(0); got != 5 {
		t.Fatalf("select on zero total = %d, want 5", got)
	}
}

// TestNegativeRejected: a negative update that would drive a weight below
// zero is rejected and the old version stays intact.
func TestNegativeRejected(t *testing.T) {
	ix := New(8)
	var err error
	if ix, err = ix.Set(2, 4); err != nil {
		t.Fatal(err)
	}
	before := ix
	nix, _, err := ix.BatchAdd([]RangeOp{{Lo: 0, Hi: 8, Delta: -5}})
	if err != ErrNegativeWeight {
		t.Fatalf("err=%v, want ErrNegativeWeight", err)
	}
	if nix != before {
		t.Fatal("rejected batch must return the unchanged version")
	}
	if ix.Total() != 4 || ix.Select(0) != 2 || ix.Select(3) != 2 || ix.Select(4) != 8 {
		t.Fatal("old version corrupted after rejected batch")
	}
}

// TestAtomicBatch: one bad op rejects the whole batch; nothing is applied.
func TestAtomicBatch(t *testing.T) {
	ix := New(100)
	var err error
	if ix, err = ix.Set(10, 3); err != nil {
		t.Fatal(err)
	}
	ops := []RangeOp{
		{Lo: 0, Hi: 50, Delta: 100},  // valid
		{Lo: 5, Hi: 20, Delta: -200}, // 3+100-200 < 0: must reject everything
	}
	nix, _, err := ix.BatchAdd(ops)
	if err != ErrNegativeWeight {
		t.Fatalf("err=%v, want ErrNegativeWeight", err)
	}
	if nix.Total() != 3 || nix.At(0) != 0 {
		t.Fatal("partial batch leaked into committed version")
	}
}

// TestOverflow: element-level and accumulation-level overflow are detected.
func TestOverflow(t *testing.T) {
	ix := New(4)
	var err error
	if ix, err = ix.Set(0, math.MaxUint64); err != nil {
		t.Fatal(err)
	}
	if _, _, err = ix.BatchAdd([]RangeOp{{Lo: 0, Hi: 1, Delta: 1}}); err != ErrOverflow {
		t.Fatalf("element overflow: err=%v", err)
	}
	// block-total overflow: two huge weights in one block
	if _, err = ix.Set(1, 1); err != ErrOverflow {
		t.Fatalf("block total overflow: err=%v", err)
	}
	// cross-block total overflow: huge weights in different blocks
	ix2 := New(2 * BlockSize)
	if ix2, err = ix2.Set(0, math.MaxUint64); err != nil {
		t.Fatal(err)
	}
	if _, err = ix2.Set(BlockSize, 1); err != ErrOverflow {
		t.Fatalf("fenwick total overflow: err=%v", err)
	}
	// old version still fine
	if ix.Total() != math.MaxUint64 || ix.Select(math.MaxUint64-1) != 0 {
		t.Fatal("old version corrupted after overflow rejections")
	}
}

// TestPersistence: old versions keep supporting select-by-cumulative-weight.
func TestPersistence(t *testing.T) {
	v0 := New(200)
	v1, _, err := v0.BatchAdd([]RangeOp{{Lo: 10, Hi: 20, Delta: 5}})
	if err != nil {
		t.Fatal(err)
	}
	v2, _, err := v1.BatchAdd([]RangeOp{{Lo: 100, Hi: 150, Delta: 7}})
	if err != nil {
		t.Fatal(err)
	}
	// v0: total 0 -> everything past-the-end
	if v0.Total() != 0 || v0.Select(0) != 200 {
		t.Fatal("v0 broken")
	}
	// v1: weight 5 on [10,20); select(0..24) lands in [10,20)
	if v1.Total() != 50 {
		t.Fatalf("v1 total=%d", v1.Total())
	}
	for target := uint64(0); target < 50; target++ {
		got := v1.Select(target)
		if got < 10 || got >= 20 {
			t.Fatalf("v1.select(%d)=%d, want in [10,20)", target, got)
		}
	}
	if v1.Select(50) != 200 {
		t.Fatal("v1 past-the-end broken")
	}
	// v2 sees both ranges; v1 must not see the second one
	if v2.Total() != 50+350 {
		t.Fatalf("v2 total=%d", v2.Total())
	}
	if got := v2.Select(50 + 349); got < 100 || got >= 150 {
		t.Fatalf("v2.select=%d, want in [100,150)", got)
	}
	if v1.Total() != 50 {
		t.Fatal("v1 mutated by v2 commit")
	}
}

// TestEncodingLayout verifies the real byte-level packed layout and that
// Bytes() reflects the actual encoding, not a map wrapper.
func TestEncodingLayout(t *testing.T) {
	const n = 2 * BlockSize
	ix := New(n) // all zeros: width 0, no data bytes
	wantPerBlock := blockHeaderBytes + 0
	if got := ix.BlockBytes(0); got != wantPerBlock {
		t.Fatalf("zero block = %d bytes, want %d", got, wantPerBlock)
	}
	// values in {0,1}: width 1 -> 64 bits = 8 data bytes per block
	var err error
	for i := 0; i < n; i += 2 {
		if ix, err = ix.Set(i, 1); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := ix.BlockBytes(0), blockHeaderBytes+8; got != want {
		t.Fatalf("binary block = %d bytes, want %d", got, want)
	}
	// values up to 252 with base 0: width 8 -> 64 data bytes
	ix2 := New(BlockSize)
	for i := 0; i < BlockSize; i++ {
		if ix2, err = ix2.Set(i, uint64(i)*4); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := ix2.BlockBytes(0), blockHeaderBytes+BlockSize; got != want {
		t.Fatalf("byte-wide block = %d bytes, want %d", got, want)
	}
	// round-trip through the packed encoding
	for i := 0; i < BlockSize; i++ {
		if got := ix2.At(i); got != uint64(i)*4 {
			t.Fatalf("at(%d)=%d", i, got)
		}
	}
	// total footprint: blocks + fenwick + header, far below n*8 for small widths
	if got := ix.Bytes(); got != 16+2*(blockHeaderBytes+8)+(2+1)*8 {
		t.Fatalf("Bytes()=%d", got)
	}
	naive := n * 8
	if ix.Bytes() >= naive {
		t.Fatalf("compressed %d bytes not smaller than naive %d", ix.Bytes(), naive)
	}
}

// TestAccessStats: select/prefix report bounded block access and log fenwick.
func TestAccessStats(t *testing.T) {
	const n = 1 << 16
	ix := New(n)
	var err error
	if ix, _, err = ix.BatchAdd([]RangeOp{{Lo: 0, Hi: n, Delta: 3}}); err != nil {
		t.Fatal(err)
	}
	_, st := ix.SelectStats(ix.Total() - 1)
	if st.BlocksAccessed != 1 {
		t.Fatalf("select decoded %d blocks, want 1", st.BlocksAccessed)
	}
	nb := (n + BlockSize - 1) / BlockSize
	maxNodes := 0
	for x := nb; x > 0; x >>= 1 {
		maxNodes++
	}
	if st.FenwickNodes > maxNodes+1 {
		t.Fatalf("select visited %d fenwick nodes (> ~log2 %d)", st.FenwickNodes, nb)
	}
	if _, pst := ix.PrefixStats(n / 2); pst.BlocksAccessed > 1 {
		t.Fatalf("prefix decoded %d blocks", pst.BlocksAccessed)
	}
}
