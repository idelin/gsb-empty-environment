package cwi

import (
	"math/bits"
	"math/rand"
	"testing"
)

// naive is the independent reference: plain prefix sums over a []uint64.
type naive struct {
	w []uint64
}

func (n *naive) prefix(i int) uint64 {
	s := uint64(0)
	for k := 0; k < i; k++ {
		s += n.w[k]
	}
	return s
}

func (n *naive) total() uint64 { return n.prefix(len(n.w)) }

func (n *naive) select_(target uint64) int {
	if target >= n.total() {
		return len(n.w)
	}
	acc := uint64(0)
	for i, w := range n.w {
		acc += w
		if acc > target {
			return i
		}
	}
	return len(n.w)
}

// tryAdd applies delta to [lo,hi); returns false (and changes nothing) if
// it would go negative or overflow.
func (n *naive) tryAdd(lo, hi int, delta int64) bool {
	if delta > 0 {
		for i := lo; i < hi; i++ {
			if _, c := bits.Add64(n.w[i], uint64(delta), 0); c != 0 {
				return false
			}
		}
		t := uint64(0)
		for i := lo; i < hi; i++ {
			n.w[i] += uint64(delta)
		}
		for _, w := range n.w {
			var c uint64
			t, c = bits.Add64(t, w, 0)
			if c != 0 {
				return false // unreachable in tests, kept for symmetry
			}
		}
		return true
	}
	neg := uint64(-(delta + 1)) + 1
	for i := lo; i < hi; i++ {
		if n.w[i] < neg {
			return false
		}
	}
	for i := lo; i < hi; i++ {
		n.w[i] -= neg
	}
	return true
}

func (n *naive) clone() *naive {
	w := make([]uint64, len(n.w))
	copy(w, n.w)
	return &naive{w: w}
}

func mustNew(t *testing.T, ws []uint64) *Index {
	t.Helper()
	ix, err := New(ws)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ix
}

func TestSelectZeroPlateausAndBoundary(t *testing.T) {
	// weights: 5,0,0,0,5 -> cumulative 5,5,5,5,10
	ix := mustNew(t, []uint64{5, 0, 0, 0, 5})
	cases := []struct {
		target uint64
		want   int
	}{
		{0, 0}, {4, 0}, // inside first element
		{5, 4}, {9, 4}, // zero plateau 1..3 must be skipped
		{10, 5},               // target == total -> past-the-end
		{11, 5}, {1 << 40, 5}, // beyond total -> past-the-end
	}
	for _, c := range cases {
		got, st, err := ix.Select(0, c.target)
		if err != nil {
			t.Fatalf("Select(%d): %v", c.target, err)
		}
		if got != c.want {
			t.Errorf("Select(%d) = %d, want %d", c.target, got, c.want)
		}
		if c.target < 10 && st.BlocksDecoded != 1 {
			t.Errorf("Select(%d) decoded %d blocks, want 1", c.target, st.BlocksDecoded)
		}
	}
	// All-zero sequence: nothing selectable, everything past-the-end.
	zx := mustNew(t, []uint64{0, 0, 0})
	if got, _, _ := zx.Select(0, 0); got != 3 {
		t.Errorf("all-zero Select(0) = %d, want 3", got)
	}
}

func TestPrefixSumBasic(t *testing.T) {
	ws := []uint64{3, 1, 4, 1, 5, 9, 2, 6}
	ix := mustNew(t, ws)
	n := &naive{w: ws}
	for i := 0; i <= len(ws); i++ {
		got, _, err := ix.PrefixSum(0, i)
		if err != nil {
			t.Fatalf("PrefixSum(%d): %v", i, err)
		}
		if want := n.prefix(i); got != want {
			t.Errorf("PrefixSum(%d) = %d, want %d", i, got, want)
		}
	}
}

func TestRandomizedVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 300 // not a multiple of BlockSize on purpose
	ws := make([]uint64, n)
	for i := range ws {
		switch rng.Intn(4) {
		case 0:
			ws[i] = 0 // plant zero plateaus
		case 1:
			ws[i] = uint64(rng.Intn(3))
		default:
			ws[i] = uint64(rng.Intn(1000))
		}
	}
	ix := mustNew(t, ws)
	versions := []*naive{{w: append([]uint64(nil), ws...)}}

	for op := 0; op < 400; op++ {
		lo := rng.Intn(n)
		hi := lo + rng.Intn(n-lo) + 1
		var delta int64
		switch rng.Intn(3) {
		case 0:
			delta = int64(rng.Intn(50) + 1)
		case 1:
			delta = -int64(rng.Intn(20) + 1)
		default:
			delta = int64(rng.Intn(2001)) - 1000
		}
		nxt := versions[len(versions)-1].clone()
		okNaive := nxt.tryAdd(lo, hi, delta)
		rep, err := ix.RangeAdd(lo, hi, delta)
		if okNaive {
			if err != nil {
				t.Fatalf("op %d: RangeAdd(%d,%d,%d) unexpectedly failed: %v", op, lo, hi, delta, err)
			}
			if rep.Version != len(versions) {
				t.Fatalf("op %d: committed version %d, want %d", op, rep.Version, len(versions))
			}
			if rep.BlocksReencoded != rep.LastBlock-rep.FirstBlock+1 || rep.FirstBlock != lo/BlockSize || rep.LastBlock != (hi-1)/BlockSize {
				t.Fatalf("op %d: bad re-encode range %+v", op, rep)
			}
			versions = append(versions, nxt)
		} else {
			if err == nil {
				t.Fatalf("op %d: RangeAdd(%d,%d,%d) should have been rejected", op, lo, hi, delta)
			}
			if ix.Versions() != len(versions) {
				t.Fatalf("op %d: rejected update changed version count", op)
			}
		}

		// Spot-check random versions against the naive snapshots.
		for q := 0; q < 4; q++ {
			v := rng.Intn(len(versions))
			i := rng.Intn(n + 1)
			got, _, err := ix.PrefixSum(v, i)
			if err != nil {
				t.Fatalf("op %d: PrefixSum(v=%d,%d): %v", op, v, i, err)
			}
			if want := versions[v].prefix(i); got != want {
				t.Fatalf("op %d: PrefixSum(v=%d,%d) = %d, want %d", op, v, i, got, want)
			}
			tot := versions[v].total()
			target := uint64(0)
			if tot > 0 {
				target = uint64(rng.Int63n(int64(tot) + 2)) // sometimes == total or beyond
			}
			sel, _, err := ix.Select(v, target)
			if err != nil {
				t.Fatalf("op %d: Select(v=%d,%d): %v", op, v, target, err)
			}
			if want := versions[v].select_(target); sel != want {
				t.Fatalf("op %d: Select(v=%d,%d) = %d, want %d", op, v, target, sel, want)
			}
			if sel < n && versions[v].w[sel] == 0 {
				t.Fatalf("op %d: Select(v=%d,%d) picked zero-weight index %d", op, v, target, sel)
			}
		}
	}
}

func TestNegativeUpdateRejectedAtomically(t *testing.T) {
	ws := []uint64{10, 0, 3, 7, 0, 0, 2}
	ix := mustNew(t, ws)
	before := ix.Versions()
	// Batch touches index 2 (weight 3) with delta -5: must fail as a whole,
	// even though index 0 (weight 10) could absorb it.
	if _, err := ix.RangeAdd(0, 5, -5); err != ErrNegative {
		t.Fatalf("RangeAdd err = %v, want ErrNegative", err)
	}
	if ix.Versions() != before {
		t.Fatal("rejected batch created a version")
	}
	for i, want := range []uint64{10, 0, 3, 7, 0} {
		got, _, _ := ix.PrefixSum(0, i+1)
		prev, _, _ := ix.PrefixSum(0, i)
		if got-prev != want {
			t.Fatalf("weight[%d] changed after rejected batch: got %d want %d", i, got-prev, want)
		}
	}
}

func TestOverflowDetection(t *testing.T) {
	// Construction-time overflow of the total.
	if _, err := New([]uint64{^uint64(0), 1}); err != ErrOverflow {
		t.Fatalf("New overflow err = %v, want ErrOverflow", err)
	}
	// Update-time overflow on a single element.
	ix := mustNew(t, []uint64{^uint64(0) - 4, 1})
	if _, err := ix.RangeAdd(0, 1, 10); err != ErrOverflow {
		t.Fatalf("RangeAdd overflow err = %v, want ErrOverflow", err)
	}
	if ix.Versions() != 1 {
		t.Fatal("overflowing update committed")
	}
	// A valid update right at the boundary still works.
	if _, err := ix.RangeAdd(0, 1, 3); err != nil {
		t.Fatalf("boundary update failed: %v", err)
	}
	tot, _ := ix.Total(1)
	if tot != ^uint64(0) {
		t.Fatalf("total = %d, want MaxUint64", tot)
	}
	// One more increment must now overflow the global total.
	if _, err := ix.RangeAdd(1, 2, 1); err != ErrOverflow {
		t.Fatalf("global overflow err = %v, want ErrOverflow", err)
	}
}

func TestOldVersionsRemainQueryable(t *testing.T) {
	ix := mustNew(t, []uint64{1, 2, 3, 4, 5})
	if _, err := ix.RangeAdd(0, 5, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.RangeAdd(2, 3, -50); err != nil {
		t.Fatal(err)
	}
	// v0: 1,2,3,4,5  v1: 101,102,103,104,105  v2: 101,102,53,104,105
	wantTotals := []uint64{15, 515, 465}
	for v, want := range wantTotals {
		got, err := ix.Total(v)
		if err != nil || got != want {
			t.Fatalf("Total(v%d) = %d,%v want %d", v, got, err, want)
		}
	}
	if got, _, _ := ix.Select(0, 14); got != 4 {
		t.Fatalf("v0 Select(14) = %d, want 4", got)
	}
	if got, _, _ := ix.Select(0, 15); got != 5 {
		t.Fatalf("v0 Select(15==total) = %d, want 5", got)
	}
	if got, _, _ := ix.Select(0, 0); got != 0 {
		t.Fatalf("v0 Select(0) = %d, want 0", got)
	}
	if got, _, _ := ix.Select(1, 202); got != 1 { // 101+102=203 > 202
		t.Fatalf("v1 Select(202) = %d, want 1", got)
	}
	if got, _, _ := ix.Select(2, 250); got != 2 { // 101+102=203, +53=256 > 250
		t.Fatalf("v2 Select(250) = %d, want 2", got)
	}
}

func TestEncodingLayoutAndBytes(t *testing.T) {
	// 64 small weights (1 byte each) + 1-byte block total header.
	ws := make([]uint64, BlockSize)
	for i := range ws {
		ws[i] = 1
	}
	ix := mustNew(t, ws)
	got, err := ix.StoredBytes(0)
	if err != nil {
		t.Fatal(err)
	}
	if want := BlockSize + 1; got != want {
		t.Fatalf("StoredBytes = %d, want %d (layout: %s)", got, want, Layout())
	}
	// Larger weights widen the varints: 64 weights of 300 -> 2 bytes each.
	ix2 := mustNew(t, func() []uint64 {
		w := make([]uint64, BlockSize)
		for i := range w {
			w[i] = 200
		}
		return w
	}())
	got2, _ := ix2.StoredBytes(0)
	if want := 2*BlockSize + 2; got2 != want {
		t.Fatalf("StoredBytes = %d, want %d", got2, want)
	}
	// Structural sharing: a one-block update adds only that block's bytes.
	// All-zero blocks encode as 1B total + 64 one-byte zero deltas = 65B.
	big := make([]uint64, 3*BlockSize)
	ix3 := mustNew(t, big)
	v0, _ := ix3.StoredBytes(0)
	if v0 != 3*(BlockSize+1) {
		t.Fatalf("zero blocks StoredBytes = %d, want %d", v0, 3*(BlockSize+1))
	}
	rep, err := ix3.RangeAdd(BlockSize, BlockSize+1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FirstBlock != 1 || rep.LastBlock != 1 || rep.BlocksReencoded != 1 {
		t.Fatalf("re-encode range = %+v, want exactly block 1", rep)
	}
	if rep.BytesWritten != BlockSize+1 {
		t.Fatalf("BytesWritten = %d, want %d", rep.BytesWritten, BlockSize+1)
	}
	v1, _ := ix3.StoredBytes(1)
	if v1 != v0 {
		t.Fatalf("v1 StoredBytes = %d, want %d (single 5 still fits 1-byte varints)", v1, v0)
	}
	if ix3.TotalStoredBytes() != v0+v1 {
		t.Fatal("TotalStoredBytes mismatch")
	}
	if ix3.NaiveBytes() != 8*3*BlockSize*2 {
		t.Fatal("NaiveBytes mismatch")
	}
}

func TestAccessStatsAndRanges(t *testing.T) {
	ws := make([]uint64, 4*BlockSize)
	for i := range ws {
		ws[i] = uint64(i%7) + 1
	}
	ix := mustNew(t, ws)
	// PrefixSum landing exactly on a block boundary decodes nothing.
	if _, st, _ := ix.PrefixSum(0, 2*BlockSize); st.BlocksDecoded != 0 {
		t.Fatalf("boundary PrefixSum decoded %d blocks, want 0", st.BlocksDecoded)
	}
	if _, st, _ := ix.PrefixSum(0, 2*BlockSize+1); st.BlocksDecoded != 1 {
		t.Fatalf("PrefixSum decoded %d blocks, want 1", st.BlocksDecoded)
	}
	// Select decodes exactly the block containing the target.
	_, st, err := ix.Select(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if st.BlocksDecoded != 1 {
		t.Fatalf("Select decoded %d blocks, want 1", st.BlocksDecoded)
	}
	// A range update spanning two blocks re-encodes exactly those two.
	rep, err := ix.RangeAdd(BlockSize-1, 2*BlockSize+1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FirstBlock != 0 || rep.LastBlock != 2 || rep.BlocksReencoded != 3 {
		t.Fatalf("re-encode range = %+v, want blocks 0..2", rep)
	}
}
