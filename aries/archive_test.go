package aries

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCheckpointArchive covers the archiving boundary: records older than
// the checkpoint move to the archive, the END_CHKPT record stays live,
// and recovery still works from the truncated log.
func TestCheckpointArchive(t *testing.T) {
	dir := tempDir(t)
	defer cleanup(dir)
	archiveDir := filepath.Join(dir, "archive")

	wal, store, pool, tm := openEnv(t, dir)
	defer wal.Close()
	defer store.Close()

	// Committed history before the checkpoint.
	t1 := tm.Begin()
	mustWrite(t, tm, t1, 0, "arch-001")
	if err := tm.Commit(t1); err != nil {
		t.Fatalf("commit t1: %v", err)
	}
	chkLSN, err := tm.Checkpoint()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	// New work after the checkpoint (kept in the live log).
	t2 := tm.Begin()
	mustWrite(t, tm, t2, 1, "live-0001")
	if err := tm.Commit(t2); err != nil {
		t.Fatalf("commit t2: %v", err)
	}

	path, n, err := ArchiveCheckpoint(wal, pool, archiveDir, chkLSN, tm.ActiveTxns())
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if n == 0 {
		t.Fatal("archive moved 0 records")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("archive file missing: %v", err)
	}

	// The live log must start at the END_CHKPT record and contain only
	// records at or after it.
	first := true
	var firstLSN uint64
	count := 0
	if err := wal.Scan(func(r *LogRecord) bool {
		if first {
			firstLSN = r.LSN
			first = false
			if r.Type != LogEndCheckpoint {
				t.Fatalf("first live record = %s, want END_CHKPT", r.Type)
			}
		}
		if r.LSN < chkLSN {
			t.Fatalf("live log contains archived record LSN %d (< %d)", r.LSN, chkLSN)
		}
		count++
		return true
	}); err != nil {
		t.Fatalf("scan live: %v", err)
	}
	if firstLSN != chkLSN {
		t.Fatalf("first live LSN = %d, want checkpoint LSN %d", firstLSN, chkLSN)
	}
	if count == 0 {
		t.Fatal("live log empty after archive")
	}

	// LSNs must continue monotonically across the rewrite.
	t3 := tm.Begin()
	lsn, err := wal.Append(&LogRecord{TxnID: t3, Type: LogUpdate, PageID: 9, NewData: []byte("x")})
	if err != nil {
		t.Fatalf("append after archive: %v", err)
	}
	if lsn <= chkLSN {
		t.Fatalf("LSN after archive = %d, want > %d", lsn, chkLSN)
	}

	// Recovery from the truncated log must still work: t3 is a loser.
	wal.Flush()
	wal.Close()
	store.Close()

	wal2, store2, pool2, _ := openEnv(t, dir)
	defer wal2.Close()
	defer store2.Close()
	stats, err := Recover(wal2, pool2)
	if err != nil {
		t.Fatalf("recover after archive: %v", err)
	}
	if stats.CheckpointLSN != chkLSN {
		t.Fatalf("analysis checkpoint = %d, want %d", stats.CheckpointLSN, chkLSN)
	}
	if len(stats.Losers) != 1 || stats.Losers[0] != t3 {
		t.Fatalf("losers = %v, want [%d]", stats.Losers, t3)
	}
	for page, want := range map[uint32]string{0: "arch-001", 1: "live-0001"} {
		if got := pageString(t, store2, page, len(want)); got != want {
			t.Fatalf("page %d = %q, want %q", page, got, want)
		}
	}
}

// TestArchiveBoundaries covers invalid and degenerate archive requests.
func TestArchiveBoundaries(t *testing.T) {
	dir := tempDir(t)
	defer cleanup(dir)
	wal, store, pool, tm := openEnv(t, dir)
	defer wal.Close()
	defer store.Close()

	// upto = 0 is rejected.
	if _, _, err := wal.Archive(filepath.Join(dir, "a"), 0, nil); err == nil {
		t.Fatal("archive upto=0: want error, got nil")
	}
	// Archiving an empty log archives nothing.
	_, n, err := wal.Archive(filepath.Join(dir, "a"), 100, nil)
	if err != nil || n != 0 {
		t.Fatalf("archive empty log: n=%d err=%v, want 0,nil", n, err)
	}

	// Records of active transactions are kept even below the cutoff.
	tx := tm.Begin()
	mustWrite(t, tm, tx, 0, "keepme!!")
	chk, err := tm.Checkpoint()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	_, n, err = ArchiveCheckpoint(wal, pool, filepath.Join(dir, "a"), chk, tm.ActiveTxns())
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if n == 0 {
		t.Fatal("expected some records to be archived")
	}
	// The active transaction's UPDATE must still be in the live log so
	// undo can find it.
	found := false
	wal.Scan(func(r *LogRecord) bool {
		if r.TxnID == tx && r.Type == LogUpdate {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("active transaction's UPDATE was archived away")
	}
}

// TestSnapshotArchiveRecovery exercises the full pipeline: snapshot,
// checkpoint, archive (truncating the live WAL), more work, crash, then
// restore-from-snapshot plus recovery.
func TestSnapshotArchiveRecovery(t *testing.T) {
	dir := tempDir(t)
	defer cleanup(dir)
	snapDir := filepath.Join(dir, "snaps")
	archiveDir := filepath.Join(dir, "archive")

	wal, store, pool, tm := openEnv(t, dir)

	sn, err := NewSnapshotter(snapDir, pool, wal)
	if err != nil {
		t.Fatalf("snapshotter: %v", err)
	}

	t1 := tm.Begin()
	mustWrite(t, tm, t1, 0, "base-001")
	if err := tm.Commit(t1); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := sn.Take(); err != nil { // full base snapshot
		t.Fatalf("snapshot: %v", err)
	}
	chk, err := tm.Checkpoint()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, _, err := ArchiveCheckpoint(wal, pool, archiveDir, chk, tm.ActiveTxns()); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Post-archive work: one committed, one loser. Then crash.
	t2 := tm.Begin()
	mustWrite(t, tm, t2, 1, "post-comm")
	if err := tm.Commit(t2); err != nil {
		t.Fatalf("commit t2: %v", err)
	}
	t3 := tm.Begin()
	mustWrite(t, tm, t3, 0, "post-loser")
	wal.Flush()
	wal.Close()
	store.Close()
	sn.Close()

	// Reboot: restore the snapshot first, then recover from the live WAL.
	rstore, err := OpenPageStore(filepath.Join(dir, "pages.db"), 64)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer rstore.Close()
	if err := RestoreSnapshots(snapDir, rstore); err != nil {
		t.Fatalf("restore: %v", err)
	}
	wal2, err := OpenWAL(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	defer wal2.Close()
	pool2 := NewBufferPool(rstore)
	stats, err := Recover(wal2, pool2)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if len(stats.Losers) != 1 || stats.Losers[0] != t3 {
		t.Fatalf("losers = %v, want [%d]", stats.Losers, t3)
	}
	for page, want := range map[uint32]string{0: "base-001", 1: "post-comm"} {
		if got := pageString(t, rstore, page, len(want)); got != want {
			t.Fatalf("page %d = %q, want %q", page, got, want)
		}
	}
}
