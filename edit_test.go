package blockstore

import (
	"bytes"
	"errors"
	"testing"
)

func TestOverwriteCopyOnWrite(t *testing.T) {
	st := NewStore(8, 0)
	s1, err := st.NewSlice([]byte("ABCDEFGHIJKLMNOP")) // 2 blocks
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Release()

	s2, err := s1.Overwrite(3, []byte("xyz"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Release()

	if got := string(s1.Bytes()); got != "ABCDEFGHIJKLMNOP" {
		t.Fatalf("base slice mutated: %q", got)
	}
	if got := string(s2.Bytes()); got != "ABCxyzGHIJKLMNOP" {
		t.Fatalf("overwrite result: %q", got)
	}
	// The untouched second block is shared, not copied.
	ids1, ids2 := s1.BlockIDs(), s2.BlockIDs()
	if ids1[1] != ids2[1] {
		t.Fatal("untouched block not shared")
	}
	if ids1[0] == ids2[0] {
		t.Fatal("edited block not copied")
	}
	if err := st.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestMoveAndOverlap(t *testing.T) {
	st := NewStore(4, 0)
	s1, _ := st.NewSlice([]byte("ABCDEFGH"))
	defer s1.Release()

	// Move "CDEF" (src=2,n=4) to the front: delete -> "ABGH", insert at 0.
	s2, err := s1.Move(0, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Release()
	if got := string(s2.Bytes()); got != "CDEFABGH" {
		t.Fatalf("move-to-front: %q", got)
	}
	// Move to the back: dst in post-removal coordinates.
	s3, err := s1.Move(4, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Release()
	if got := string(s3.Bytes()); got != "ABGHEFCD"[0:0]+"ABGHCDEF" {
		t.Fatalf("move-to-back: %q", got)
	}
	if string(s1.Bytes()) != "ABCDEFGH" {
		t.Fatal("base mutated by moves")
	}
}

func TestCopyWithinReadsPreOperationContent(t *testing.T) {
	st := NewStore(4, 0)
	s1, _ := st.NewSlice([]byte("ABCDEFGH"))
	defer s1.Release()

	// Overlapping forward copy: a naive byte-by-byte loop would smear
	// ("ABABABAB"); pre-operation read must give "ABABCDGH".
	s2, err := s1.CopyWithin(2, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Release()
	if got := string(s2.Bytes()); got != "ABABCDGH" {
		t.Fatalf("overlapping copy: %q", got)
	}
	// Overlapping backward copy.
	s3, err := s1.CopyWithin(0, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Release()
	if got := string(s3.Bytes()); got != "CDEFEFGH" {
		t.Fatalf("backward copy: %q", got)
	}
}

func TestBranchSnapshotsAreIndependent(t *testing.T) {
	st := NewStore(8, 0)
	base, _ := st.NewSlice([]byte("the quick brown fox jumps over"))
	defer base.Release()

	branch := base.Snapshot()
	defer branch.Release()

	// Intersecting edits on both branches: each affects only its target.
	left, err := base.Overwrite(4, []byte("SLOW"))
	if err != nil {
		t.Fatal(err)
	}
	defer left.Release()
	right, err := branch.Overwrite(4, []byte("LAZY"))
	if err != nil {
		t.Fatal(err)
	}
	defer right.Release()

	if got := string(base.Bytes()); got != "the quick brown fox jumps over" {
		t.Fatalf("base changed: %q", got)
	}
	if got := string(branch.Bytes()); got != "the quick brown fox jumps over" {
		t.Fatalf("branch changed: %q", got)
	}
	if got := string(left.Bytes()); got != "the SLOWk brown fox jumps over" {
		t.Fatalf("left: %q", got)
	}
	if got := string(right.Bytes()); got != "the LAZYk brown fox jumps over" {
		t.Fatalf("right: %q", got)
	}
	if err := st.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackOnLimitExceeded(t *testing.T) {
	st := NewStore(16, 16*10) // room for exactly 10 blocks
	s1, err := st.NewSlice(make([]byte, 16*5))
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Release()
	before := st.Stats()

	if _, err := s1.Insert(0, make([]byte, 16*6)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
	after := st.Stats()
	if after.LiveBytes != before.LiveBytes || after.TempBytes != 0 || after.Blocks != before.Blocks {
		t.Fatalf("ledger changed after rollback: %+v -> %+v", before, after)
	}
	if s1.Len() != 16*5 {
		t.Fatal("base slice damaged by failed edit")
	}
	if err := st.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
}

func TestCancelRollsBackWholeEdit(t *testing.T) {
	st := NewStore(16, 0)
	s1, _ := st.NewSlice([]byte("0123456789abcdef0123456789abcdef"))
	defer s1.Release()
	before := st.Stats()

	e := s1.Edit()
	if err := e.Overwrite(0, []byte("XXXXXXXX")); err != nil {
		t.Fatal(err)
	}
	if err := e.Insert(16, []byte("YYYYYYYYYYYYYYYY")); err != nil {
		t.Fatal(err)
	}
	if st.Stats().TempBytes == 0 {
		t.Fatal("temp copies not visible in ledger while edit in flight")
	}
	e.Cancel()

	// Cumulative history counters (Peak/Copied/Written) may move, but the
	// current ledger state must be fully restored.
	after := st.Stats()
	if after.Blocks != before.Blocks || after.TempBlocks != before.TempBlocks ||
		after.LiveBytes != before.LiveBytes || after.TempBytes != before.TempBytes ||
		after.LiveSlices != before.LiveSlices || after.IndexBytes != before.IndexBytes {
		t.Fatalf("cancel did not restore ledger: %+v -> %+v", before, after)
	}
	if err := st.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Commit(); err != ErrEditorClosed {
		t.Fatalf("commit after cancel: %v", err)
	}
}

func TestBorrowedBlocksAreNotReclaimed(t *testing.T) {
	st := NewStore(8, 0)
	s1, _ := st.NewSlice([]byte("ABCDEFGHIJKLMNOP")) // blocks b0,b1
	s2, err := s1.Overwrite(0, []byte("zz"))         // shares b1, new block for head
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Release()

	full := st.Stats().LiveBytes
	s1.Release() // drops borrow: b0 (only in s1) reclaimed, b1 (shared) kept
	if got := st.Stats().LiveBytes; got != full-8 {
		t.Fatalf("expected one block reclaimed, live=%d", got)
	}
	if got := string(s2.Bytes()); got != "zzCDEFGHIJKLMNOP" {
		t.Fatalf("borrower damaged: %q", got)
	}
	if err := st.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
	s2.Release()
	if got := st.Stats(); got.LiveBytes != 0 || got.Blocks != 0 {
		t.Fatalf("blocks leaked after all releases: %+v", got)
	}
}

func TestTempCopiesCountedInLedger(t *testing.T) {
	st := NewStore(64, 0)
	s1, _ := st.NewSlice(make([]byte, 64*10))
	defer s1.Release()

	var sawTemp int64
	st.onAlloc = func() { sawTemp = st.Stats().TempBytes }
	before := st.Stats()
	s2, err := s1.Overwrite(100, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Release()
	st.onAlloc = nil

	if sawTemp <= 0 {
		t.Fatal("temp block never appeared in ledger during edit")
	}
	if st.Stats().PeakBytes < before.LiveBytes+64 {
		t.Fatalf("peak %d does not include temp copy on top of %d live", st.Stats().PeakBytes, before.LiveBytes)
	}
}

func TestSmallEditDoesNotCopyWholeSequence(t *testing.T) {
	st := NewStore(64, 0)
	const logical = 1 << 20 // 1 MiB, 16384 blocks
	s1, err := st.NewSlice(pattern(logical))
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Release()

	before := st.Stats()
	s2, err := s1.Overwrite(500_001, []byte{0xFF}) // unaligned 1-byte edit
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Release()
	after := st.Stats()

	copied := after.CopiedBytes - before.CopiedBytes
	if copied > int64(2*st.BlockSize()) {
		t.Fatalf("1-byte edit copied %d bytes of a %d-byte sequence", copied, logical)
	}
	// Almost all physical blocks are shared between the two versions.
	ids1, ids2 := s1.BlockIDs(), s2.BlockIDs()
	if len(ids1) != len(ids2) {
		t.Fatal("block count changed by overwrite")
	}
	shared := 0
	for i := range ids1 {
		if ids1[i] == ids2[i] {
			shared++
		}
	}
	if shared < len(ids1)-2 {
		t.Fatalf("only %d/%d blocks shared after 1-byte edit", shared, len(ids1))
	}
	t.Logf("1-byte edit of %d bytes: copied %d bytes, shared %d/%d blocks",
		logical, copied, shared, len(ids1))
	if !bytes.Equal(s1.Bytes(), pattern(logical)) {
		t.Fatal("base version changed")
	}
}

func TestLedgerDetectsCorruption(t *testing.T) {
	st := NewStore(8, 0)
	s1, _ := st.NewSlice([]byte("ABCDEFGH"))
	defer s1.Release()
	if err := st.VerifyLedger(); err != nil {
		t.Fatal(err)
	}
	// Independent traversal must catch a tampered refcount.
	st.blocks[s1.exts[0]].refs++
	if err := st.VerifyLedger(); err == nil {
		t.Fatal("ledger verification missed tampered refcount")
	}
	st.blocks[s1.exts[0]].refs--
}
