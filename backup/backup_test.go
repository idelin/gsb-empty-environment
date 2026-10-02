
package backup

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"example.com/lsmbackup/lsm"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "backup-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeBatch(t *testing.T, e *lsm.Engine, gen, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key-%06d", i)
		val := fmt.Sprintf("gen%d-%060d", gen, i)
		if err := e.Put(key, val); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Flush(); err != nil {
		t.Fatal(err)
	}
}

// TestSnapshotSurvivesCompaction is the acceptance test: take a snapshot,
// then keep writing heavily and compact (which deletes the original files);
// the snapshot directory must remain a complete, verifiable copy.
func TestSnapshotSurvivesCompaction(t *testing.T) {
	base := tempDir(t)
	defer os.RemoveAll(base)

	e, err := lsm.Open(filepath.Join(base, "db"))
	if err != nil {
		t.Fatal(err)
	}
	x, err := NewExporter(e.Dir(), filepath.Join(base, "backups"))
	if err != nil {
		t.Fatal(err)
	}

	// Phase 1: load data and export a full snapshot.
	writeBatch(t, e, 0, 500)
	writeBatch(t, e, 1, 500)
	snap, err := x.Export()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Manifest.Files) != 2 {
		t.Fatalf("snapshot should contain 2 sstables, got %d", len(snap.Manifest.Files))
	}

	// Snapshot files are hard links to the very same inodes: zero-copy backup.
	for _, fe := range snap.Manifest.Files {
		src, err1 := os.Stat(filepath.Join(e.Dir(), fe.Name))
		dst, err2 := os.Stat(filepath.Join(snap.Dir, fe.Name))
		if err1 != nil || err2 != nil {
			t.Fatal(err1, err2)
		}
		if !os.SameFile(src, dst) {
			t.Fatalf("%s: snapshot file is a copy, not a hard link", fe.Name)
		}
	}

	// Phase 2: heavy foreground writes + compaction delete the old files.
	for gen := 2; gen < 8; gen++ {
		writeBatch(t, e, gen, 500)
	}
	if err := e.Compact(); err != nil {
		t.Fatal(err)
	}
	for _, fe := range snap.Manifest.Files {
		if _, err := os.Stat(filepath.Join(e.Dir(), fe.Name)); !os.IsNotExist(err) {
			t.Fatalf("compaction should have deleted %s from the live dir", fe.Name)
		}
	}

	// The snapshot directory is untouched and passes CRC verification.
	if err := Verify(snap.Dir, snap.Manifest); err != nil {
		t.Fatalf("snapshot corrupted after compaction: %v", err)
	}

	// The snapshot still serves the original (gen0/gen1) data.
	m, err := lsm.ReadSSTable(filepath.Join(snap.Dir, snap.Manifest.Files[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	if got := m["key-000042"]; got != fmt.Sprintf("gen0-%060d", 42) {
		t.Fatalf("snapshot data mismatch: %q", got)
	}
}

// TestIncrementalManifest checks that the second export records only the
// SSTables created since the first export.
func TestIncrementalManifest(t *testing.T) {
	base := tempDir(t)
	defer os.RemoveAll(base)

	e, err := lsm.Open(filepath.Join(base, "db"))
	if err != nil {
		t.Fatal(err)
	}
	x, err := NewExporter(e.Dir(), filepath.Join(base, "backups"))
	if err != nil {
		t.Fatal(err)
	}

	writeBatch(t, e, 0, 100)
	first, err := x.Export()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Added) != len(first.Manifest.Files) {
		t.Fatalf("first export must be a full backup: added=%d files=%d",
			len(first.Added), len(first.Manifest.Files))
	}

	writeBatch(t, e, 1, 100)
	writeBatch(t, e, 2, 100)
	second, err := x.Export()
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Added) != 2 {
		t.Fatalf("incremental list should have 2 new sstables, got %v", second.Added)
	}
	if len(second.Manifest.Files) != 3 {
		t.Fatalf("full manifest should have 3 sstables, got %d", len(second.Manifest.Files))
	}
	for _, name := range second.Added {
		if _, err := os.Stat(filepath.Join(second.Dir, name)); err != nil {
			t.Fatalf("incremental file %s missing from snapshot dir", name)
		}
	}
	if err := Verify(second.Dir, second.Manifest); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyDetectsCorruption makes sure Verify is not a no-op.
func TestVerifyDetectsCorruption(t *testing.T) {
	base := tempDir(t)
	defer os.RemoveAll(base)

	e, err := lsm.Open(filepath.Join(base, "db"))
	if err != nil {
		t.Fatal(err)
	}
	x, err := NewExporter(e.Dir(), filepath.Join(base, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	writeBatch(t, e, 0, 10)
	snap, err := x.Export()
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(snap.Dir, snap.Manifest); err != nil {
		t.Fatal(err)
	}

	victim := filepath.Join(snap.Dir, snap.Manifest.Files[0].Name)
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(victim, []byte("corrupted"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(snap.Dir, snap.Manifest); err == nil {
		t.Fatal("Verify should fail on corrupted snapshot")
	}
}
