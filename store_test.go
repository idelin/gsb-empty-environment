package blkmap

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

const testBS = 64 // small blocks to exercise splits/merges

func openTest(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir, testBS)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func fill(b []byte, seed byte) {
	for i := range b {
		b[i] = seed + byte(i%13)
	}
}

func mustWrite(t *testing.T, s *Store, p []byte, off int64) {
	t.Helper()
	if _, err := s.WriteAt(p, off); err != nil {
		t.Fatalf("WriteAt(%d): %v", off, err)
	}
}

func readAll(t *testing.T, s *Store, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	if _, err := s.ReadAt(buf, 0); err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	return buf
}

func checkInvariant(t *testing.T, exts []Extent) {
	t.Helper()
	for i := 1; i < len(exts); i++ {
		prev, cur := exts[i-1], exts[i]
		if prev.Off+prev.Len > cur.Off {
			t.Fatalf("extents overlap: %+v vs %+v", prev, cur)
		}
		if prev.Off+prev.Len == cur.Off && prev.Block == cur.Block && prev.BOff+prev.Len == cur.BOff {
			t.Fatalf("adjacent mergeable extents not merged: %+v vs %+v", prev, cur)
		}
	}
	if !sortedNonOverlapping(exts) {
		t.Fatal("extents not sorted/non-overlapping")
	}
}

func countBlocks(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".blk" {
			n++
		}
	}
	return n
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	data := make([]byte, 3*testBS)
	fill(data, 7)
	mustWrite(t, s, data, 0)
	if got := readAll(t, s, len(data)); !bytes.Equal(got, data) {
		t.Fatal("round trip mismatch")
	}
	// Unwritten region beyond the data reads as zeros.
	if got := readAll(t, s, len(data)+2*testBS)[len(data):]; !bytes.Equal(got, make([]byte, 2*testBS)) {
		t.Fatal("unwritten region not zero")
	}
}

func TestCrossBlockWriteOnlyTouchesCoveredRange(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	base := make([]byte, 2*testBS)
	fill(base, 'A')
	mustWrite(t, s, base, 0)

	// Write 20 bytes straddling the boundary of block 0 and block 1.
	patch := make([]byte, 20)
	fill(patch, 'x')
	mustWrite(t, s, patch, testBS-10)

	want := make([]byte, 2*testBS)
	copy(want, base)
	copy(want[testBS-10:], patch)
	if got := readAll(t, s, 2*testBS); !bytes.Equal(got, want) {
		t.Fatalf("cross-block write clobbered uncovered bytes\ngot  %v\nwant %v", got, want)
	}
	checkInvariant(t, s.Extents())
}

func TestPunchHoleMiddleKeepsBothSides(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	data := make([]byte, 4*testBS)
	fill(data, 3)
	mustWrite(t, s, data, 0)

	// Punch a range that starts and ends mid-block.
	holeOff, holeLen := int64(testBS+10), int64(2*testBS-20)
	if err := s.PunchHole(holeOff, holeLen); err != nil {
		t.Fatalf("PunchHole: %v", err)
	}

	got := readAll(t, s, 4*testBS)
	if !bytes.Equal(got[:holeOff], data[:holeOff]) {
		t.Fatal("left side of hole changed")
	}
	if !bytes.Equal(got[holeOff+holeLen:], data[holeOff+holeLen:]) {
		t.Fatal("right side of hole changed")
	}
	if !bytes.Equal(got[holeOff:holeOff+holeLen], make([]byte, holeLen)) {
		t.Fatal("hole does not read as zero")
	}
	// The middle block (fully inside the hole) must be gone from the map.
	for _, e := range s.Extents() {
		if e.Off >= uint64(holeOff) && e.Off+e.Len <= uint64(holeOff+holeLen) {
			t.Fatalf("extent survives inside hole: %+v", e)
		}
	}
	checkInvariant(t, s.Extents())
}

func TestHoleOccupiesNoPhysicalBlock(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	data := make([]byte, 3*testBS)
	fill(data, 9)
	mustWrite(t, s, data, 0)
	before := countBlocks(t, dir)
	if err := s.PunchHole(testBS, testBS); err != nil {
		t.Fatalf("PunchHole: %v", err)
	}
	// Punching allocates nothing; the orphaned block is reclaimed on reopen.
	if got := countBlocks(t, dir); got != before {
		t.Fatalf("punch allocated blocks: %d -> %d", before, got)
	}
	s = openTest(t, dir) // reopen triggers orphan GC
	if got := countBlocks(t, dir); got != before-1 {
		t.Fatalf("orphan block not reclaimed: got %d, want %d", got, before-1)
	}
}

func TestSnapshotImmuneToLaterPunchAndWrite(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	data := make([]byte, 3*testBS)
	fill(data, 21)
	mustWrite(t, s, data, 0)
	if err := s.Snapshot("s1"); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Punch the middle and overwrite the tail after the snapshot.
	if err := s.PunchHole(testBS, testBS); err != nil {
		t.Fatalf("PunchHole: %v", err)
	}
	tail := make([]byte, testBS)
	fill(tail, 'Z')
	mustWrite(t, s, tail, 2*testBS)

	// Live view: hole is zero, tail is new.
	live := readAll(t, s, 3*testBS)
	if !bytes.Equal(live[testBS:2*testBS], make([]byte, testBS)) {
		t.Fatal("live hole not zero")
	}
	if !bytes.Equal(live[2*testBS:], tail) {
		t.Fatal("live tail not updated")
	}
	// Snapshot: byte-for-byte the original data.
	snap := make([]byte, 3*testBS)
	if _, err := s.ReadSnapshotAt("s1", snap, 0); err != nil {
		t.Fatalf("ReadSnapshotAt: %v", err)
	}
	if !bytes.Equal(snap, data) {
		t.Fatal("snapshot disturbed by later punch/write")
	}
	// Snapshot survives a reopen.
	s = openTest(t, dir)
	snap2 := make([]byte, 3*testBS)
	if _, err := s.ReadSnapshotAt("s1", snap2, 0); err != nil {
		t.Fatalf("ReadSnapshotAt after reopen: %v", err)
	}
	if !bytes.Equal(snap2, data) {
		t.Fatal("snapshot not durable across reopen")
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	data := make([]byte, 5*testBS)
	fill(data, 42)
	mustWrite(t, s, data, testBS/2) // unaligned start
	if err := s.PunchHole(2*testBS, testBS); err != nil {
		t.Fatalf("PunchHole: %v", err)
	}
	want := readAll(t, s, 6*testBS)

	s = openTest(t, dir)
	if got := readAll(t, s, 6*testBS); !bytes.Equal(got, want) {
		t.Fatal("state changed across reopen")
	}
	checkInvariant(t, s.Extents())
}

// crashAt returns an Inject hook that fails the nth occurrence of point.
func crashAt(point string) func(string) error {
	return func(p string) error {
		if p == point {
			return errors.New("injected crash at " + p)
		}
		return nil
	}
}

func TestCrashBeforeManifestCommitKeepsOldState(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	old := make([]byte, testBS)
	fill(old, 1)
	mustWrite(t, s, old, 0)

	// "Crash" after the new block is committed but before the manifest
	// rename: the write must be all-or-nothing from the reader's view.
	s.Inject = crashAt(PointManifestStaged)
	if _, err := s.WriteAt(make([]byte, testBS), 0); err == nil {
		t.Fatal("expected injected error")
	}
	blocksBefore := countBlocks(t, dir)

	s = openTest(t, dir) // process restart: recover from the directory
	if got := readAll(t, s, testBS); !bytes.Equal(got, old) {
		t.Fatal("old state lost after crash before manifest commit")
	}
	if got := countBlocks(t, dir); got != blocksBefore-1 {
		t.Fatalf("orphan block not reclaimed: got %d, want %d", got, blocksBefore-1)
	}
	checkInvariant(t, s.Extents())
}

func TestCrashDuringBlockWriteLeavesNoTrace(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	old := make([]byte, testBS)
	fill(old, 5)
	mustWrite(t, s, old, 0)

	// "Crash" after the block temp file is fsynced but before rename.
	s.Inject = crashAt(PointBlockStaged)
	if _, err := s.WriteAt(make([]byte, testBS), 0); err == nil {
		t.Fatal("expected injected error")
	}

	s = openTest(t, dir)
	if got := readAll(t, s, testBS); !bytes.Equal(got, old) {
		t.Fatal("old state lost after crash during block write")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("stale temp file not cleaned: %s", e.Name())
		}
	}
}

func TestCrashAfterManifestCommitIsDurable(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	data := make([]byte, testBS)
	fill(data, 11)
	// "Crash" right after the manifest is committed: the write is durable.
	s.Inject = crashAt(PointManifestCommitted)
	if _, err := s.WriteAt(data, 0); err == nil {
		t.Fatal("expected injected error")
	}
	s = openTest(t, dir)
	if got := readAll(t, s, testBS); !bytes.Equal(got, data) {
		t.Fatal("committed write lost after crash")
	}
}

func TestRandomOpsAgainstReferenceModel(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	rng := rand.New(rand.NewSource(1))
	ref := make([]byte, 8*testBS)

	for i := 0; i < 300; i++ {
		off := rng.Intn(len(ref))
		n := 1 + rng.Intn(2*testBS)
		if off+n > len(ref) {
			n = len(ref) - off
		}
		switch rng.Intn(2) {
		case 0: // write
			p := make([]byte, n)
			fill(p, byte(rng.Intn(256)))
			mustWrite(t, s, p, int64(off))
			copy(ref[off:off+n], p)
		case 1: // punch
			if err := s.PunchHole(int64(off), int64(n)); err != nil {
				t.Fatalf("PunchHole: %v", err)
			}
			for j := off; j < off+n; j++ {
				ref[j] = 0
			}
		}
		if i%50 == 0 {
			s = openTest(t, dir) // periodic reopen to exercise recovery path
		}
	}
	if got := readAll(t, s, len(ref)); !bytes.Equal(got, ref) {
		t.Fatal("random ops diverged from reference model")
	}
	checkInvariant(t, s.Extents())
}

func TestSnapshotAfterReopenAndDelete(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	data := make([]byte, 2*testBS)
	fill(data, 77)
	mustWrite(t, s, data, 0)
	for i := 0; i < 3; i++ {
		if err := s.Snapshot(fmt.Sprintf("snap%d", i)); err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
	}
	if err := s.DeleteSnapshot("snap1"); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	s = openTest(t, dir)
	if _, err := s.ReadSnapshotAt("snap1", make([]byte, 1), 0); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("deleted snapshot still readable: %v", err)
	}
	got := make([]byte, 2*testBS)
	if _, err := s.ReadSnapshotAt("snap2", got, 0); err != nil {
		t.Fatalf("ReadSnapshotAt: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("snapshot data mismatch")
	}
}
