package kvdb

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func readAllSegments(t *testing.T, dir string) []byte {
	var buf bytes.Buffer
	for _, name := range segFiles(t, dir) {
		data, err := ioutil.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(data)
	}
	return buf.Bytes()
}

// 压缩只保留可见记录：被覆盖的旧值与墓碑彻底消失，末值保留。
func TestCompactionKeepsOnlyVisible(t *testing.T) {
	opts := &Options{MaxSegmentBytes: 128, SyncWrites: true}
	db, dir := openTemp(t, opts)
	defer os.RemoveAll(dir)
	defer db.Close()

	db.Put([]byte("k"), []byte("stale-v1"))
	db.Delete([]byte("k"))
	db.Put([]byte("k"), []byte("final-v2"))
	for i := 0; i < 50; i++ {
		db.Put([]byte(fmt.Sprintf("key-%02d", i)), []byte(fmt.Sprintf("val-%02d", i)))
	}
	for i := 0; i < 50; i += 2 {
		db.Delete([]byte(fmt.Sprintf("key-%02d", i)))
	}
	before := len(segFiles(t, dir))
	if before < 3 {
		t.Fatalf("test setup: want several segments, got %d", before)
	}

	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}

	mustGet(t, db, "k", "final-v2")
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("key-%02d", i)
		if i%2 == 0 {
			mustNotFound(t, db, key)
		} else {
			mustGet(t, db, key, fmt.Sprintf("val-%02d", i))
		}
	}

	if after := len(segFiles(t, dir)); after >= before {
		t.Fatalf("compaction did not shrink segments: before=%d after=%d", before, after)
	}
	raw := readAllSegments(t, dir)
	for _, gone := range []string{"stale-v1", "val-00", "val-02"} {
		if bytes.Contains(raw, []byte(gone)) {
			t.Fatalf("compacted store still contains invisible record %q", gone)
		}
	}
	if !bytes.Contains(raw, []byte("final-v2")) {
		t.Fatal("compacted store lost visible record final-v2")
	}
}

// 压缩在 MANIFEST 提交前崩溃：老布局不变，暂存文件被清理，数据无损。
func TestCompactInterruptBeforeCommit(t *testing.T) {
	opts := &Options{MaxSegmentBytes: 128, SyncWrites: true}
	db, dir := openTemp(t, opts)
	defer os.RemoveAll(dir)

	for i := 0; i < 40; i++ {
		db.Put([]byte(fmt.Sprintf("key-%02d", i)), []byte(fmt.Sprintf("val-%02d", i)))
	}
	crash := errors.New("injected: crash before compaction commit")
	db.opts.Injector = InjectorFunc(func(point string) error {
		if point == InjectCompactBeforeCommit {
			return crash
		}
		return nil
	})
	if err := db.Compact(); err != crash {
		t.Fatalf("got %v, want injected crash", err)
	}
	for i := 0; i < 40; i++ {
		mustGet(t, db, fmt.Sprintf("key-%02d", i), fmt.Sprintf("val-%02d", i))
	}
	db.Close()

	db2 := reopen(t, dir, opts)
	defer db2.Close()
	for i := 0; i < 40; i++ {
		mustGet(t, db2, fmt.Sprintf("key-%02d", i), fmt.Sprintf("val-%02d", i))
	}
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == compactSuffix {
			t.Fatalf("scratch file %s survived recovery", e.Name())
		}
	}
	// 恢复后再次压缩可以收敛。
	if err := db2.Compact(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		mustGet(t, db2, fmt.Sprintf("key-%02d", i), fmt.Sprintf("val-%02d", i))
	}
}

// 压缩在 MANIFEST 提交后、清扫旧段前崩溃：恢复时按新清单清理旧段。
func TestCompactInterruptAfterCommit(t *testing.T) {
	opts := &Options{MaxSegmentBytes: 128, SyncWrites: true}
	db, dir := openTemp(t, opts)
	defer os.RemoveAll(dir)

	for i := 0; i < 40; i++ {
		db.Put([]byte(fmt.Sprintf("key-%02d", i)), []byte(fmt.Sprintf("val-%02d", i)))
	}
	before := len(segFiles(t, dir))
	crash := errors.New("injected: crash after compaction commit")
	db.opts.Injector = InjectorFunc(func(point string) error {
		if point == InjectCompactAfterCommit {
			return crash
		}
		return nil
	})
	if err := db.Compact(); err != crash {
		t.Fatalf("got %v, want injected crash", err)
	}
	db.Close()

	db2 := reopen(t, dir, opts)
	defer db2.Close()
	for i := 0; i < 40; i++ {
		mustGet(t, db2, fmt.Sprintf("key-%02d", i), fmt.Sprintf("val-%02d", i))
	}
	if after := len(segFiles(t, dir)); after >= before {
		t.Fatalf("recovery did not reclaim old segments: before=%d after=%d", before, after)
	}
}

// 旧读者固定（pin）段句柄期间，压缩不得回收底层文件；读者结束后才回收。
func TestReaderPinsSegment(t *testing.T) {
	opts := &Options{MaxSegmentBytes: 128, SyncWrites: true}
	db, dir := openTemp(t, opts)
	defer os.RemoveAll(dir)
	defer db.Close()

	db.Put([]byte("pinned"), []byte("value"))
	for i := 0; i < 20; i++ {
		db.Put([]byte(fmt.Sprintf("filler-%02d", i)), []byte("xxxxxxxxxxxxxxxx"))
	}

	db.mu.RLock()
	loc := db.index["pinned"]
	seg := db.segments[loc.segID]
	db.mu.RUnlock()
	if seg == db.active {
		t.Fatal("test setup: pinned key should live in a sealed segment")
	}

	db.mu.Lock()
	seg.refs++ // simulate an in-flight reader holding the handle
	db.mu.Unlock()

	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(seg.path); err != nil {
		t.Fatalf("pinned segment reclaimed under reader: %v", err)
	}
	rec := make([]byte, loc.length)
	if _, err := seg.file.ReadAt(rec, loc.offset); err != nil {
		t.Fatalf("read from pinned segment: %v", err)
	}
	k, v, tomb, err := decodeRecord(rec)
	if err != nil || tomb || string(k) != "pinned" || string(v) != "value" {
		t.Fatalf("pinned read = %q %q tomb=%v err=%v", k, v, tomb, err)
	}
	mustGet(t, db, "pinned", "value") // new readers use the compacted segment

	db.mu.Lock()
	seg.refs--
	db.sweepLocked(seg)
	db.mu.Unlock()
	if _, err := os.Stat(seg.path); !os.IsNotExist(err) {
		t.Fatal("segment not reclaimed after last reader released it")
	}
}
