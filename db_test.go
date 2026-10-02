package lsm

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "lsm-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func testOptions() Options {
	return Options{MemtableLimit: 1 << 20, LevelCap: 1, MaxLevels: 3}
}

func mustGet(t *testing.T, db *DB, key, want string) {
	t.Helper()
	got, err := db.Get(key)
	if err != nil {
		t.Fatalf("Get(%q) unexpected error: %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, got, want)
	}
}

func mustNotFound(t *testing.T, db *DB, key string) {
	t.Helper()
	if _, err := db.Get(key); err != ErrNotFound {
		t.Fatalf("Get(%q) err = %v, want ErrNotFound", key, err)
	}
}

// A delete in the memtable must immediately shadow data already flushed
// to disk, and vice versa.
func TestPointGetTombstoneMasking(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	db, err := Open(dir, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Put("K1", []byte("V1")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, db, "K1", "V1")

	// Tombstone still in memtable: masks nothing on disk yet, but the
	// read path must already answer NotFound.
	if err := db.Delete("K1"); err != nil {
		t.Fatal(err)
	}
	mustNotFound(t, db, "K1")

	// Value on disk (L0), tombstone in memtable.
	db2dir := tempDir(t)
	defer os.RemoveAll(db2dir)
	db2, err := Open(db2dir, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	db2.Put("K1", []byte("V1"))
	if err := db2.Flush(); err != nil {
		t.Fatal(err)
	}
	db2.Delete("K1")
	mustNotFound(t, db2, "K1")

	// Tombstone flushed to L0 as well: still NotFound.
	if err := db2.Flush(); err != nil {
		t.Fatal(err)
	}
	mustNotFound(t, db2, "K1")

	// A newer Put after the delete resurrects the key.
	db2.Put("K1", []byte("V2"))
	mustGet(t, db2, "K1", "V2")
}

func TestRangeScanMasksTombstones(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	db, err := Open(dir, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, kv := range []KV{{"K1", []byte("a")}, {"K2", []byte("b")}, {"K3", []byte("c")}, {"K4", []byte("d")}, {"K5", []byte("e")}} {
		if err := db.Put(kv.Key, kv.Value); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}
	db.Delete("K2")
	db.Delete("K4")

	assertScan := func(want []string) {
		t.Helper()
		got := db.Scan("K1", "K5")
		if len(got) != len(want) {
			t.Fatalf("Scan returned %d entries %v, want keys %v", len(got), got, want)
		}
		for i, kv := range got {
			if kv.Key != want[i] {
				t.Fatalf("Scan[%d].Key = %q, want %q", i, kv.Key, want[i])
			}
		}
	}

	assertScan([]string{"K1", "K3", "K5"})

	// Tombstones survive a flush and still mask during scans.
	if err := db.Flush(); err != nil {
		t.Fatal(err)
	}
	assertScan([]string{"K1", "K3", "K5"})
}

// Tombstones must be preserved at intermediate levels (they may still need
// to shadow older versions elsewhere) and only dropped at the bottom-most
// level.
func TestTombstonePreservedAtIntermediateLevel(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	db, err := Open(dir, testOptions()) // MaxLevels=3, LevelCap=1
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	db.Put("K1", []byte("V1"))
	db.Flush() // L0: [K1=V1]
	db.Delete("K1")
	db.Flush() // L0 overflows -> merged into L1, tombstone kept

	stats := db.Stats()
	if stats[1].Tombstones != 1 {
		t.Fatalf("L1 tombstones = %d, want 1 (tombstone must survive intermediate compaction)", stats[1].Tombstones)
	}
	if stats[1].Entries != 1 {
		t.Fatalf("L1 entries = %d, want 1 (older V1 version must be shadowed/merged away)", stats[1].Entries)
	}
	mustNotFound(t, db, "K1")
}

// Acceptance boundary: insert K1=V1, delete K1 (tombstone), Get returns
// NotFound immediately, and after enough compactions the key sinks to the
// bottom-most level where the tombstone is physically erased.
func TestTombstonePhysicallyPurgedAtBottomLevel(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	db, err := Open(dir, testOptions()) // MaxLevels=3, LevelCap=1
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	db.Put("K1", []byte("V1"))
	db.Flush()
	db.Delete("K1")
	mustNotFound(t, db, "K1") // immediate, tombstone still in memtable
	db.Flush()                // -> merged into L1 as a tombstone

	// Drive further compactions with unrelated keys until the tombstone
	// sinks to the bottom-most level and is erased, and the tree fully
	// converges into a single bottom-level table.
	for i := 0; i < 10; i++ {
		db.Put("filler", []byte{byte('a' + i)})
		db.Flush()
	}

	// 1. Key still reads as deleted.
	mustNotFound(t, db, "K1")

	// 2. No tombstones remain anywhere in the tree.
	totalEntries, totalTombstones := 0, 0
	for _, s := range db.Stats() {
		totalEntries += s.Entries
		totalTombstones += s.Tombstones
	}
	if totalTombstones != 0 {
		t.Fatalf("tombstones remaining after bottom-level compaction: %d", totalTombstones)
	}
	if totalEntries != 1 { // only the latest "filler" version survives
		t.Fatalf("total on-disk entries = %d, want 1", totalEntries)
	}

	// 3. No trace of "K1" in any physical file.
	files, err := filepath.Glob(filepath.Join(dir, "*.sst"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one sstable file")
	}
	for _, f := range files {
		data, err := ioutil.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("K1")) {
			t.Fatalf("file %s still physically contains key K1", f)
		}
	}
}

// Deletes must survive a restart: tombstones are durable records.
func TestPersistenceAcrossReopen(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	db, err := Open(dir, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	db.Put("keep", []byte("yes"))
	db.Put("gone", []byte("x"))
	db.Flush()
	db.Delete("gone")
	if err := db.Close(); err != nil { // flushes the tombstone
		t.Fatal(err)
	}

	db2, err := Open(dir, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	mustGet(t, db2, "keep", "yes")
	mustNotFound(t, db2, "gone")
}
