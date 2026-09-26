package cowbitmap

import (
	"errors"
	"sync/atomic"
	"testing"
)

func mustSet(t *testing.T, m *Bitmap, bits ...int) {
	t.Helper()
	for _, b := range bits {
		if err := m.Set(b, true); err != nil {
			t.Fatalf("Set(%d): %v", b, err)
		}
	}
}

func mustGet(t *testing.T, m *Bitmap, bit int) bool {
	t.Helper()
	v, err := m.Get(bit)
	if err != nil {
		t.Fatalf("Get(%d): %v", bit, err)
	}
	return v
}

func refs(b *block) int32 { return atomic.LoadInt32(&b.refs) }

// Old snapshots must not observe flips made after the snapshot.
func TestSnapshotIsolation(t *testing.T) {
	m := New(4096)
	mustSet(t, m, 1, 600, 2000)
	snap := m.Snapshot()

	// Flip bits in the original, including inside the same block as bit 1.
	mustSet(t, m, 2, 601)
	if err := m.Set(1, false); err != nil {
		t.Fatal(err)
	}

	for _, bit := range []int{1, 600, 2000} {
		if !mustGet(t, snap, bit) {
			t.Errorf("snapshot lost bit %d after new flips", bit)
		}
	}
	for _, bit := range []int{2, 601} {
		if mustGet(t, snap, bit) {
			t.Errorf("snapshot observed new bit %d", bit)
		}
	}
	if !mustGet(t, m, 2) || mustGet(t, m, 1) {
		t.Error("original does not reflect its own flips")
	}
}

// Snapshots share blocks (refs=2); a write to a shared block copies exactly
// that block and drops the old refs back to 1.
func TestBlockSharingAndCOW(t *testing.T) {
	m := New(2048) // 4 blocks
	mustSet(t, m, 10, 1000)
	snap := m.Snapshot()

	if got := snap.SharedBlocks(); got != 2 {
		t.Fatalf("shared blocks after snapshot = %d, want 2", got)
	}
	shared := m.blocks[0]
	if refs(shared) != 2 {
		t.Fatalf("refs before COW = %d, want 2", refs(shared))
	}

	mustSet(t, m, 11) // touches block 0 only
	if m.blocks[0] == shared {
		t.Fatal("block 0 was not copied on write")
	}
	if refs(shared) != 1 {
		t.Fatalf("refs after COW = %d, want 1", refs(shared))
	}
	if snap.blocks[0] != shared {
		t.Fatal("snapshot must keep the original block")
	}
	if got := snap.SharedBlocks(); got != 1 {
		t.Fatalf("shared blocks after COW = %d, want 1 (block 1 still shared)", got)
	}
}

// Double release must return an error and must not drive refcounts negative.
func TestDoubleRelease(t *testing.T) {
	m := New(1024)
	mustSet(t, m, 5)
	snap := m.Snapshot()
	blk := m.blocks[0]

	if err := m.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := m.Release(); !errors.Is(err, ErrDoubleRelease) {
		t.Fatalf("second Release = %v, want ErrDoubleRelease", err)
	}
	if r := refs(blk); r != 1 {
		t.Fatalf("refs after double release = %d, want 1 (snapshot still holds it)", r)
	}

	if err := snap.Release(); err != nil {
		t.Fatal(err)
	}
	if err := snap.Release(); !errors.Is(err, ErrDoubleRelease) {
		t.Fatalf("second snapshot Release = %v, want ErrDoubleRelease", err)
	}
	if r := refs(blk); r != 0 {
		t.Fatalf("refs after full release = %d, want 0 (never negative)", r)
	}
}

// Length mismatch must be rejected before any retain/allocation happens.
func TestLengthMismatchRejectedUpfront(t *testing.T) {
	a := New(1024)
	b := New(2048)
	mustSet(t, a, 3)
	mustSet(t, b, 3)
	before := refs(a.blocks[0])

	if _, err := And(a, b); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("And = %v, want ErrLengthMismatch", err)
	}
	if _, err := Or(a, b); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("Or = %v, want ErrLengthMismatch", err)
	}
	if got := refs(a.blocks[0]); got != before {
		t.Fatalf("operand refs changed on rejected op: %d -> %d", before, got)
	}
}

// Lazy results compute blocks only on first access and match eager results.
func TestLazyMaterialization(t *testing.T) {
	a := New(8192) // 16 blocks
	b := New(8192)
	mustSet(t, a, 1, 700, 1500, 5000)
	mustSet(t, b, 700, 1500, 3000)

	and, err := And(a, b)
	if err != nil {
		t.Fatal(err)
	}
	defer and.Release()
	if got := and.MaterializedBlocks(); got != 0 {
		t.Fatalf("materialized at creation = %d, want 0 (lazy)", got)
	}

	// One Get materializes exactly the containing block.
	if !mustGetLazy(t, and, 700) {
		t.Fatal("AND lost bit 700")
	}
	if got := and.MaterializedBlocks(); got != 1 {
		t.Fatalf("materialized after 1 Get = %d, want 1", got)
	}
	if mustGetLazy(t, and, 1) || mustGetLazy(t, and, 3000) {
		t.Fatal("AND kept a bit set in only one operand")
	}

	or, err := Or(a, b)
	if err != nil {
		t.Fatal(err)
	}
	defer or.Release()
	for _, bit := range []int{1, 700, 1500, 3000, 5000} {
		if !mustGetLazy(t, or, bit) {
			t.Errorf("OR lost bit %d", bit)
		}
	}

	full, err := and.Materialize()
	if err != nil {
		t.Fatal(err)
	}
	defer full.Release()
	if full.Count() != 2 {
		t.Fatalf("materialized AND count = %d, want 2", full.Count())
	}
}

func mustGetLazy(t *testing.T, l *LazyBitmap, bit int) bool {
	t.Helper()
	v, err := l.Get(bit)
	if err != nil {
		t.Fatalf("lazy Get(%d): %v", bit, err)
	}
	return v
}

// Releasing a lazy result must drop operand refs; double release is an error
// and never makes refs negative.
func TestLazyReleaseReclaimsRefs(t *testing.T) {
	a := New(1024)
	b := New(1024)
	mustSet(t, a, 1)
	mustSet(t, b, 1, 2)

	l, err := And(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if refs(a.blocks[0]) != 2 || refs(b.blocks[0]) != 2 {
		t.Fatal("lazy op must retain operand blocks")
	}
	mustGetLazy(t, l, 1) // materialize a cached block too

	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if refs(a.blocks[0]) != 1 || refs(b.blocks[0]) != 1 {
		t.Fatal("operand refs not reclaimed after lazy Release")
	}
	if err := l.Release(); !errors.Is(err, ErrDoubleRelease) {
		t.Fatalf("second lazy Release = %v, want ErrDoubleRelease", err)
	}
	if refs(a.blocks[0]) != 1 {
		t.Fatal("double lazy release drove refs below expectation")
	}
}

// Operands may be released while a lazy result is still alive.
func TestLazyOutlivesOperands(t *testing.T) {
	a := New(1024)
	b := New(1024)
	mustSet(t, a, 42)
	mustSet(t, b, 42)
	l, err := And(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	if err := b.Release(); err != nil {
		t.Fatal(err)
	}
	if !mustGetLazy(t, l, 42) {
		t.Fatal("lazy result broke after operands were released")
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}

// EncodedLen must equal len(Encode) exactly, and Decode must round-trip
// both the bits and the content-sharing structure.
func TestEncodingLayoutAndRoundTrip(t *testing.T) {
	m := New(10000) // 20 blocks
	mustSet(t, m, 0, 511, 512, 9999)
	snap := m.Snapshot() // shares all 3 non-zero blocks

	enc := m.Encode()
	if got, want := len(enc), m.EncodedLen(); got != want {
		t.Fatalf("len(Encode) = %d, EncodedLen = %d", got, want)
	}
	// Layout: 28-byte header + 3 unique blocks*64B + 20 table entries*4B.
	if want := 28 + 3*BlockBytes + 20*4; len(enc) != want {
		t.Fatalf("encoded size = %d, want %d per documented layout", len(enc), want)
	}
	if string(enc[:4]) != magic {
		t.Fatal("bad magic")
	}

	back, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	for _, bit := range []int{0, 511, 512, 9999} {
		if !mustGet(t, back, bit) {
			t.Errorf("round-trip lost bit %d", bit)
		}
	}
	if back.Count() != 4 || back.Len() != 10000 {
		t.Fatal("round-trip changed length or popcount")
	}

	// Snapshot of the decoded bitmap must share blocks, and a COW write must
	// not disturb the decoded original.
	snap2 := back.Snapshot()
	mustSet(t, back, 7)
	if mustGet(t, snap2, 7) {
		t.Fatal("decoded bitmap is not copy-on-write")
	}
	_ = snap
}

// Corrupt / truncated encodings must be rejected.
func TestDecodeRejectsGarbage(t *testing.T) {
	m := New(1024)
	mustSet(t, m, 1)
	good := m.Encode()
	for _, bad := range [][]byte{nil, good[:10], good[:len(good)-1], append(append([]byte{}, good...), 0)} {
		if _, err := Decode(bad); !errors.Is(err, ErrBadEncoding) {
			t.Errorf("Decode(len=%d) = %v, want ErrBadEncoding", len(bad), err)
		}
	}
	badmagic := append([]byte{}, good...)
	badmagic[0] = 'X'
	if _, err := Decode(badmagic); !errors.Is(err, ErrBadEncoding) {
		t.Error("bad magic accepted")
	}
}

// MemBytes must count pointer slots plus distinct non-nil blocks only.
func TestMemBytes(t *testing.T) {
	m := New(4096) // 8 blocks, all zero
	if got, want := m.MemBytes(), 8*8; got != want {
		t.Fatalf("empty MemBytes = %d, want %d (pointer slice only)", got, want)
	}
	mustSet(t, m, 1, 1000) // 2 distinct blocks
	if got, want := m.MemBytes(), 8*8+2*BlockBytes; got != want {
		t.Fatalf("MemBytes = %d, want %d", got, want)
	}
	snap := m.Snapshot()
	if got := snap.MemBytes(); got != 8*8+2*BlockBytes {
		t.Fatalf("snapshot MemBytes = %d, sharing must not double-count", got)
	}
}

// Clearing the last bit of a block reclaims it (back to nil).
func TestZeroBlockReclamation(t *testing.T) {
	m := New(1024)
	mustSet(t, m, 5)
	if m.blocks[0] == nil {
		t.Fatal("block not allocated")
	}
	if err := m.Set(5, false); err != nil {
		t.Fatal(err)
	}
	if m.blocks[0] != nil {
		t.Fatal("all-zero block not reclaimed")
	}
	if got, want := m.MemBytes(), 2*8; got != want {
		t.Fatalf("MemBytes after reclaim = %d, want %d", got, want)
	}
}

func TestOutOfRange(t *testing.T) {
	m := New(100)
	if err := m.Set(100, true); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("Set(100) = %v, want ErrOutOfRange", err)
	}
	if _, err := m.Get(-1); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("Get(-1) = %v, want ErrOutOfRange", err)
	}
}
