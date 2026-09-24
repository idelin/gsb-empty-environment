package pagedb

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db, dir
}

func reopen(t *testing.T, db *DB, dir string) *DB {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	ndb, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return ndb
}

func mustGet(t *testing.T, db *DB, pageID uint64) ([]byte, uint64) {
	t.Helper()
	data, ver, err := db.Get(pageID)
	if err != nil {
		t.Fatalf("Get(%d): %v", pageID, err)
	}
	return data, ver
}

// 已提交事务的数据可读，且带非零版本号。
func TestCommitAndRead(t *testing.T) {
	db, _ := openTemp(t)
	defer db.Close()

	tx, _ := db.Begin()
	if err := tx.Set(0, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Set(1, []byte("world")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	data, ver := mustGet(t, db, 0)
	if string(data) != "hello" || ver == 0 {
		t.Fatalf("page0 = %q v%d, want hello v>0", data, ver)
	}
	data, _ = mustGet(t, db, 1)
	if string(data) != "world" {
		t.Fatalf("page1 = %q, want world", data)
	}
}

// 中止的事务不留下任何可见数据。
func TestAbortNotVisible(t *testing.T) {
	db, dir := openTemp(t)
	tx, _ := db.Begin()
	tx.Set(0, []byte("aborted"))
	tx.Abort()
	db = reopen(t, db, dir)
	defer db.Close()

	data, ver := mustGet(t, db, 0)
	if data != nil || ver != 0 {
		t.Fatalf("aborted tx visible: %q v%d", data, ver)
	}
}

// 提交前崩溃（无 COMMIT 记录）：半个事务不暴露。
func TestCrashBeforeCommitNotVisible(t *testing.T) {
	db, dir := openTemp(t)

	tx1, _ := db.Begin()
	tx1.Set(0, []byte("committed"))
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}

	// tx2 写了 WAL 的 UPDATE 记录但未来得及提交，进程崩溃。
	tx2, _ := db.Begin()
	tx2.Set(0, []byte("HALF"))
	tx2.Set(1, []byte("HALF"))

	db = reopen(t, db, dir) // 模拟重启：恢复只重放有 COMMIT 的事务
	defer db.Close()

	data, _ := mustGet(t, db, 0)
	if string(data) != "committed" {
		t.Fatalf("page0 = %q, want committed (half tx leaked)", data)
	}
	data, ver := mustGet(t, db, 1)
	if data != nil || ver != 0 {
		t.Fatalf("page1 = %q v%d, want absent", data, ver)
	}
}

// 提交点之后、页刷盘之前崩溃：恢复时整体重放，数据不丢。
func TestCrashAfterCommitPoint(t *testing.T) {
	db, dir := openTemp(t)
	db.Fail.AfterWALSyncBeforePageFlush = func() error {
		return errors.New("injected crash")
	}

	tx, _ := db.Begin()
	tx.Set(0, []byte("durable"))
	tx.Set(1, []byte("data"))
	if err := tx.Commit(); err == nil {
		t.Fatal("Commit should report injected crash")
	}

	db = reopen(t, db, dir)
	defer db.Close()

	data, _ := mustGet(t, db, 0)
	if string(data) != "durable" {
		t.Fatalf("page0 = %q, want durable (replay failed)", data)
	}
	data, _ = mustGet(t, db, 1)
	if string(data) != "data" {
		t.Fatalf("page1 = %q, want data", data)
	}
}

// 恢复重放是幂等的：恢复两次，页文件字节与读结果完全一致。
func TestReplayIdempotent(t *testing.T) {
	db, dir := openTemp(t)
	db.Fail.AfterWALSyncBeforePageFlush = func() error {
		return errors.New("injected crash")
	}
	tx, _ := db.Begin()
	tx.Set(0, []byte("v1-data"))
	tx.Set(2, []byte("other"))
	tx.Commit() // 注入崩溃：页未刷盘
	db.Close()

	pagesPath := filepath.Join(dir, pagesFileName)

	db1, err := Open(dir) // 第一次恢复：重放
	if err != nil {
		t.Fatal(err)
	}
	d0a, v0a := mustGet(t, db1, 0)
	d2a, v2a := mustGet(t, db1, 2)
	raw1, _ := os.ReadFile(pagesPath)
	db1.Close()

	db2, err := Open(dir) // 第二次恢复：版本相同，跳过重放
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	d0b, v0b := mustGet(t, db2, 0)
	d2b, v2b := mustGet(t, db2, 2)
	raw2, _ := os.ReadFile(pagesPath)

	if !bytes.Equal(d0a, d0b) || v0a != v0b || !bytes.Equal(d2a, d2b) || v2a != v2b {
		t.Fatalf("replay not idempotent: (%q v%d,%q v%d) vs (%q v%d,%q v%d)",
			d0a, v0a, d2a, v2a, d0b, v0b, d2b, v2b)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Fatal("page file changed after second recovery")
	}
}

// 残缺日志尾（撕裂写）可识别：截断后继续正常工作。
func TestTornTail(t *testing.T) {
	db, dir := openTemp(t)
	tx, _ := db.Begin()
	tx.Set(0, []byte("before-tear"))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// 模拟断电：WAL 末尾留下半条记录。
	walPath := filepath.Join(dir, walFileName)
	f, err := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	partial := encodeRecord(record{typ: recUpdate, lsn: 99, txid: 99, pageID: 0, data: []byte("torn")})
	if _, err := f.Write(partial[:len(partial)/2]); err != nil {
		t.Fatal(err)
	}
	f.Close()

	db, err = Open(dir) // 应识别撕裂尾并截断，而不是报错
	if err != nil {
		t.Fatalf("Open with torn tail: %v", err)
	}
	defer db.Close()
	data, _ := mustGet(t, db, 0)
	if string(data) != "before-tear" {
		t.Fatalf("page0 = %q, want before-tear", data)
	}

	// 截断后 WAL 仍可继续追加。
	tx, _ = db.Begin()
	tx.Set(1, []byte("after-tear"))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db = reopen(t, db, dir)
	defer db.Close()
	data, _ = mustGet(t, db, 0)
	if string(data) != "before-tear" {
		t.Fatalf("page0 = %q after reopen", data)
	}
	data, _ = mustGet(t, db, 1)
	if string(data) != "after-tear" {
		t.Fatalf("page1 = %q after reopen", data)
	}
}

// WAL 中间损坏（后面还有合法记录）必须报错，不能当作可忽略尾部。
func TestMiddleCorruption(t *testing.T) {
	db, dir := openTemp(t)
	tx, _ := db.Begin()
	tx.Set(0, bytes.Repeat([]byte("A"), 100))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, _ = db.Begin()
	tx.Set(1, []byte("second-tx"))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// 翻转第一条记录数据区的一个字节（偏移 40 落在首条记录的数据内）。
	walPath := filepath.Join(dir, walFileName)
	raw, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[40] ^= 0xFF
	if err := os.WriteFile(walPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = Open(dir)
	if !errors.Is(err, ErrMiddleCorruption) {
		t.Fatalf("Open err = %v, want ErrMiddleCorruption", err)
	}
}

// 页校验和能发现页文件损坏；重启后恢复用 WAL 重建该页。
func TestPageChecksumAndRepair(t *testing.T) {
	db, dir := openTemp(t)
	tx, _ := db.Begin()
	tx.Set(0, []byte("checksummed"))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// 直接写坏页文件（模拟断电撕裂页写/介质错误）。
	pagesPath := filepath.Join(dir, pagesFileName)
	f, err := os.OpenFile(pagesPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xDE, 0xAD}, 100); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, _, err := db.Get(0); !errors.Is(err, ErrPageCorrupt) {
		t.Fatalf("Get err = %v, want ErrPageCorrupt", err)
	}

	db = reopen(t, db, dir) // 恢复时页版本按 0 处理，从 WAL 重建
	defer db.Close()
	data, _ := mustGet(t, db, 0)
	if string(data) != "checksummed" {
		t.Fatalf("page0 = %q after repair, want checksummed", data)
	}
}

// 页版本随提交单调递增，且等于写入它的日志 LSN 的相对顺序。
func TestVersionMonotonic(t *testing.T) {
	db, _ := openTemp(t)
	defer db.Close()

	tx, _ := db.Begin()
	tx.Set(0, []byte("v1"))
	tx.Commit()
	_, v1 := mustGet(t, db, 0)

	tx, _ = db.Begin()
	tx.Set(0, []byte("v2"))
	tx.Commit()
	data, v2 := mustGet(t, db, 0)

	if v2 <= v1 {
		t.Fatalf("version not monotonic: v1=%d v2=%d", v1, v2)
	}
	if string(data) != "v2" {
		t.Fatalf("page0 = %q, want v2", data)
	}
}

// 提交后崩溃恢复，未提交的新事务不影响已提交数据（混合场景）。
func TestMixedCommittedAndUncommitted(t *testing.T) {
	db, dir := openTemp(t)

	tx1, _ := db.Begin()
	tx1.Set(0, []byte("tx1"))
	tx1.Commit()

	tx2, _ := db.Begin()
	tx2.Set(0, []byte("tx2-uncommitted"))
	tx2.Set(5, []byte("tx2-page5"))
	// 不提交，直接崩溃。
	db = reopen(t, db, dir)
	defer db.Close()

	data, _ := mustGet(t, db, 0)
	if string(data) != "tx1" {
		t.Fatalf("page0 = %q, want tx1", data)
	}
	data, _ = mustGet(t, db, 5)
	if data != nil {
		t.Fatalf("page5 = %q, want absent", data)
	}
}
