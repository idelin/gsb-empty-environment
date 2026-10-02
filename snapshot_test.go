package aries

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestIncrementalSnapshotOnlyChangedPages 验证增量语义:
// 每次快照只归档自上次以来变更的页,空变更也产生合法空归档。
func TestIncrementalSnapshotOnlyChangedPages(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	defer db.Shutdown()
	snapDir := filepath.Join(dir, "snaps")
	sn, err := NewSnapshotter(db.BufferPool(), db.Log(), snapDir)
	if err != nil {
		t.Fatal(err)
	}

	txn, _ := db.Begin()
	mustUpdate(t, db, txn, 1, 0, "page-one")
	mustUpdate(t, db, txn, 2, 0, "page-two")
	db.Commit(txn)

	m1, err := sn.TakeIncremental()
	if err != nil {
		t.Fatal(err)
	}
	if m1.PageCount != 2 || m1.ID != 1 {
		t.Fatalf("snap1: pages=%d id=%d, want 2/1", m1.PageCount, m1.ID)
	}
	if m1.BaseLSN != 0 || m1.UpToLSN <= m1.BaseLSN {
		t.Fatalf("snap1 watermark: [%d,%d)", m1.BaseLSN, m1.UpToLSN)
	}

	txn2, _ := db.Begin()
	mustUpdate(t, db, txn2, 3, 0, "page-three")
	db.Commit(txn2)

	m2, err := sn.TakeIncremental()
	if err != nil {
		t.Fatal(err)
	}
	if m2.PageCount != 1 {
		t.Fatalf("snap2: pages=%d, want 1 (只应包含页 3)", m2.PageCount)
	}
	if m2.BaseLSN != m1.UpToLSN {
		t.Fatalf("snap2.BaseLSN = %d, want %d (水位必须衔接)", m2.BaseLSN, m1.UpToLSN)
	}
	_, pages, err := readSnapshotFile(filepath.Join(snapDir, m2.File))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].ID != 3 {
		t.Fatalf("snap2 contains %+v, want only page 3", pages)
	}

	// 无变更:空归档,水位仍推进。
	m3, err := sn.TakeIncremental()
	if err != nil {
		t.Fatal(err)
	}
	if m3.PageCount != 0 {
		t.Fatalf("snap3: pages=%d, want 0", m3.PageCount)
	}
	if _, pages, err := readSnapshotFile(filepath.Join(snapDir, m3.File)); err != nil || len(pages) != 0 {
		t.Fatalf("empty archive invalid: pages=%d err=%v", len(pages), err)
	}
	// 快照后缓冲池变更集应被排空。
	if got := db.BufferPool().ChangedCount(); got != 0 {
		t.Fatalf("changed set not drained: %d", got)
	}
}

// TestSnapshotRestore 验证归档可重放到全新磁盘,内容逐字节一致。
func TestSnapshotRestore(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	snapDir := filepath.Join(dir, "snaps")
	sn, _ := NewSnapshotter(db.BufferPool(), db.Log(), snapDir)

	txn, _ := db.Begin()
	mustUpdate(t, db, txn, 1, 0, "hello-snapshot")
	mustUpdate(t, db, txn, 7, 100, "deep-page")
	db.Commit(txn)
	if _, err := sn.TakeIncremental(); err != nil {
		t.Fatal(err)
	}
	txn2, _ := db.Begin()
	mustUpdate(t, db, txn2, 1, 32, "second-round")
	db.Commit(txn2)
	if _, err := sn.TakeIncremental(); err != nil {
		t.Fatal(err)
	}
	want1, _, _ := db.BufferPool().ReadPage(1)
	want7, _, _ := db.BufferPool().ReadPage(7)
	db.Shutdown()

	// 重放到全新磁盘。
	dm2, err := OpenDiskManager(filepath.Join(dir, "replica.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dm2.Close()
	metas, err := RestoreSnapshots(snapDir, dm2)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("restored %d snapshots, want 2", len(metas))
	}
	got1, _, _ := dm2.ReadPage(1)
	got7, _, _ := dm2.ReadPage(7)
	if !bytes.Equal(got1, want1) || !bytes.Equal(got7, want7) {
		t.Fatal("replica pages mismatch")
	}
}

// TestSnapshotCorruption 验证 CRC 能检出被篡改的归档。
func TestSnapshotCorruption(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	defer db.Shutdown()
	snapDir := filepath.Join(dir, "snaps")
	sn, _ := NewSnapshotter(db.BufferPool(), db.Log(), snapDir)
	txn, _ := db.Begin()
	mustUpdate(t, db, txn, 1, 0, "integrity!")
	db.Commit(txn)
	meta, err := sn.TakeIncremental()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(snapDir, meta.File)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xFF // 篡改
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	dm2, _ := OpenDiskManager(filepath.Join(dir, "replica.db"))
	defer dm2.Close()
	if _, err := RestoreSnapshots(snapDir, dm2); err == nil {
		t.Fatal("expected corruption error, got nil")
	}
}

// TestSnapshotManifestResume 验证快照器重启后续接水位与编号。
func TestSnapshotManifestResume(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	defer db.Shutdown()
	snapDir := filepath.Join(dir, "snaps")
	sn, _ := NewSnapshotter(db.BufferPool(), db.Log(), snapDir)
	txn, _ := db.Begin()
	mustUpdate(t, db, txn, 1, 0, "x")
	db.Commit(txn)
	m1, _ := sn.TakeIncremental()

	sn2, err := NewSnapshotter(db.BufferPool(), db.Log(), snapDir)
	if err != nil {
		t.Fatal(err)
	}
	if sn2.LastLSN() != m1.UpToLSN {
		t.Fatalf("resumed watermark = %d, want %d", sn2.LastLSN(), m1.UpToLSN)
	}
	m2, err := sn2.TakeIncremental()
	if err != nil {
		t.Fatal(err)
	}
	if m2.ID != m1.ID+1 {
		t.Fatalf("resumed ID = %d, want %d", m2.ID, m1.ID+1)
	}
	if got := len(sn2.Snapshots()); got != 2 {
		t.Fatalf("manifest has %d snapshots, want 2", got)
	}
}

// TestRestoreEmptyDir 验证空目录恢复是空操作。
func TestRestoreEmptyDir(t *testing.T) {
	dir := t.TempDir()
	dm, err := OpenDiskManager(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()
	metas, err := RestoreSnapshots(filepath.Join(dir, "no-such-dir"), dm)
	if err != nil || metas != nil {
		t.Fatalf("empty restore: metas=%v err=%v", metas, err)
	}
}

// TestCheckpointArchiveContent 验证检查点记录携带的 ATT/DPT 内容准确。
func TestCheckpointArchiveContent(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	defer db.Shutdown()
	t1, _ := db.Begin()
	t2, _ := db.Begin()
	mustUpdate(t, db, t1, 1, 0, "aaaa")
	mustUpdate(t, db, t2, 2, 0, "bbbb")
	db.Commit(t1) // t1 已提交,不应出现在 ATT
	mustUpdate(t, db, t2, 3, 0, "cccc")

	ckptLSN, err := db.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := db.Log().ReadRecord(ckptLSN)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Type != TypeCheckpoint || rec.Checkpoint == nil {
		t.Fatalf("record at %d is %v", ckptLSN, rec.Type)
	}
	cd := rec.Checkpoint
	if len(cd.ActiveTxns) != 1 {
		t.Fatalf("ATT size = %d, want 1 (只有 t2)", len(cd.ActiveTxns))
	}
	if _, ok := cd.ActiveTxns[t2.ID]; !ok {
		t.Fatalf("ATT missing t2")
	}
	dpt := db.BufferPool().DirtyPageTable()
	if len(cd.DirtyPages) != len(dpt) {
		t.Fatalf("DPT size = %d, want %d", len(cd.DirtyPages), len(dpt))
	}
	for page, recLSN := range dpt {
		if cd.DirtyPages[page] != recLSN {
			t.Fatalf("DPT[%d] = %d, want %d", page, cd.DirtyPages[page], recLSN)
		}
	}
	// master 记录指向检查点。
	got, err := ReadMasterCheckpoint(dir)
	if err != nil || got != ckptLSN {
		t.Fatalf("master = %d, %v; want %d", got, err, ckptLSN)
	}
}

// TestSnapshotBoundsRecovery 验证快照刷盘后恢复工作量有界:
// 快照之后崩溃,Redo 只需重放快照之后的少量日志。
func TestSnapshotBoundsRecovery(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	sn, _ := NewSnapshotter(db.BufferPool(), db.Log(), filepath.Join(dir, "snaps"))

	txn, _ := db.Begin()
	for i := 0; i < 50; i++ {
		mustUpdate(t, db, txn, uint64(1+i%8), i*8, "bulk-write")
	}
	db.Commit(txn)
	if _, err := sn.TakeIncremental(); err != nil { // 归档并刷盘
		t.Fatal(err)
	}
	ckpt, err := db.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	// 检查点后仅一笔已提交的新写(页不刷盘),然后崩溃。
	txn2, _ := db.Begin()
	mustUpdate(t, db, txn2, 1, 0, "post-snap!")
	if err := db.Commit(txn2); err != nil {
		t.Fatal(err)
	}
	db.lm.SyncAll()
	db.Close()

	rdb, res, err := RecoverDB(dir, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	if res.CheckpointLSN != ckpt {
		t.Fatalf("CheckpointLSN = %d, want %d", res.CheckpointLSN, ckpt)
	}
	// 页已刷盘,Redo 只需处理检查点后未落盘的极少记录。
	if res.RedoneCount > 2 {
		t.Fatalf("RedoneCount = %d, want <= 2 (快照应使恢复有界)", res.RedoneCount)
	}
	if got := pageRegion(t, rdb.Disk(), 1, 0, 10); !bytes.Equal(got, []byte("post-snap!")) {
		t.Fatalf("page1 = %q, want post-snap!", got)
	}
	assertDiskConsistentWithLog(t, rdb.Log(), rdb.Disk())
}
