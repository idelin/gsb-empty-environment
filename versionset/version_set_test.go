package versionset

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// tempDir returns a fresh temporary directory; callers must
// defer os.RemoveAll(dir) (Go 1.11 has no t.Cleanup).
func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "versionset-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// addL0File writes an SSTable and commits it at level 0.
func addL0File(t *testing.T, vs *VersionSet, smallest, largest, data string) FileMetadata {
	t.Helper()
	meta, err := vs.WriteSST(smallest, largest, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	edit := &VersionEdit{NewFiles: []NewFileEntry{{Level: 0, Meta: meta}}}
	if err := vs.LogAndApply(edit); err != nil {
		t.Fatal(err)
	}
	return meta
}

func levelFiles(v *Version, level int) []uint64 {
	var nums []uint64
	for _, f := range v.Files[level] {
		nums = append(nums, f.Number)
	}
	return nums
}

func sstFilesOnDisk(t *testing.T, dir string) []string {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".sst" {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestRecoverAfterCleanShutdown(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	vs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m1 := addL0File(t, vs, "a", "c", "aaa")
	m2 := addL0File(t, vs, "d", "f", "ddd")
	if err := vs.Close(); err != nil {
		t.Fatal(err)
	}

	vs2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer vs2.Close()
	got := levelFiles(vs2.Current(), 0)
	want := []uint64{m1.Number, m2.Number}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("L0 files = %v, want %v", got, want)
	}
	if err := vs2.VerifyChecksums(); err != nil {
		t.Fatal(err)
	}
}

// TestCrashDuringL0ToL1Compaction is the acceptance scenario: the process
// "crashes" right after the compaction output file hits disk but before the
// VersionEdit is committed. Recovery must drop the orphan and restore the
// pre-compaction version.
func TestCrashDuringL0ToL1Compaction(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	vs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m1 := addL0File(t, vs, "a", "c", "data-a-c")
	m2 := addL0File(t, vs, "d", "f", "data-d-f")

	// --- Compaction starts: write the merged L1 output file... ---
	orphan, err := vs.WriteSST("a", "f", []byte("merged-a-f"))
	if err != nil {
		t.Fatal(err)
	}
	// --- CRASH: no LogAndApply, no Close. The edit is never committed. ---

	// Sanity: the orphan file exists on disk before recovery.
	if _, err := os.Stat(vs.SSTPath(orphan.Number)); err != nil {
		t.Fatalf("orphan should exist pre-recovery: %v", err)
	}

	// --- Restart: replay manifest, clean up orphans. ---
	vs2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer vs2.Close()

	cur := vs2.Current()
	if got, want := levelFiles(cur, 0), []uint64{m1.Number, m2.Number}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L0 files = %v, want %v (pre-compaction state)", got, want)
	}
	if got := levelFiles(cur, 1); len(got) != 0 {
		t.Fatalf("L1 should be empty after crash, got %v", got)
	}
	if _, err := os.Stat(vs2.SSTPath(orphan.Number)); !os.IsNotExist(err) {
		t.Fatalf("orphan %06d.sst should have been removed, stat err=%v", orphan.Number, err)
	}
	if got := sstFilesOnDisk(t, dir); len(got) != 2 {
		t.Fatalf("disk should hold exactly the 2 live SSTables, got %v", got)
	}
	if err := vs2.VerifyChecksums(); err != nil {
		t.Fatal(err)
	}

	// The system must keep working: retry the compaction to completion.
	out, err := vs2.CompactL0ToL1([]uint64{m1.Number, m2.Number}, "a", "f", []byte("merged-a-f"))
	if err != nil {
		t.Fatal(err)
	}
	cur = vs2.Current()
	if got := levelFiles(cur, 0); len(got) != 0 {
		t.Fatalf("L0 should be empty after committed compaction, got %v", got)
	}
	if got, want := levelFiles(cur, 1), []uint64{out.Number}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L1 files = %v, want %v", got, want)
	}
}

// TestCommittedCompactionSurvivesRestart verifies the other side of
// atomicity: once LogAndApply returns, the new version is durable.
func TestCommittedCompactionSurvivesRestart(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	vs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m1 := addL0File(t, vs, "a", "c", "data-a-c")
	m2 := addL0File(t, vs, "d", "f", "data-d-f")
	out, err := vs.CompactL0ToL1([]uint64{m1.Number, m2.Number}, "a", "f", []byte("merged-a-f"))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate crash after commit (no Close).
	vs2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer vs2.Close()
	cur := vs2.Current()
	if got := levelFiles(cur, 0); len(got) != 0 {
		t.Fatalf("L0 = %v, want empty", got)
	}
	if got, want := levelFiles(cur, 1), []uint64{out.Number}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L1 = %v, want %v", got, want)
	}
	if err := vs2.VerifyChecksums(); err != nil {
		t.Fatal(err)
	}
}

// TestTornManifestTail simulates a crash in the middle of a manifest write:
// the trailing partial record must be ignored on recovery.
func TestTornManifestTail(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	vs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m1 := addL0File(t, vs, "a", "c", "data-a-c")
	if err := vs.Close(); err != nil {
		t.Fatal(err)
	}

	// Append garbage: half a record header + partial payload.
	manifest := filepath.Join(dir, "MANIFEST-000001")
	f, err := os.OpenFile(manifest, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0x20, 0x00, 0x00, 0x00, 0xde, 0xad}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	vs2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer vs2.Close()
	if got, want := levelFiles(vs2.Current(), 0), []uint64{m1.Number}; !reflect.DeepEqual(got, want) {
		t.Fatalf("L0 files = %v, want %v", got, want)
	}
}

// TestChecksumMismatchDetected corrupts a live SSTable and expects
// VerifyChecksums to catch it via the manifest-recorded CRC32.
func TestChecksumMismatchDetected(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	vs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m1 := addL0File(t, vs, "a", "c", "original-data")
	defer vs.Close()
	if err := vs.VerifyChecksums(); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(vs.SSTPath(m1.Number), []byte("corrupted-data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := vs.VerifyChecksums(); err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
}

// TestFileNumberingSurvivesRestart ensures file numbers are never reused
// after recovery, so orphans can never be mistaken for new files.
func TestFileNumberingSurvivesRestart(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	vs, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m1 := addL0File(t, vs, "a", "c", "x")
	orphan, err := vs.WriteSST("d", "f", []byte("y")) // never committed
	if err != nil {
		t.Fatal(err)
	}
	// Crash.
	vs2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer vs2.Close()
	m2 := addL0File(t, vs2, "g", "i", "z")
	if m2.Number <= orphan.Number || m2.Number <= m1.Number {
		t.Fatalf("file number reused after recovery: got %d, orphan was %d", m2.Number, orphan.Number)
	}
}
