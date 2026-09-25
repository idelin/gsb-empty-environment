package mvcc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func open(t *testing.T, dir string, opts Options) *Store {
	t.Helper()
	s, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func put(t *testing.T, s *Store, key, val string) {
	t.Helper()
	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Put(key, []byte(val)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func del(t *testing.T, s *Store, key string) {
	t.Helper()
	tx, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := tx.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func mustGet(t *testing.T, tx *ReadTx, key, want string) {
	t.Helper()
	got, ok, err := tx.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if !ok || string(got) != want {
		t.Fatalf("Get(%q) = %q, %v; want %q, true", key, got, ok, want)
	}
}

func mustMiss(t *testing.T, tx *ReadTx, key string) {
	t.Helper()
	got, ok, err := tx.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if ok {
		t.Fatalf("Get(%q) = %q, true; want miss", key, got)
	}
}

func TestPutGetAndVersioning(t *testing.T) {
	s := open(t, t.TempDir(), Options{})
	defer s.Close()

	put(t, s, "a", "v1")
	put(t, s, "a", "v2")
	if got := s.Version(); got != 2 {
		t.Fatalf("Version = %d; want 2", got)
	}
	r, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	mustGet(t, r, "a", "v2")
	mustMiss(t, r, "missing")
}

func TestOldReaderSeesOldValueAfterWrite(t *testing.T) {
	s := open(t, t.TempDir(), Options{})
	defer s.Close()

	put(t, s, "a", "old")
	r1, err := s.Snapshot() // 固定在版本 1
	if err != nil {
		t.Fatal(err)
	}
	defer r1.Close()

	put(t, s, "a", "new") // 长读不阻塞新写

	mustGet(t, r1, "a", "old") // 旧读者仍读旧值

	r2, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	mustGet(t, r2, "a", "new")
}

func TestDeleteCreatesTombstone(t *testing.T) {
	s := open(t, t.TempDir(), Options{})
	defer s.Close()

	put(t, s, "a", "v1")
	rOld, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer rOld.Close()

	del(t, s, "a")

	mustGet(t, rOld, "a", "v1") // 墓碑之前的读者仍可见

	rNew, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer rNew.Close()
	mustMiss(t, rNew, "a")
}

func TestGCBoundedByOldestReader(t *testing.T) {
	s := open(t, t.TempDir(), Options{})
	defer s.Close()

	put(t, s, "a", "v1")
	r1, err := s.Snapshot() // 固定在 v1，成为最老活跃读者
	if err != nil {
		t.Fatal(err)
	}

	put(t, s, "a", "v2")
	put(t, s, "a", "v3")

	// 长读不阻塞新写（上面两次写入已成功），但限制回收。
	freed, err := s.GC()
	if err != nil {
		t.Fatal(err)
	}
	if freed != 0 {
		t.Fatalf("GC freed %d entries while old reader active; want 0", freed)
	}
	mustGet(t, r1, "a", "v1")

	// 关闭旧读者后才可释放链前缀。
	if err := r1.Close(); err != nil {
		t.Fatal(err)
	}
	freed, err = s.GC()
	if err != nil {
		t.Fatal(err)
	}
	if freed != 2 {
		t.Fatalf("GC freed %d entries after reader closed; want 2", freed)
	}

	r, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	mustGet(t, r, "a", "v3")
}

func TestGCRemovesDeadTombstoneChain(t *testing.T) {
	s := open(t, t.TempDir(), Options{})
	defer s.Close()

	put(t, s, "a", "v1")
	del(t, s, "a")

	freed, err := s.GC() // 无活跃读者，整条链（值+墓碑）可回收
	if err != nil {
		t.Fatal(err)
	}
	if freed != 2 {
		t.Fatalf("GC freed %d; want 2 (value + tombstone)", freed)
	}
	r, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	mustMiss(t, r, "a")
}

func TestWriterReentry(t *testing.T) {
	s := open(t, t.TempDir(), Options{})
	defer s.Close()

	tx1, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(); !errors.Is(err, ErrWriterActive) {
		t.Fatalf("second Begin = %v; want ErrWriterActive", err)
	}
	if err := tx1.Put("k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}
	// 事务重入：已提交的事务禁止再写、再提交、再回滚。
	if err := tx1.Put("k", []byte("x")); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("Put after Commit = %v; want ErrTxClosed", err)
	}
	if err := tx1.Commit(); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("second Commit = %v; want ErrTxClosed", err)
	}
	if err := tx1.Rollback(); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("Rollback after Commit = %v; want ErrTxClosed", err)
	}

	// 上一个写事务结束后可以再次 Begin。
	tx2, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Rollback(); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("second Rollback = %v; want ErrTxClosed", err)
	}
	if err := tx2.Commit(); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("Commit after Rollback = %v; want ErrTxClosed", err)
	}
}

func TestReadTxDoubleClose(t *testing.T) {
	s := open(t, t.TempDir(), Options{})
	defer s.Close()

	put(t, s, "a", "v1")
	r, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// 重复关闭需明确报错。
	if err := r.Close(); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("second Close = %v; want ErrTxClosed", err)
	}
	// 关闭后读取同样报错。
	if _, _, err := r.Get("a"); !errors.Is(err, ErrTxClosed) {
		t.Fatalf("Get after Close = %v; want ErrTxClosed", err)
	}
}

func TestCrashRecoveryAfterSync(t *testing.T) {
	dir := t.TempDir()
	boom := errors.New("injected crash")

	// 注入点：fsync 完成、内存应用之前崩溃。
	s := open(t, dir, Options{
		SyncWrites: true,
		Failpoints: Failpoints{AfterSyncBeforeApply: func() error { return boom }},
	})
	tx, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Put("a", []byte("durable")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); !errors.Is(err, boom) {
		t.Fatalf("Commit = %v; want injected error", err)
	}
	s.Close() // 内存状态丢失，模拟进程退出

	// 重新打开：恢复流程重放日志，已 fsync 的事务应当找回。
	s2 := open(t, dir, Options{SyncWrites: true})
	defer s2.Close()
	r, err := s2.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	mustGet(t, r, "a", "durable")
	if got := s2.Version(); got != 1 {
		t.Fatalf("Version after recovery = %d; want 1", got)
	}
}

func TestCrashBeforeAppendLosesNothing(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, Options{})
	put(t, s, "a", "v1")
	s.Close()

	boom := errors.New("injected crash")
	s2 := open(t, dir, Options{
		Failpoints: Failpoints{BeforeAppend: func() error { return boom }},
	})
	tx, _ := s2.Begin()
	tx.Put("b", []byte("lost"))
	if err := tx.Commit(); !errors.Is(err, boom) {
		t.Fatalf("Commit = %v; want injected error", err)
	}
	s2.Close()

	s3 := open(t, dir, Options{})
	defer s3.Close()
	r, _ := s3.Snapshot()
	defer r.Close()
	mustGet(t, r, "a", "v1")
	mustMiss(t, r, "b") // 未落盘的事务不出现
}

func TestTornTailTruncatedOnRecovery(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, Options{SyncWrites: true})
	put(t, s, "a", "v1")
	s.Close()

	// 模拟断电：日志尾部追加半个撕裂帧。
	f, err := os.OpenFile(filepath.Join(dir, logFileName), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0, 0, 0, 42, 0xDE, 0xAD}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s2 := open(t, dir, Options{})
	defer s2.Close()
	r, _ := s2.Snapshot()
	defer r.Close()
	mustGet(t, r, "a", "v1")

	// 撕裂部分已被截断，后续写入正常。
	put(t, s2, "b", "v2")
	r2, _ := s2.Snapshot()
	defer r2.Close()
	mustGet(t, r2, "b", "v2")
}

func TestCompactReclaimsDisk(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, Options{SyncWrites: true})
	defer s.Close()

	for i := 0; i < 50; i++ {
		put(t, s, "a", fmt.Sprintf("v%d", i))
	}
	before, err := s.LogSize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GC(); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	after, err := s.LogSize()
	if err != nil {
		t.Fatal(err)
	}
	if after >= before {
		t.Fatalf("Compact did not shrink log: before=%d after=%d", before, after)
	}

	r, _ := s.Snapshot()
	mustGet(t, r, "a", "v49")
	r.Close()

	// 重启后状态依旧正确。
	s.Close()
	s2 := open(t, dir, Options{})
	defer s2.Close()
	r2, _ := s2.Snapshot()
	defer r2.Close()
	mustGet(t, r2, "a", "v49")
	if got := s2.Version(); got != 50 {
		t.Fatalf("Version = %d; want 50", got)
	}
}

func TestStoreDoubleClose(t *testing.T) {
	s := open(t, t.TempDir(), Options{})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("second Close = %v; want ErrStoreClosed", err)
	}
	if _, err := s.Begin(); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Begin after Close = %v; want ErrStoreClosed", err)
	}
	if _, err := s.Snapshot(); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Snapshot after Close = %v; want ErrStoreClosed", err)
	}
}

func TestCompactPreservesVersionWatermark(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir, Options{SyncWrites: true})
	put(t, s, "a", "v1")
	del(t, s, "a") // 版本 2：墓碑
	if _, err := s.GC(); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(); err != nil { // 所有条目被回收，日志只剩锚点帧
		t.Fatal(err)
	}
	s.Close()

	s2 := open(t, dir, Options{})
	defer s2.Close()
	if got := s2.Version(); got != 2 {
		t.Fatalf("Version after compact+reopen = %d; want 2 (水位线不得回退)", got)
	}
	put(t, s2, "b", "v3")
	if got := s2.Version(); got != 3 {
		t.Fatalf("Version after new write = %d; want 3", got)
	}
}
