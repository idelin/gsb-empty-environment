package vrec_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	vrec "vrecstore/vrec"
)

func mustOpen(t *testing.T, hook vrec.FailHook) (*vrec.DB, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	opts := &vrec.Options{FailHook: hook}
	db, err := vrec.Open(dir, opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return db, dir
}

func putOK(t *testing.T, db *vrec.DB, k, v string) int64 {
	t.Helper()
	before := db.HeadVersion()
	if err := db.Update(func(tx *vrec.Txn) error {
		return tx.Put(k, []byte(v))
	}); err != nil {
		t.Fatalf("put %s=%s: %v", k, v, err)
	}
	if got := db.HeadVersion(); got != before+1 {
		t.Fatalf("head = %d, want %d", got, before+1)
	}
	return db.HeadVersion()
}

func mustGet(t *testing.T, r interface {
	Get(string) ([]byte, error)
}, key, want string) {
	t.Helper()
	got, err := r.Get(key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("get %s = %q, want %q", key, got, want)
	}
}

func TestPutGetSnapshotIsolation(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()

	putOK(t, db, "a", "v1")
	r, err := db.BeginReader()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	snap := r.Snapshot()
	putOK(t, db, "a", "v2")

	// 老读者仍读旧值。
	mustGet(t, r, "a", "v1")
	// 新读者读到新值。
	if err := db.View(func(nr *vrec.Reader) error {
		mustGet(t, nr, "a", "v2")
		if nr.Snapshot() <= snap {
			t.Fatalf("new snapshot %d not greater than %d", nr.Snapshot(), snap)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteTombstone(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()

	putOK(t, db, "a", "v1")
	r, _ := db.BeginReader()
	defer r.Close()

	if err := db.Update(func(tx *vrec.Txn) error { return tx.Delete("a") }); err != nil {
		t.Fatal(err)
	}
	// 老读者仍看到删除前的值。
	mustGet(t, r, "a", "v1")
	// 新读者看到墓碑 -> NotFound。
	if err := db.View(func(nr *vrec.Reader) error {
		if _, err := nr.Get("a"); !errors.Is(err, vrec.ErrNotFound) {
			return fmt.Errorf("want ErrNotFound, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 删除后重新写，历史链上同时存在墓碑与新值。
	putOK(t, db, "a", "v2")
	if err := db.View(func(nr *vrec.Reader) error {
		mustGet(t, nr, "a", "v2")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGCBoundaryByOldestReader(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()

	for _, v := range []string{"v1", "v2", "v3"} {
		putOK(t, db, "a", v)
	}
	// a: v1,v2,v3 共三个节点。
	if h := db.History("a"); len(h) != 3 {
		t.Fatalf("history len = %d, want 3", len(h))
	}

	// 老读者固定在 v3，GC 不能释放任何 a 的链前缀（v3 是边界，之上无节点）。
	r, _ := db.BeginReader()
	putOK(t, db, "a", "v4")
	st, err := db.RunGC()
	if err != nil {
		t.Fatal(err)
	}
	// 水位=3：v1,v2 已早于任何活跃读者，可释放；必须保留边界 v3 与新节点 v4。
	if st.Watermark != 3 || st.NodesRemoved != 2 {
		t.Fatalf("watermark=%d removed=%d, want 3/2", st.Watermark, st.NodesRemoved)
	}
	if h := db.History("a"); len(h) != 2 || h[0].Version != 3 || h[1].Version != 4 {
		t.Fatalf("history = %+v, want v3,v4", h)
	}
	mustGet(t, r, "a", "v3")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	// 关闭老读者后再 GC：水位升到 4，只保留 v4。
	st, err = db.RunGC()
	if err != nil {
		t.Fatal(err)
	}
	if st.NodesRemoved != 1 {
		t.Fatalf("removed %d, want 1", st.NodesRemoved)
	}
	if h := db.History("a"); len(h) != 1 || h[0].Version != 4 {
		t.Fatalf("history after gc = %+v, want only v4", h)
	}
	if st.OldWALBytes <= st.NewWALBytes {
		t.Fatalf("wal not shrunk: %d -> %d", st.OldWALBytes, st.NewWALBytes)
	}
	if err := db.View(func(nr *vrec.Reader) error {
		mustGet(t, nr, "a", "v4")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGCPartialPrefixKeptAtWatermark(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()

	putOK(t, db, "a", "v1")
	putOK(t, db, "a", "v2")
	// 读者固定在 v2，之后继续写。
	old, _ := db.BeginReader()
	putOK(t, db, "a", "v3")
	putOK(t, db, "a", "v4")
	putOK(t, db, "a", "v5")

	st, err := db.RunGC()
	if err != nil {
		t.Fatal(err)
	}
	// 水位=2：保留边界节点 v2 与 v3,v4,v5，只释放 v1 一个前缀节点。
	if st.Watermark != 2 {
		t.Fatalf("watermark = %d, want 2", st.Watermark)
	}
	if st.NodesRemoved != 1 {
		t.Fatalf("removed = %d, want 1", st.NodesRemoved)
	}
	h := db.History("a")
	if len(h) != 4 || h[0].Version != 2 {
		t.Fatalf("history = %+v, want versions 2,3,4,5", h)
	}
	// 老读者仍读 v2；v1 已不可达且老读者也不需要它。
	mustGet(t, old, "a", "v2")
	if _, err := old.Get("a"); err != nil {
		t.Fatal(err)
	}
	old.Close()
}

func TestGCDropsVanishedKeys(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()

	putOK(t, db, "a", "1")
	putOK(t, db, "b", "1")
	if err := db.Update(func(tx *vrec.Txn) error { return tx.Delete("b") }); err != nil {
		t.Fatal(err)
	}
	st, err := db.RunGC()
	if err != nil {
		t.Fatal(err)
	}
	if st.NodesKept != 1 || st.NodesRemoved != 2 {
		t.Fatalf("kept=%d removed=%d, want 1/2", st.NodesKept, st.NodesRemoved)
	}
	if keys := db.SortedKeys(); len(keys) != 1 || keys[0] != "a" {
		t.Fatalf("keys = %v, want [a]", keys)
	}
	// b 的最新状态是墓碑，因此新读者查 b 为 NotFound。
	if err := db.View(func(r *vrec.Reader) error {
		if _, err := r.Get("b"); !errors.Is(err, vrec.ErrNotFound) {
			return fmt.Errorf("want not found, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedCloseAndReentrancy(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()

	r, _ := db.BeginReader()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); !errors.Is(err, vrec.ErrTxnClosed) {
		t.Fatalf("second reader close = %v, want ErrTxnClosed", err)
	}
	if _, err := r.Get("a"); !errors.Is(err, vrec.ErrTxnClosed) {
		t.Fatalf("get after close = %v, want ErrTxnClosed", err)
	}

	tx, _ := db.Begin()
	if err := tx.Put("a", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); !errors.Is(err, vrec.ErrTxnClosed) {
		t.Fatalf("second rollback = %v, want ErrTxnClosed", err)
	}
	if err := tx.Put("a", []byte("2")); !errors.Is(err, vrec.ErrTxnClosed) {
		t.Fatalf("put after rollback = %v, want ErrTxnClosed", err)
	}

	tx2, _ := db.Begin()
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); !errors.Is(err, vrec.ErrTxnClosed) {
		t.Fatalf("second commit = %v, want ErrTxnClosed", err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); !errors.Is(err, vrec.ErrClosed) {
		t.Fatalf("second db close = %v, want ErrClosed", err)
	}
}

func TestSingleWriterRejectsSecondTxn(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()

	tx, _ := db.Begin()
	defer tx.Rollback()
	if _, err := db.Begin(); !errors.Is(err, vrec.ErrTxnReentrant) {
		t.Fatalf("second Begin = %v, want ErrTxnReentrant", err)
	}
}

func TestSecondWriterRejectedWhileFirstActive(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()

	tx, _ := db.Begin()
	defer tx.Rollback()
	// 第一个写事务未结束，第二个写事务（重入写者）必须被明确拒绝。
	if _, err := db.Begin(); !errors.Is(err, vrec.ErrTxnReentrant) {
		t.Fatalf("second Begin = %v, want ErrTxnReentrant", err)
	}
}

func TestLongReaderDoesNotBlockWrites(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()

	putOK(t, db, "a", "v0")
	r, _ := db.BeginReader()
	defer r.Close()

	done := make(chan int64, 10)
	go func() {
		for i := 1; i <= 10; i++ {
			done <- putVer(db, "a", fmt.Sprintf("v%d", i))
		}
	}()
	for i := 1; i <= 10; i++ {
		<-done
	}
	if db.HeadVersion() != 11 {
		t.Fatalf("head = %d, want 11", db.HeadVersion())
	}
	// 老读者始终读到 v0，期间从未被写阻塞。
	mustGet(t, r, "a", "v0")
}

func putVer(db *vrec.DB, k, v string) int64 {
	if err := db.Update(func(tx *vrec.Txn) error {
		return tx.Put(k, []byte(v))
	}); err != nil {
		panic(err)
	}
	return db.HeadVersion()
}

func reopen(t *testing.T, dir string, hook vrec.FailHook) *vrec.DB {
	t.Helper()
	db, err := vrec.Open(dir, &vrec.Options{FailHook: hook})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return db
}

func TestDurabilityAndReopen(t *testing.T) {
	db, dir := mustOpen(t, nil)
	putOK(t, db, "a", "v1")
	putOK(t, db, "b", "v1")
	if err := db.Update(func(tx *vrec.Txn) error {
		tx.Put("a", []byte("v2"))
		return tx.Delete("b")
	}); err != nil {
		t.Fatal(err)
	}
	head := db.HeadVersion()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = reopen(t, dir, nil)
	defer db.Close()
	if db.HeadVersion() != head {
		t.Fatalf("head after reopen = %d, want %d", db.HeadVersion(), head)
	}
	if err := db.View(func(r *vrec.Reader) error {
		mustGet(t, r, "a", "v2")
		if _, err := r.Get("b"); !errors.Is(err, vrec.ErrNotFound) {
			return fmt.Errorf("b: want NotFound, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGCCompactedWALReplays(t *testing.T) {
	db, dir := mustOpen(t, nil)
	putOK(t, db, "a", "v1")
	putOK(t, db, "a", "v2")
	putOK(t, db, "a", "v3")
	if _, err := db.RunGC(); err != nil {
		t.Fatal(err)
	}
	putOK(t, db, "a", "v4")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = reopen(t, dir, nil)
	defer db.Close()
	if err := db.View(func(r *vrec.Reader) error {
		mustGet(t, r, "a", "v4")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := db.History("a")
	if len(h) != 2 || h[0].Version != 3 || h[1].Version != 4 {
		t.Fatalf("history after reopen = %+v, want v3,v4", h)
	}
}

// onceHook 在第 n 次命中指定 stage 时返回 err，之后不再触发。
type onceHook struct {
	stage string
	nth   int
	err   error
	calls int
}

func (h *onceHook) Hook(c vrec.FailContext) error {
	if c.Stage != h.stage {
		return nil
	}
	h.calls++ // 只对目标 stage 计数
	if h.calls == h.nth {
		return h.err
	}
	return nil
}

func TestFailpointTxnWriteAbortsCleanly(t *testing.T) {
	h := &onceHook{stage: vrec.StageTxnWrite, nth: 2, err: errors.New("disk stall")}
	db, _ := mustOpen(t, h.Hook)
	defer db.Close()
	putOK(t, db, "a", "v1")

	head := db.HeadVersion()
	err := db.Update(func(tx *vrec.Txn) error {
		return tx.Put("a", []byte("v2"))
	})
	if err == nil {
		t.Fatal("expected injected error")
	}
	if db.HeadVersion() != head {
		t.Fatalf("head advanced after pre-durable failure: %d", db.HeadVersion())
	}
	// 存储仍然健康：之后的写入正常。
	putOK(t, db, "a", "v3")
	if err := db.View(func(r *vrec.Reader) error {
		mustGet(t, r, "a", "v3")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFailpointTxnSyncSimulatedCrash(t *testing.T) {
	h := &onceHook{stage: vrec.StageTxnSync, nth: 2, err: vrec.ErrSimCrash}
	db, dir := mustOpen(t, h.Hook)
	putOK(t, db, "a", "v1")

	err := db.Update(func(tx *vrec.Txn) error {
		return tx.Put("a", []byte("v2"))
	})
	if !errors.Is(err, vrec.ErrSimCrash) {
		t.Fatalf("err = %v, want ErrSimCrash", err)
	}
	// “进程已死”：本实例拒绝继续服务。
	if err := db.View(func(r *vrec.Reader) error {
		_, err := r.Get("a")
		return err
	}); !errors.Is(err, vrec.ErrFaulted) {
		t.Fatalf("post-crash use = %v, want ErrFaulted", err)
	}
	walBefore := walSize(t, dir)
	_ = db.Close()

	// 重启：帧已 fsync，事务必须作为已提交出现。
	db = reopen(t, dir, nil)
	defer db.Close()
	if err := db.View(func(r *vrec.Reader) error {
		mustGet(t, r, "a", "v2")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if walSize(t, dir) != walBefore {
		t.Fatal("wal should not need truncation when frames synced")
	}
}

func TestFailpointGCStages(t *testing.T) {
	for _, stage := range []string{vrec.StageGCWrite, vrec.StageGCRename} {
		t.Run(stage, func(t *testing.T) {
			h := &onceHook{stage: stage, nth: 1, err: errors.New("io error")}
			db, dir := mustOpen(t, h.Hook)
			putOK(t, db, "a", "v1")
			putOK(t, db, "a", "v2")
			head := db.HeadVersion()

			if _, err := db.RunGC(); err == nil {
				t.Fatal("expected injected gc error")
			}
			// 原 WAL 完好、存储仍健康。
			if db.HeadVersion() != head {
				t.Fatal("head changed after aborted GC")
			}
			if err := db.View(func(r *vrec.Reader) error {
				mustGet(t, r, "a", "v2")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db = reopen(t, dir, nil)
			defer db.Close()
			if err := db.View(func(r *vrec.Reader) error {
				mustGet(t, r, "a", "v2")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// 中止后遗留的临时文件在 Open 时被清理。
			if _, err := os.Stat(filepath.Join(dir, "wal.log.compact.tmp")); !os.IsNotExist(err) {
				t.Fatalf("tmp file leaked: %v", err)
			}
		})
	}
}

func TestFailpointGCDirSyncReplaysEitherWay(t *testing.T) {
	h := &onceHook{stage: vrec.StageGCDirSync, nth: 1, err: vrec.ErrSimCrash}
	db, dir := mustOpen(t, h.Hook)
	putOK(t, db, "a", "v1")
	putOK(t, db, "a", "v2")
	if _, err := db.RunGC(); !errors.Is(err, vrec.ErrSimCrash) {
		t.Fatalf("gc err = %v, want ErrSimCrash", err)
	}
	_ = db.Close()

	// 无论崩溃落在 rename 前还是后，重放都给出最新值。
	db = reopen(t, dir, nil)
	defer db.Close()
	if err := db.View(func(r *vrec.Reader) error {
		mustGet(t, r, "a", "v2")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func walSize(t *testing.T, dir string) int64 {
	t.Helper()
	st, err := os.Stat(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

func TestTornTailIsTruncatedOnReopen(t *testing.T) {
	db, dir := mustOpen(t, nil)
	putOK(t, db, "a", "v1")
	putOK(t, db, "b", "v1")
	wal := filepath.Join(dir, "wal.log")
	goodSize := walSize(t, dir)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// 模拟断电：向完好日志末尾追加一段无法成帧的垃圾字节。
	f, err := os.OpenFile(wal, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{'V', 'R', 0x01, 0xff, 0xff, 0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	db = reopen(t, dir, nil)
	defer db.Close()
	if db.HeadVersion() != 2 {
		t.Fatalf("head = %d, want 2 (torn tail dropped)", db.HeadVersion())
	}
	if walSize(t, dir) != goodSize {
		t.Fatalf("wal size = %d, want truncated to %d", walSize(t, dir), goodSize)
	}
	if err := db.View(func(r *vrec.Reader) error {
		mustGet(t, r, "a", "v1")
		mustGet(t, r, "b", "v1")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 截断后日志恢复追加能力，新版本不覆盖旧数据。
	putOK(t, db, "c", "v1")
}

func TestTxnReadsOwnWritesAndRollback(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()
	putOK(t, db, "a", "committed")

	tx, _ := db.Begin()
	if err := tx.Put("a", []byte("dirty")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, tx, "a", "dirty")
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(r *vrec.Reader) error {
		mustGet(t, r, "a", "committed")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyKeyRejected(t *testing.T) {
	db, _ := mustOpen(t, nil)
	defer db.Close()
	if err := db.Update(func(tx *vrec.Txn) error {
		if err := tx.Put("", []byte("x")); !errors.Is(err, vrec.ErrEmptyKey) {
			t.Fatalf("put empty = %v", err)
		}
		if err := tx.Delete(""); !errors.Is(err, vrec.ErrEmptyKey) {
			t.Fatalf("delete empty = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
