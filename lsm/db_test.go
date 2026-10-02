package lsm

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "lsm-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func countSSTFiles(t *testing.T, dir string) int {
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, fi := range fis {
		if strings.HasSuffix(fi.Name(), ".sst") {
			n++
		}
	}
	return n
}

func TestFlushGetAndCompaction(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	db, err := Open(dir, Options{FlushThresholdBytes: 1024, CompactThreshold: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// 写入足够数据触发多次 flush 与至少一次 compaction。
	const n = 500
	for i := 0; i < n; i++ {
		key := []byte(fmt.Sprintf("key-%04d", i))
		val := []byte(fmt.Sprintf("value-%04d-%s", i, strings.Repeat("x", 16)))
		if err := db.Put(key, val); err != nil {
			t.Fatal(err)
		}
	}
	// 覆盖写一部分 key，验证 compaction 后新值胜出。
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%04d", i))
		if err := db.Put(key, []byte(fmt.Sprintf("updated-%04d", i))); err != nil {
			t.Fatal(err)
		}
	}

	sstCount, compacted := db.Stats()
	if compacted == 0 {
		t.Fatalf("expected compaction to have removed files, got 0")
	}
	if got := countSSTFiles(t, dir); got != sstCount {
		t.Fatalf("dir has %d sst files, db tracks %d", got, sstCount)
	}

	for i := 0; i < n; i++ {
		key := []byte(fmt.Sprintf("key-%04d", i))
		got, ok, err := db.Get(key)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("key %s missing", key)
		}
		want := fmt.Sprintf("value-%04d-%s", i, strings.Repeat("x", 16))
		if i < 100 {
			want = fmt.Sprintf("updated-%04d", i)
		}
		if string(got) != want {
			t.Fatalf("key %s: got %q want %q", key, got, want)
		}
	}
}

func TestManualCompactDeletesObsoleteFiles(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	db, err := Open(dir, Options{FlushThresholdBytes: 512, CompactThreshold: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for i := 0; i < 200; i++ {
		if err := db.Put([]byte(fmt.Sprintf("k%04d", i)), []byte(strings.Repeat("v", 32))); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := db.Stats()
	if before < 2 {
		t.Fatalf("expected multiple sstables before compact, got %d", before)
	}
	if err := db.Compact(); err != nil {
		t.Fatal(err)
	}
	after, compacted := db.Stats()
	if after != 1 {
		t.Fatalf("expected 1 sstable after compact, got %d", after)
	}
	if compacted != before {
		t.Fatalf("expected %d files removed, got %d", before, compacted)
	}
	if got := countSSTFiles(t, dir); got != 1 {
		t.Fatalf("expected 1 sst file on disk, got %d", got)
	}
}

func TestWALReplay(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	db, err := Open(dir, Options{FlushThresholdBytes: 1 << 30, CompactThreshold: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := db.Put([]byte(fmt.Sprintf("wal-key-%d", i)), []byte(fmt.Sprintf("wal-val-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := Open(dir, Options{FlushThresholdBytes: 1 << 30, CompactThreshold: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	for i := 0; i < 10; i++ {
		got, ok, err := db2.Get([]byte(fmt.Sprintf("wal-key-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if !ok || string(got) != fmt.Sprintf("wal-val-%d", i) {
			t.Fatalf("wal replay lost key %d: ok=%v val=%q", i, ok, got)
		}
	}
}

func TestSSTableRoundTrip(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.sst")

	entries := []Entry{
		{Key: []byte("a"), Value: []byte("1")},
		{Key: []byte("b"), Value: []byte("2")},
		{Key: []byte("c"), Value: []byte("")},
	}
	if err := WriteSSTable(path, entries); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSSTable(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(entries) {
		t.Fatalf("got %d entries, want %d", len(got), len(entries))
	}
	for i := range entries {
		if string(got[i].Key) != string(entries[i].Key) || string(got[i].Value) != string(entries[i].Value) {
			t.Fatalf("entry %d mismatch: %v vs %v", i, got[i], entries[i])
		}
	}

	// 损坏文件应被 CRC 检出。
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, 6); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := ReadSSTable(path); err == nil {
		t.Fatal("expected crc error on corrupted file")
	}
}
