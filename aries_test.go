package aries

import (
	"bytes"
	"io"
	"testing"
)

// maxPageLSNs 扫描日志,返回每页最后被 UPDATE/CLR 修改的 LSN。
func maxPageLSNs(t *testing.T, lm *LogManager) map[uint64]LSN {
	t.Helper()
	m := make(map[uint64]LSN)
	it := lm.Iterator(0)
	for {
		rec, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("iterate log: %v", err)
		}
		if rec.Type == TypeUpdate || rec.Type == TypeCLR {
			if rec.LSN > m[rec.PageID] {
				m[rec.PageID] = rec.LSN
			}
		}
	}
	return m
}

// assertDiskConsistentWithLog 断言磁盘上每页的 pageLSN 与日志中该页最大修改 LSN 一致。
func assertDiskConsistentWithLog(t *testing.T, lm *LogManager, dm *DiskManager) {
	t.Helper()
	for page, want := range maxPageLSNs(t, lm) {
		got, err := dm.PageLSN(page)
		if err != nil {
			t.Fatalf("PageLSN(%d): %v", page, err)
		}
		if got != want {
			t.Fatalf("page %d: disk pageLSN=%d, want %d", page, got, want)
		}
	}
}

func pageRegion(t *testing.T, dm *DiskManager, page uint64, off, length int) []byte {
	t.Helper()
	data, _, err := dm.ReadPage(page)
	if err != nil {
		t.Fatalf("ReadPage(%d): %v", page, err)
	}
	return data[off : off+length]
}

// TestCrashRecoveryThreePhases 是验收场景:
// 3 个未提交事务 + 2 个已提交事务,检查点之后崩溃,
// 恢复后已提交数据必须持久,未提交修改必须回滚。
func TestCrashRecoveryThreePhases(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenDB(dir, 64)
	if err != nil {
		t.Fatal(err)
	}

	t1, _ := db.Begin()
	t2, _ := db.Begin()
	t3, _ := db.Begin()
	t4, _ := db.Begin()
	t5, _ := db.Begin()

	// 检查点之前的修改。
	mustUpdate(t, db, t1, 1, 0, "t1-before-ckpt")
	mustUpdate(t, db, t3, 2, 0, "t3-uncommitted")

	ckptLSN, err := db.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadMasterCheckpoint(dir); got != ckptLSN {
		t.Fatalf("master checkpoint = %d, want %d", got, ckptLSN)
	}

	// 检查点之后:t1、t2 提交;t3、t4、t5 保持未提交。
	mustUpdate(t, db, t1, 1, 32, "t1-after-ckpt")
	if err := db.Commit(t1); err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, db, t2, 3, 0, "t2-committed")
	if err := db.Commit(t2); err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, db, t3, 1, 0, "t3-dirty-write!") // 覆盖 t1 的已提交数据
	mustUpdate(t, db, t4, 2, 16, "t4-first")
	mustUpdate(t, db, t5, 3, 64, "t5-uncommitted")
	mustUpdate(t, db, t4, 2, 16, "t4-second") // 同事务同位置二次写,检验撤销链

	// 日志全部落盘(模拟已刷盘的 WAL),脏页全部留在内存 —— 崩溃。
	if err := db.lm.SyncAll(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil { // 只关句柄,不刷缓冲池
		t.Fatal(err)
	}

	// ---------- 恢复 ----------
	rdb, res, err := RecoverDB(dir, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()

	// Analysis:检查点被采用,ATT/DPT 重建正确。
	if res.CheckpointLSN != ckptLSN {
		t.Fatalf("CheckpointLSN = %d, want %d", res.CheckpointLSN, ckptLSN)
	}
	for _, p := range []uint64{1, 2, 3} {
		if _, ok := res.DirtyPageTable[p]; !ok {
			t.Fatalf("DPT missing page %d", p)
		}
	}
	wantStates := map[uint64]TxnState{
		t3.ID: TxnAborted, t4.ID: TxnAborted, t5.ID: TxnAborted,
	}
	for id, want := range wantStates {
		if got := res.TransactionTable[id]; got != want {
			t.Fatalf("txn %d state = %v, want %v", id, got, want)
		}
	}
	// Redo 起点必须早于检查点(t3 在检查点前就弄脏了页 2)。
	if res.RedoStartLSN >= ckptLSN {
		t.Fatalf("RedoStartLSN = %d, want < checkpoint %d", res.RedoStartLSN, ckptLSN)
	}
	if res.RedoneCount == 0 {
		t.Fatal("Redo replayed nothing")
	}
	// Undo:恰好回滚 t3、t4、t5,共 5 条 UPDATE -> 5 条 CLR。
	wantLosers := []uint64{t3.ID, t4.ID, t5.ID}
	if len(res.UndoneTxns) != len(wantLosers) {
		t.Fatalf("UndoneTxns = %v, want %v", res.UndoneTxns, wantLosers)
	}
	for i, id := range wantLosers {
		if res.UndoneTxns[i] != id {
			t.Fatalf("UndoneTxns = %v, want %v", res.UndoneTxns, wantLosers)
		}
	}
	if res.CLRCount != 5 {
		t.Fatalf("CLRCount = %d, want 5", res.CLRCount)
	}

	dm := rdb.Disk()
	// 已提交数据持久。
	if got := pageRegion(t, dm, 1, 0, 14); !bytes.Equal(got, []byte("t1-before-ckpt")) {
		t.Fatalf("page1[0:14] = %q, want t1-before-ckpt (t3 的脏写必须被撤销)", got)
	}
	if got := pageRegion(t, dm, 1, 32, 13); !bytes.Equal(got, []byte("t1-after-ckpt")) {
		t.Fatalf("page1[32:45] = %q, want t1-after-ckpt", got)
	}
	if got := pageRegion(t, dm, 3, 0, 12); !bytes.Equal(got, []byte("t2-committed")) {
		t.Fatalf("page3[0:13] = %q, want t2-committed", got)
	}
	// 未提交修改全部回滚(恢复为前像/零页)。
	if got := pageRegion(t, dm, 2, 0, 15); !bytes.Equal(got, make([]byte, 15)) {
		t.Fatalf("page2[0:15] = %q, want zeros (t3 未提交)", got)
	}
	if got := pageRegion(t, dm, 2, 16, 9); !bytes.Equal(got, make([]byte, 9)) {
		t.Fatalf("page2[16:25] = %q, want zeros (t4 未提交)", got)
	}
	if got := pageRegion(t, dm, 3, 64, 14); !bytes.Equal(got, make([]byte, 14)) {
		t.Fatalf("page3[64:78] = %q, want zeros (t5 未提交)", got)
	}
	// 磁盘页 LSN 与日志严格一致。
	assertDiskConsistentWithLog(t, rdb.Log(), dm)
}

func mustUpdate(t *testing.T, db *DB, txn *Transaction, page uint64, off int, s string) {
	t.Helper()
	if err := db.Update(txn, page, off, []byte(s)); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// TestRecoveryIdempotent 验证恢复幂等:二次恢复不再产生 CLR/Redo。
func TestRecoveryIdempotent(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	t1, _ := db.Begin()
	t2, _ := db.Begin()
	mustUpdate(t, db, t1, 1, 0, "committed-data")
	if err := db.Commit(t1); err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, db, t2, 1, 0, "uncommitted!!!")
	db.lm.SyncAll()
	db.Close()

	_, res1, err := RecoverDB(dir, 64)
	if err != nil {
		t.Fatal(err)
	}
	if res1.CLRCount != 1 || len(res1.UndoneTxns) != 1 {
		t.Fatalf("first recovery: CLR=%d losers=%v", res1.CLRCount, res1.UndoneTxns)
	}

	// 模拟恢复过程中再次崩溃后的二次恢复。
	db2, res2, err := RecoverDB(dir, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if res2.CLRCount != 0 || len(res2.UndoneTxns) != 0 {
		t.Fatalf("second recovery not idempotent: CLR=%d losers=%v", res2.CLRCount, res2.UndoneTxns)
	}
	if res2.RedoneCount != 0 {
		t.Fatalf("second recovery redid %d records, want 0", res2.RedoneCount)
	}
	if got := pageRegion(t, db2.Disk(), 1, 0, 14); !bytes.Equal(got, []byte("committed-data")) {
		t.Fatalf("page1 = %q, want committed-data", got)
	}
}

// TestRecoveryWithoutCheckpoint 验证无检查点时从头扫描也能正确恢复。
func TestRecoveryWithoutCheckpoint(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	t1, _ := db.Begin()
	t2, _ := db.Begin()
	mustUpdate(t, db, t1, 5, 8, "winner")
	mustUpdate(t, db, t2, 5, 8, "loser!")
	db.Commit(t1)
	db.lm.SyncAll()
	db.Close()

	rdb, res, err := RecoverDB(dir, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	if res.CheckpointLSN != NilLSN {
		t.Fatalf("CheckpointLSN = %d, want 0", res.CheckpointLSN)
	}
	if got := pageRegion(t, rdb.Disk(), 5, 8, 6); !bytes.Equal(got, []byte("winner")) {
		t.Fatalf("page5 = %q, want winner", got)
	}
	assertDiskConsistentWithLog(t, rdb.Log(), rdb.Disk())
}

// TestAbortRollback 验证运行期 Abort 通过 CLR 正确回滚。
func TestAbortRollback(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	defer db.Shutdown()
	t1, _ := db.Begin()
	mustUpdate(t, db, t1, 1, 0, "aaaaaaaa")
	mustUpdate(t, db, t1, 1, 8, "bbbbbbbb")
	if err := db.Abort(t1); err != nil {
		t.Fatal(err)
	}
	data, _, err := db.BufferPool().ReadPage(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data[:16], make([]byte, 16)) {
		t.Fatalf("page1[:16] = %q, want zeros after abort", data[:16])
	}
	if err := db.Update(t1, 1, 0, []byte("x")); err != ErrTxnNotRunning {
		t.Fatalf("update after abort: err = %v, want ErrTxnNotRunning", err)
	}
}

// TestCLRUndoChain 验证 CLR 的 undoNext 链在日志中正确链接。
func TestCLRUndoChain(t *testing.T) {
	dir := t.TempDir()
	db, _ := OpenDB(dir, 64)
	t1, _ := db.Begin()
	mustUpdate(t, db, t1, 2, 0, "first")
	mustUpdate(t, db, t1, 2, 0, "secnd")
	mustUpdate(t, db, t1, 2, 0, "third")
	db.lm.SyncAll()
	db.Close()

	rdb, res, err := RecoverDB(dir, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	if res.CLRCount != 3 {
		t.Fatalf("CLRCount = %d, want 3", res.CLRCount)
	}
	// 收集 CLR,验证 undoNext 逆序链接到对应的 UPDATE。
	var clrs, updates []*LogRecord
	it := rdb.Log().Iterator(0)
	for {
		rec, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch rec.Type {
		case TypeCLR:
			clrs = append(clrs, rec)
		case TypeUpdate:
			updates = append(updates, rec)
		}
	}
	if len(clrs) != 3 || len(updates) != 3 {
		t.Fatalf("clrs=%d updates=%d", len(clrs), len(updates))
	}
	// 撤销顺序与写入顺序相反:third -> secnd -> first。
	for i, clr := range clrs {
		want := updates[len(updates)-1-i]
		if clr.UndoNextLSN != want.PrevLSN {
			t.Fatalf("clr[%d].undoNext = %d, want %d", i, clr.UndoNextLSN, want.PrevLSN)
		}
		if !bytes.Equal(clr.AfterImage, want.BeforeImage) {
			t.Fatalf("clr[%d] after-image mismatch", i)
		}
	}
}
