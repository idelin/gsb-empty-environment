package kvdb

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openTemp(t *testing.T, opts *Options) (*DB, string) {
	dir, err := ioutil.TempDir("", "kvdb-test")
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir, opts)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return db, dir
}

func reopen(t *testing.T, dir string, opts *Options) *DB {
	db, err := Open(dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func mustGet(t *testing.T, db *DB, key, want string) {
	v, err := db.Get([]byte(key))
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if string(v) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, v, want)
	}
}

func mustNotFound(t *testing.T, db *DB, key string) {
	if _, err := db.Get([]byte(key)); err != ErrNotFound {
		t.Fatalf("Get(%q) = %v, want ErrNotFound", key, err)
	}
}

func segFiles(t *testing.T, dir string) []string {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), segmentSuffix) {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestPutGetDelete(t *testing.T) {
	db, dir := openTemp(t, nil)
	defer os.RemoveAll(dir)
	defer db.Close()

	if err := db.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, db, "k", "v")
	if err := db.Delete([]byte("k")); err != nil {
		t.Fatal(err)
	}
	mustNotFound(t, db, "k")
	mustNotFound(t, db, "never-written")
	if err := db.Put(nil, []byte("v")); err != errEmptyKey {
		t.Fatalf("empty key: got %v", err)
	}
}

// 同键写-删-写后只能看到末值，重启后依然如此。
func TestWriteDeleteWriteSameKey(t *testing.T) {
	opts := &Options{SyncWrites: true}
	db, dir := openTemp(t, opts)
	defer os.RemoveAll(dir)

	db.Put([]byte("k"), []byte("first"))
	db.Delete([]byte("k"))
	db.Put([]byte("k"), []byte("last"))
	mustGet(t, db, "k", "last")
	db.Close()

	db2 := reopen(t, dir, opts)
	defer db2.Close()
	mustGet(t, db2, "k", "last")
}

// 不 Close 直接重开，模拟进程崩溃：已 fsync 的写必须恢复。
func TestCrashRecoveryWithoutClose(t *testing.T) {
	opts := &Options{SyncWrites: true}
	db, dir := openTemp(t, opts)
	defer os.RemoveAll(dir)

	db.Put([]byte("a"), []byte("1"))
	db.Put([]byte("b"), []byte("2"))
	db.Delete([]byte("a"))
	// no Close: process "crashes" here

	db2 := reopen(t, dir, opts)
	defer db2.Close()
	mustNotFound(t, db2, "a")
	mustGet(t, db2, "b", "2")
}

// 段尾被撕裂（断电只写了一半）时，启动扫描截断到最后一条完整记录。
func TestTornTailTruncated(t *testing.T) {
	opts := &Options{SyncWrites: true}
	db, dir := openTemp(t, opts)
	defer os.RemoveAll(dir)

	db.Put([]byte("k1"), []byte("v1"))
	db.Put([]byte("k2"), []byte("v2"))
	db.Close()

	segs := segFiles(t, dir)
	if len(segs) != 1 {
		t.Fatalf("want 1 segment, got %v", segs)
	}
	path := filepath.Join(dir, segs[0])
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	goodSize := st.Size()

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("torn-partial-record-without-crc")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	db2 := reopen(t, dir, opts)
	defer db2.Close()
	mustGet(t, db2, "k1", "v1")
	mustGet(t, db2, "k2", "v2")

	st, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != goodSize {
		t.Fatalf("torn tail not truncated: size %d, want %d", st.Size(), goodSize)
	}
}

// 超过 MaxSegmentBytes 触发段轮转，轮转后数据完整且可恢复。
func TestSegmentRotation(t *testing.T) {
	opts := &Options{MaxSegmentBytes: 64, SyncWrites: true}
	db, dir := openTemp(t, opts)
	defer os.RemoveAll(dir)

	for i := 0; i < 30; i++ {
		db.Put([]byte(fmt.Sprintf("key-%02d", i)), []byte(fmt.Sprintf("value-%02d", i)))
	}
	if n := len(segFiles(t, dir)); n < 2 {
		t.Fatalf("want rotation, got %d segment(s)", n)
	}
	db.Close()

	db2 := reopen(t, dir, opts)
	defer db2.Close()
	for i := 0; i < 30; i++ {
		mustGet(t, db2, fmt.Sprintf("key-%02d", i), fmt.Sprintf("value-%02d", i))
	}
}

// 恢复时清理孤儿文件：未提交的段、压缩临时文件、MANIFEST.tmp。
func TestRecoveryCleansOrphans(t *testing.T) {
	db, dir := openTemp(t, &Options{SyncWrites: true})
	defer os.RemoveAll(dir)
	db.Put([]byte("k"), []byte("v"))
	db.Close()

	ioutil.WriteFile(filepath.Join(dir, "000000.seg"), []byte("junk"), 0644)
	ioutil.WriteFile(filepath.Join(dir, "000099"+compactSuffix), []byte("junk"), 0644)
	ioutil.WriteFile(filepath.Join(dir, manifestTmp), []byte("junk"), 0644)

	db2 := reopen(t, dir, nil)
	defer db2.Close()
	mustGet(t, db2, "k", "v")
	for _, name := range []string{"000000.seg", "000099" + compactSuffix, manifestTmp} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("orphan %s not cleaned", name)
		}
	}
}

// 写路径故障注入：fsync 前注入错误，写操作返回该错误。
func TestFaultInjectionOnWritePath(t *testing.T) {
	boom := errors.New("injected: disk lost power before fsync")
	opts := &Options{
		SyncWrites: true,
		Injector: InjectorFunc(func(point string) error {
			if point == InjectBeforeAppendSync {
				return boom
			}
			return nil
		}),
	}
	db, dir := openTemp(t, opts)
	defer os.RemoveAll(dir)
	defer db.Close()

	if err := db.Put([]byte("k"), []byte("v")); err != boom {
		t.Fatalf("got %v, want injected error", err)
	}
}

// 并发读写删与压缩交错，验证无数据竞争、无恐慌，且重开后状态一致。
func TestConcurrentReadWriteCompact(t *testing.T) {
	db, dir := openTemp(t, &Options{MaxSegmentBytes: 512})
	defer os.RemoveAll(dir)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				key := []byte(fmt.Sprintf("key-%d", (id*7+i)%64))
				switch i % 3 {
				case 0:
					db.Put(key, []byte(fmt.Sprintf("w%d-i%d", id, i)))
				case 1:
					if _, err := db.Get(key); err != nil && err != ErrNotFound {
						t.Errorf("Get: %v", err)
					}
				case 2:
					db.Delete(key)
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			db.Compact()
		}
	}()
	wg.Wait()

	if err := db.Sync(); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db2 := reopen(t, dir, nil)
	defer db2.Close()
	for i := 0; i < 64; i++ {
		if _, err := db2.Get([]byte(fmt.Sprintf("key-%d", i))); err != nil && err != ErrNotFound {
			t.Fatalf("Get after reopen: %v", err)
		}
	}
}
