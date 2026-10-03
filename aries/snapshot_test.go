package aries

import (
	"path/filepath"
	"strings"
	"testing"
)

// openEnv opens a fresh WAL + store + pool + txn manager in dir.
func openEnv(t *testing.T, dir string) (*WAL, *PageStore, *BufferPool, *TxnMgr) {
	t.Helper()
	wal, err := OpenWAL(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	store, err := OpenPageStore(filepath.Join(dir, "pages.db"), 64)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	pool := NewBufferPool(store)
	return wal, store, pool, NewTxnMgr(wal, pool)
}

// TestIncrementalSnapshotChain covers the snapshot lifecycle: full base,
// deltas containing only changed pages, a zero-change (empty) delta, and
// a restore that reproduces the exact live state.
func TestIncrementalSnapshotChain(t *testing.T) {
	dir := tempDir(t)
	defer cleanup(dir)
	snapDir := filepath.Join(dir, "snaps")

	wal, store, pool, tm := openEnv(t, dir)
	defer wal.Close()
	defer store.Close()

	sn, err := NewSnapshotter(snapDir, pool, wal)
	if err != nil {
		t.Fatalf("snapshotter: %v", err)
	}
	defer sn.Close()

	// Base data on pages 0..2.
	tx := tm.Begin()
	mustWrite(t, tm, tx, 0, "page0-v1")
	mustWrite(t, tm, tx, 1, "page1-v1")
	mustWrite(t, tm, tx, 2, "page2-v1")
	if err := tm.Commit(tx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	m0, err := sn.Take()
	if err != nil {
		t.Fatalf("take base: %v", err)
	}
	if !m0.Full || m0.Generation != 0 || m0.Pages != 3 {
		t.Fatalf("base meta = %+v, want full gen0 with 3 pages", m0)
	}

	// Change only page 1: the delta must contain exactly that page.
	tx = tm.Begin()
	mustWrite(t, tm, tx, 1, "page1-v2")
	if err := tm.Commit(tx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	m1, err := sn.Take()
	if err != nil {
		t.Fatalf("take delta1: %v", err)
	}
	if m1.Full || m1.Pages != 1 {
		t.Fatalf("delta1 meta = %+v, want incremental with 1 page", m1)
	}

	// No changes: an empty delta is legal and must restore cleanly.
	m2, err := sn.Take()
	if err != nil {
		t.Fatalf("take delta2: %v", err)
	}
	if m2.Full || m2.Pages != 0 {
		t.Fatalf("delta2 meta = %+v, want incremental with 0 pages", m2)
	}

	// Touch pages 0 and 2 across two txns.
	tx = tm.Begin()
	mustWrite(t, tm, tx, 0, "page0-v2")
	mustWrite(t, tm, tx, 2, "page2-v2")
	if err := tm.Commit(tx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	m3, err := sn.Take()
	if err != nil {
		t.Fatalf("take delta3: %v", err)
	}
	if m3.Pages != 2 {
		t.Fatalf("delta3 meta = %+v, want 2 pages", m3)
	}

	// Restore into a brand-new store and compare against live state.
	restoreDir := tempDir(t)
	defer cleanup(restoreDir)
	rstore, err := OpenPageStore(filepath.Join(restoreDir, "restored.db"), 64)
	if err != nil {
		t.Fatalf("open restore store: %v", err)
	}
	defer rstore.Close()
	if err := RestoreSnapshots(snapDir, rstore); err != nil {
		t.Fatalf("restore: %v", err)
	}
	want := map[uint32]string{0: "page0-v2", 1: "page1-v2", 2: "page2-v2"}
	for page, w := range want {
		if got := pageString(t, rstore, page, 8); got != w {
			t.Fatalf("restored page %d = %q, want %q", page, got, w)
		}
	}

	// Chain metadata must be complete and ordered.
	chain, err := sn.Chain()
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	if len(chain) != 4 {
		t.Fatalf("chain length = %d, want 4", len(chain))
	}
	for i, m := range chain {
		if m.Generation != i {
			t.Fatalf("chain[%d].Generation = %d", i, m.Generation)
		}
	}
}

// TestSnapshotRestoreErrors covers the failure boundaries of restore.
func TestSnapshotRestoreErrors(t *testing.T) {
	// Empty directory: no manifest.
	dir := tempDir(t)
	defer cleanup(dir)
	store, err := OpenPageStore(filepath.Join(dir, "p.db"), 64)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := RestoreSnapshots(filepath.Join(dir, "nope"), store); err == nil {
		t.Fatal("restore from missing dir: want error, got nil")
	}

	// Manifest exists but is empty.
	emptyDir := filepath.Join(dir, "empty")
	if err := writeManifest(emptyDir, &manifest{}); err != nil {
		t.Fatalf("write empty manifest: %v", err)
	}
	if err := RestoreSnapshots(emptyDir, store); err == nil ||
		!strings.Contains(err.Error(), "empty manifest") {
		t.Fatalf("restore empty manifest: want 'empty manifest' error, got %v", err)
	}

	// Manifest references a missing snapshot file.
	brokenDir := filepath.Join(dir, "broken")
	if err := writeManifest(brokenDir, &manifest{Snapshots: []SnapshotMeta{
		{Generation: 0, Full: true, File: "ghost.snap"},
	}}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := RestoreSnapshots(brokenDir, store); err == nil {
		t.Fatal("restore with missing snap file: want error, got nil")
	}

	// Chain does not start with a full snapshot.
	nonFullDir := filepath.Join(dir, "nonfull")
	meta := SnapshotMeta{Generation: 0, Full: false, File: "snap-000000.snap"}
	if err := writeSnapshotFile(filepath.Join(nonFullDir, "snap-000000.snap"), meta,
		map[uint32]SnapshotPage{}); err != nil {
		t.Fatalf("write snap: %v", err)
	}
	if err := writeManifest(nonFullDir, &manifest{Snapshots: []SnapshotMeta{meta}}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := RestoreSnapshots(nonFullDir, store); err == nil ||
		!strings.Contains(err.Error(), "full snapshot") {
		t.Fatalf("restore non-full base: want error, got %v", err)
	}
}

// TestSnapshotterResume verifies the generation counter survives a
// snapshotter restart (no overwriting of earlier generations).
func TestSnapshotterResume(t *testing.T) {
	dir := tempDir(t)
	defer cleanup(dir)
	snapDir := filepath.Join(dir, "snaps")

	wal, store, pool, tm := openEnv(t, dir)
	defer wal.Close()
	defer store.Close()

	sn, _ := NewSnapshotter(snapDir, pool, wal)
	tx := tm.Begin()
	mustWrite(t, tm, tx, 0, "resume-1")
	tm.Commit(tx)
	m0, _ := sn.Take()
	sn.Close()

	// Reopen the snapshotter on the same directory.
	sn2, err := NewSnapshotter(snapDir, pool, wal)
	if err != nil {
		t.Fatalf("reopen snapshotter: %v", err)
	}
	defer sn2.Close()
	tx = tm.Begin()
	mustWrite(t, tm, tx, 0, "resume-2")
	tm.Commit(tx)
	m1, err := sn2.Take()
	if err != nil {
		t.Fatalf("take after resume: %v", err)
	}
	if m1.Generation != m0.Generation+1 {
		t.Fatalf("generation after resume = %d, want %d", m1.Generation, m0.Generation+1)
	}
	if m1.Full {
		t.Fatal("snapshot after resume must be incremental")
	}
}
