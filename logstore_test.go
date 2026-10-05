package logstore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func appendPayloads(t *testing.T, dir string, payloads ...[]byte) {
	t.Helper()
	s, rep, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, p := range payloads {
		if _, err := s.Append(p); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_ = rep
}

func reopen(t *testing.T, dir string) (*Store, *RepairReport) {
	t.Helper()
	s, rep, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return s, rep
}

func checkRecords(t *testing.T, s *Store, want [][]byte) {
	t.Helper()
	if s.Len() != len(want) {
		t.Fatalf("record count = %d, want %d", s.Len(), len(want))
	}
	for i, w := range want {
		got, err := s.Read(i)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if !bytes.Equal(got, w) {
			t.Fatalf("record %d = %q, want %q", i, got, w)
		}
	}
}

func appendToFile(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return st.Size()
}

func dataPath(dir string) string  { return filepath.Join(dir, dataFileName) }
func indexPath(dir string) string { return filepath.Join(dir, indexFileName) }

var (
	recA = []byte("alpha")
	recB = []byte("bravo")
	recC = []byte("charlie")
)

// A clean reopen must report nothing and see all records.
func TestCleanReopen(t *testing.T) {
	dir := t.TempDir()
	appendPayloads(t, dir, recA, recB, recC)

	s, rep := reopen(t, dir)
	defer s.Close()
	if !rep.Clean() {
		t.Fatalf("expected clean report, got %+v", rep)
	}
	checkRecords(t, s, [][]byte{recA, recB, recC})
}

// F1: data persisted, index entry never written -> index lags, rebuild it.
func TestIndexLagsData(t *testing.T) {
	dir := t.TempDir()
	appendPayloads(t, dir, recA, recB)

	// Crash after writing recC to the data file but before the index write.
	appendToFile(t, dataPath(dir), encodeRecord(recC))

	s, rep := reopen(t, dir)
	if rep.RebuiltEntries != 1 {
		t.Fatalf("RebuiltEntries = %d, want 1 (%+v)", rep.RebuiltEntries, rep)
	}
	if rep.RebuildStart != uint64(len(encodeRecord(recA))+len(encodeRecord(recB))) {
		t.Fatalf("RebuildStart = %d, want end of recB", rep.RebuildStart)
	}
	checkRecords(t, s, [][]byte{recA, recB, recC})
	s.Close()

	// Second reopen: no misses, no duplicates, nothing left to repair.
	s2, rep2 := reopen(t, dir)
	defer s2.Close()
	if !rep2.Clean() {
		t.Fatalf("second reopen not clean: %+v", rep2)
	}
	checkRecords(t, s2, [][]byte{recA, recB, recC})
}

// F2: torn index entry (partial write) -> garbage tail discarded.
func TestTornIndexTail(t *testing.T) {
	dir := t.TempDir()
	appendPayloads(t, dir, recA, recB)

	// Crash in the middle of writing the third index entry.
	torn := encodeIndexEntry(indexEntry{offset: 0, length: 5, digest: 0xdead})
	appendToFile(t, indexPath(dir), torn[:indexEntrySize/2])

	s, rep := reopen(t, dir)
	if rep.IndexTruncatedBytes != indexEntrySize/2 {
		t.Fatalf("IndexTruncatedBytes = %d, want %d", rep.IndexTruncatedBytes, indexEntrySize/2)
	}
	if rep.IndexEntriesKept != 2 {
		t.Fatalf("IndexEntriesKept = %d, want 2", rep.IndexEntriesKept)
	}
	checkRecords(t, s, [][]byte{recA, recB})
	s.Close()

	s2, rep2 := reopen(t, dir)
	defer s2.Close()
	if !rep2.Clean() {
		t.Fatalf("second reopen not clean: %+v", rep2)
	}
	checkRecords(t, s2, [][]byte{recA, recB})
}

// F2b: full-size index entry with a bad checksum -> discarded and rebuilt.
func TestCorruptIndexTailEntry(t *testing.T) {
	dir := t.TempDir()
	appendPayloads(t, dir, recA, recB, recC)

	// Corrupt the last index entry (simulates a misdirected write).
	f, err := os.OpenFile(indexPath(dir), os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xff}, 2*indexEntrySize+9); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s, rep := reopen(t, dir)
	if rep.IndexEntriesKept != 2 {
		t.Fatalf("IndexEntriesKept = %d, want 2", rep.IndexEntriesKept)
	}
	if rep.RebuiltEntries != 1 {
		t.Fatalf("RebuiltEntries = %d, want 1", rep.RebuiltEntries)
	}
	checkRecords(t, s, [][]byte{recA, recB, recC})
	s.Close()

	s2, rep2 := reopen(t, dir)
	defer s2.Close()
	if !rep2.Clean() {
		t.Fatalf("second reopen not clean: %+v", rep2)
	}
	checkRecords(t, s2, [][]byte{recA, recB, recC})
}

// F3: torn data record at the tail -> truncated.
func TestTornDataTail(t *testing.T) {
	dir := t.TempDir()
	appendPayloads(t, dir, recA, recB)
	want := fileSize(t, dataPath(dir))

	// Crash halfway through writing recC to the data file.
	full := encodeRecord(recC)
	appendToFile(t, dataPath(dir), full[:len(full)-3])

	s, rep := reopen(t, dir)
	if !rep.DataTruncated {
		t.Fatalf("expected data truncation: %+v", rep)
	}
	if rep.DataSizeAfter != uint64(want) {
		t.Fatalf("DataSizeAfter = %d, want %d", rep.DataSizeAfter, want)
	}
	checkRecords(t, s, [][]byte{recA, recB})
	s.Close()

	s2, rep2 := reopen(t, dir)
	defer s2.Close()
	if !rep2.Clean() {
		t.Fatalf("second reopen not clean: %+v", rep2)
	}
	checkRecords(t, s2, [][]byte{recA, recB})
}

// F4: index entry persisted but the data record was lost -> roll the index
// back to the last entry that verifies against the data.
func TestIndexLeadsData(t *testing.T) {
	dir := t.TempDir()
	appendPayloads(t, dir, recA, recB, recC)

	// Simulate losing the last data record after the index was persisted.
	lost := int64(len(encodeRecord(recC)))
	if err := os.Truncate(dataPath(dir), fileSize(t, dataPath(dir))-lost); err != nil {
		t.Fatal(err)
	}

	s, rep := reopen(t, dir)
	if rep.IndexEntriesKept != 2 {
		t.Fatalf("IndexEntriesKept = %d, want 2", rep.IndexEntriesKept)
	}
	checkRecords(t, s, [][]byte{recA, recB})
	s.Close()

	s2, rep2 := reopen(t, dir)
	defer s2.Close()
	if !rep2.Clean() {
		t.Fatalf("second reopen not clean: %+v", rep2)
	}
	checkRecords(t, s2, [][]byte{recA, recB})
}

// F5: a complete record with a bad CRC must be reported as corrupt, not
// silently treated as tail truncation, and later records must survive.
func TestCorruptCompleteRecord(t *testing.T) {
	dir := t.TempDir()
	appendPayloads(t, dir, recA, recB, recC)
	dataSize := fileSize(t, dataPath(dir))

	// Flip one payload byte inside the middle record.
	offB := int64(len(encodeRecord(recA))) + recordHdrSize
	f, err := os.OpenFile(dataPath(dir), os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{'X'}, offB); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s, rep := reopen(t, dir)
	if rep.DataTruncated {
		t.Fatalf("corrupt record must not be treated as tail truncation: %+v", rep)
	}
	if rep.DataSizeAfter != uint64(dataSize) {
		t.Fatalf("DataSizeAfter = %d, want %d (no truncation)", rep.DataSizeAfter, dataSize)
	}
	if len(rep.Corrupt) != 1 {
		t.Fatalf("len(Corrupt) = %d, want 1 (%+v)", len(rep.Corrupt), rep)
	}
	c := rep.Corrupt[0]
	if c.Offset != uint64(len(encodeRecord(recA))) {
		t.Fatalf("corrupt offset = %d, want start of recB", c.Offset)
	}
	if c.Length != uint64(len(encodeRecord(recB))) {
		t.Fatalf("corrupt length = %d, want %d", c.Length, len(encodeRecord(recB)))
	}
	// recB is excluded; recA and recC remain indexed, in order.
	checkRecords(t, s, [][]byte{recA, recC})
	s.Close()

	// Reopen: the corrupt record is still reported (it stays on disk), the
	// index is not rebuilt, and no records are missed or duplicated.
	s2, rep2 := reopen(t, dir)
	defer s2.Close()
	if rep2.RebuiltEntries != 0 || rep2.DataTruncated {
		t.Fatalf("second reopen changed state: %+v", rep2)
	}
	if len(rep2.Corrupt) != 1 {
		t.Fatalf("second reopen lost the corrupt report: %+v", rep2)
	}
	checkRecords(t, s2, [][]byte{recA, recC})
}

// An unreadable record header (bad magic) must be reported, not silently
// truncated.
func TestGarbageHeaderReported(t *testing.T) {
	dir := t.TempDir()
	appendPayloads(t, dir, recA)

	appendToFile(t, dataPath(dir), []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04})

	_, rep := reopen(t, dir)
	if len(rep.Corrupt) != 1 || rep.Corrupt[0].Reason != "invalid record header" {
		t.Fatalf("expected invalid-header corrupt report, got %+v", rep.Corrupt)
	}
	if !rep.DataTruncated {
		t.Fatalf("expected truncation after unmeasurable record: %+v", rep)
	}
}

// After recovery, new appends must land at the right offset and survive
// another reopen without misses or duplicates.
func TestAppendAfterRecovery(t *testing.T) {
	dir := t.TempDir()
	appendPayloads(t, dir, recA, recB)

	// F1 + F3 combined: a complete unindexed record followed by torn bytes.
	appendToFile(t, dataPath(dir), encodeRecord(recC))
	appendToFile(t, dataPath(dir), []byte{0x00, 0x01, 0x02})

	s, _ := reopen(t, dir)
	checkRecords(t, s, [][]byte{recA, recB, recC})
	if _, err := s.Append(recA); err != nil {
		t.Fatalf("append after recovery: %v", err)
	}
	s.Close()

	s2, rep2 := reopen(t, dir)
	defer s2.Close()
	if !rep2.Clean() {
		t.Fatalf("reopen after append not clean: %+v", rep2)
	}
	checkRecords(t, s2, [][]byte{recA, recB, recC, recA})
}
