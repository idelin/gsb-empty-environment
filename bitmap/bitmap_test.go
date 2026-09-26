package bitmap

import (
	"bytes"
	"errors"
	"testing"
)

func TestSnapshotIsolation(t *testing.T) {
	b := New(1 << 20)
	defer b.Release()
	for _, i := range []int{0, 1, 4095, 4096, 100_000, 1<<20 - 1} {
		b.Set(i, true)
	}
	snap := b.Snapshot()
	defer snap.Release()

	// Flip bits in the original: clear some old ones, set new ones.
	b.Set(0, false)
	b.Set(4095, false)
	b.Set(7, true)
	b.Set(200_000, true)

	for _, i := range []int{0, 1, 4095, 4096, 100_000, 1<<20 - 1} {
		if !snap.Get(i) {
			t.Fatalf("snapshot lost bit %d after original was flipped", i)
		}
	}
	if snap.Get(7) || snap.Get(200_000) {
		t.Fatal("snapshot gained bits that were set after it was taken")
	}
	if snap.Count() != 6 {
		t.Fatalf("snapshot count = %d, want 6", snap.Count())
	}
	if b.Get(0) || b.Get(4095) {
		t.Fatal("original did not apply clears")
	}
	if !b.Get(7) || !b.Get(200_000) {
		t.Fatal("original did not apply sets")
	}
}

func TestDoubleReleaseNeverNegative(t *testing.T) {
	b := New(1024)
	b.Set(3, true)
	b.Release()
	b.Release()
	b.Release()
	if got := b.Refs(); got != 0 {
		t.Fatalf("refs = %d after repeated Release, want 0 (never negative)", got)
	}

	// Same guard through a lazy node and a snapshot chain.
	a := New(4096)
	a.Set(1, true)
	c := New(4096)
	c.Set(1, true)
	and, err := And(a, c)
	if err != nil {
		t.Fatal(err)
	}
	and.Get(1)
	and.Release()
	and.Release()
	if got := and.Refs(); got != 0 {
		t.Fatalf("lazy node refs = %d, want 0", got)
	}
	if got := a.Refs(); got != 1 {
		t.Fatalf("operand refs = %d, want 1 (released exactly once)", got)
	}
	a.Release()
	c.Release()
}

func TestLengthMismatchRejected(t *testing.T) {
	a := New(8192)
	defer a.Release()
	b := New(8192 + 1)
	defer b.Release()

	if _, err := And(a, b); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("And err = %v, want ErrLengthMismatch", err)
	}
	if _, err := Or(a, b); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("Or err = %v, want ErrLengthMismatch", err)
	}
	// Rejection happens before any reference is taken.
	if a.Refs() != 1 || b.Refs() != 1 {
		t.Fatalf("refs changed by rejected op: a=%d b=%d", a.Refs(), b.Refs())
	}
}

func TestLazyMaterialization(t *testing.T) {
	a := New(1 << 20) // 256 blocks
	defer a.Release()
	c := New(1 << 20)
	defer c.Release()
	a.Set(5, true)
	a.Set(900_000, true)
	c.Set(5, true)
	c.Set(900_000, true)

	and, err := And(a, c)
	if err != nil {
		t.Fatal(err)
	}
	defer and.Release()
	if got := and.MaterializedBlocks(); got != 0 {
		t.Fatalf("materialized %d blocks before any read, want 0 (lazy)", got)
	}
	if !and.Get(5) {
		t.Fatal("And lost intersecting bit")
	}
	if got := and.MaterializedBlocks(); got != 1 {
		t.Fatalf("materialized %d blocks after one Get, want exactly 1", got)
	}
	if and.Get(3) {
		t.Fatal("And kept a bit set in only one operand")
	}
	if got := and.MaterializedBlocks(); got != 1 {
		t.Fatalf("materialized %d blocks, want still 1 (same block)", got)
	}
	if !and.Get(900_000) {
		t.Fatal("And lost far intersecting bit")
	}
	if got := and.MaterializedBlocks(); got != 2 {
		t.Fatalf("materialized %d blocks, want 2", got)
	}
}

func TestBlockSharingAndCOW(t *testing.T) {
	base := LiveBlocks()
	b := New(1 << 20)
	defer b.Release()
	b.Set(10, true)      // block 0
	b.Set(5000, true)    // block 1
	b.Set(900_000, true) // block 219
	if got := LiveBlocks(); got != base+3 {
		t.Fatalf("live blocks = %d, want %d", got, base+3)
	}

	snap := b.Snapshot()
	defer snap.Release()
	if got := LiveBlocks(); got != base+3 {
		t.Fatalf("snapshot allocated: live = %d, want %d (fully shared)", got, base+3)
	}

	// One flip in the original copies exactly one 512-byte block.
	b.Set(11, true)
	if got := LiveBlocks(); got != base+4 {
		t.Fatalf("after COW live = %d, want %d (3 shared + 1 copy)", got, base+4)
	}
	// The snapshot still reads the old shared block; the original reads its copy.
	if snap.Get(11) || !b.Get(11) || !snap.Get(10) || !b.Get(10) {
		t.Fatal("COW corrupted either snapshot or original")
	}

	st := b.MemStats()
	wantBytes := int64(st.Bitmaps)*headerBytes +
		int64(st.DirSlots)*8 + int64(st.MatSlots) + int64(st.Blocks)*BlockBytes
	if st.TotalByte != wantBytes {
		t.Fatalf("TotalByte = %d, want %d", st.TotalByte, wantBytes)
	}
}

func TestReferenceReclamation(t *testing.T) {
	base := LiveBlocks()

	a := New(1 << 20)
	c := New(1 << 20)
	a.Set(1, true)    // block 0
	a.Set(5000, true) // block 1
	c.Set(1, true)    // block 0
	c.Set(9000, true) // block 2
	if got := LiveBlocks(); got != base+4 {
		t.Fatalf("live blocks = %d, want %d", got, base+4)
	}

	snap := a.Snapshot()
	if got := LiveBlocks(); got != base+4 {
		t.Fatalf("snapshot allocated blocks: live = %d, want %d", got, base+4)
	}

	or, err := Or(a, c)
	if err != nil {
		t.Fatal(err)
	}
	or.Get(1) // materialize one block of the lazy node
	mid := LiveBlocks()
	if mid <= base+4 {
		t.Fatalf("lazy materialization did not allocate: live = %d", mid)
	}

	or.Release()
	snap.Release()
	a.Release()
	c.Release()
	if got := LiveBlocks(); got != base {
		t.Fatalf("live blocks = %d after releasing everything, want baseline %d", got, base)
	}
}

func TestAndOrCorrectness(t *testing.T) {
	a := New(100_000)
	defer a.Release()
	c := New(100_000)
	defer c.Release()
	bitsA := []int{0, 63, 64, 4096, 50_000, 99_999}
	bitsC := []int{64, 4096, 70_000}
	for _, i := range bitsA {
		a.Set(i, true)
	}
	for _, i := range bitsC {
		c.Set(i, true)
	}

	and, err := And(a, c)
	if err != nil {
		t.Fatal(err)
	}
	defer and.Release()
	or, err := Or(a, c)
	if err != nil {
		t.Fatal(err)
	}
	defer or.Release()

	if got := and.Count(); got != 2 {
		t.Fatalf("And count = %d, want 2", got)
	}
	if got := or.Count(); got != 7 {
		t.Fatalf("Or count = %d, want 7", got)
	}
	for i := 0; i < 100_000; i++ {
		wantA := i == 64 || i == 4096
		if and.Get(i) != wantA {
			t.Fatalf("And bit %d = %v, want %v", i, and.Get(i), wantA)
		}
		inA, inC := false, false
		for _, j := range bitsA {
			inA = inA || i == j
		}
		for _, j := range bitsC {
			inC = inC || i == j
		}
		if or.Get(i) != (inA || inC) {
			t.Fatalf("Or bit %d wrong", i)
		}
	}
}

func TestEncodeLayoutAndRoundTrip(t *testing.T) {
	b := New(1 << 20) // 256 directory slots
	defer b.Release()
	b.Set(0, true)
	b.Set(4096, true)
	b.Set(1<<20-1, true) // 3 non-empty blocks

	want := int64(headerSize + 256*dirEntrySize + 3*BlockBytes) // 20+2048+1536
	if got := b.EncodedSize(); got != want {
		t.Fatalf("EncodedSize = %d, want %d", got, want)
	}

	var buf bytes.Buffer
	if err := b.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if int64(buf.Len()) != want {
		t.Fatalf("encoded %d bytes, want %d", buf.Len(), want)
	}
	if string(buf.Bytes()[:4]) != "BMC1" {
		t.Fatalf("bad magic %q", buf.Bytes()[:4])
	}

	back, err := Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer back.Release()
	if back.Len() != b.Len() || back.Count() != 3 {
		t.Fatalf("round trip: len=%d count=%d", back.Len(), back.Count())
	}
	for _, i := range []int{0, 4096, 1<<20 - 1} {
		if !back.Get(i) {
			t.Fatalf("round trip lost bit %d", i)
		}
	}
}
