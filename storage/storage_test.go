package storage

import (
	"os"
	"path/filepath"
	"testing"

	"aries/wal"
)

func tempDir(t *testing.T) string {
	dir := filepath.Join(os.TempDir(), "storagetest-"+t.Name())
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPageLSNPersistedInHeader(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	dm, err := OpenDiskManager(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()

	p := &Page{ID: 5, LSN: wal.LSN(12345)}
	copy(p.Data[DataOffset:], []byte("hello"))
	if err := dm.WritePage(p); err != nil {
		t.Fatal(err)
	}
	var q Page
	if err := dm.ReadPage(5, &q); err != nil {
		t.Fatal(err)
	}
	if q.LSN != 12345 {
		t.Fatalf("pageLSN = %d, want 12345", q.LSN)
	}
	if got := string(q.Data[DataOffset : DataOffset+5]); got != "hello" {
		t.Fatalf("data = %q, want hello", got)
	}
	// Unwritten page reads as zeros with LSN 0.
	var z Page
	if err := dm.ReadPage(99, &z); err != nil {
		t.Fatal(err)
	}
	if z.LSN != 0 || z.Data[DataOffset] != 0 {
		t.Fatalf("fresh page not zeroed: LSN=%d", z.LSN)
	}
}

func TestBufferPoolHitMissAndEviction(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	dm, err := OpenDiskManager(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()
	bp := NewBufferPool(dm, nil, 2, nil)

	// Miss on first fetch, hit on second.
	p0, err := bp.FetchPage(0)
	if err != nil {
		t.Fatal(err)
	}
	copy(p0.Data[DataOffset:], []byte("page-zero"))
	p0.LSN = 10
	bp.UnpinPage(0, true)
	if _, err := bp.FetchPage(0); err != nil {
		t.Fatal(err)
	}
	bp.UnpinPage(0, false)
	if bp.Hits() != 1 || bp.Misses() != 1 {
		t.Fatalf("hits=%d misses=%d, want 1/1", bp.Hits(), bp.Misses())
	}
	if bp.HitRate() != 0.5 {
		t.Fatalf("hit rate = %v, want 0.5", bp.HitRate())
	}

	// Fill the pool, then force eviction of the dirty page 0.
	p1, err := bp.FetchPage(1)
	if err != nil {
		t.Fatal(err)
	}
	copy(p1.Data[DataOffset:], []byte("page-one"))
	p1.LSN = 20
	bp.UnpinPage(1, true)
	if _, err := bp.FetchPage(2); err != nil { // evicts page 0 (dirty -> flushed)
		t.Fatal(err)
	}
	bp.UnpinPage(2, false)

	// Page 0 must have been written to disk by the eviction.
	var disk Page
	if err := dm.ReadPage(0, &disk); err != nil {
		t.Fatal(err)
	}
	if got := string(disk.Data[DataOffset : DataOffset+9]); got != "page-zero" {
		t.Fatalf("evicted page on disk = %q, want page-zero", got)
	}
	if disk.LSN != 10 {
		t.Fatalf("evicted pageLSN = %d, want 10", disk.LSN)
	}
}

func TestBufferPoolAllPinned(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	dm, err := OpenDiskManager(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()
	bp := NewBufferPool(dm, nil, 1, nil)
	if _, err := bp.FetchPage(0); err != nil {
		t.Fatal(err)
	}
	// Page 0 still pinned: no evictable frame.
	if _, err := bp.FetchPage(1); err != ErrNoFreeFrame {
		t.Fatalf("err = %v, want ErrNoFreeFrame", err)
	}
	bp.UnpinPage(0, false)
	if _, err := bp.FetchPage(1); err != nil {
		t.Fatal(err)
	}
}

func TestWALFirstRule(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	dm, err := OpenDiskManager(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()
	lm, err := wal.Open(filepath.Join(dir, "test.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer lm.Close()
	bp := NewBufferPool(dm, lm, 4, nil)

	// Dirty a page and stamp it with an LSN beyond the flushed watermark.
	page, err := bp.FetchPage(0)
	if err != nil {
		t.Fatal(err)
	}
	lsn, err := lm.Append(&wal.LogRecord{TxnID: 1, Type: wal.TypeUpdate, PageID: 0, After: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	copy(page.Data[DataOffset:], []byte("x"))
	page.LSN = lsn
	bp.UnpinPage(0, true)
	if lm.FlushedLSN() > lsn {
		t.Fatalf("test setup: log already flushed past %d", lsn)
	}
	// Flushing the page must first flush the WAL past the page's LSN.
	if err := bp.FlushPage(0); err != nil {
		t.Fatal(err)
	}
	if lm.FlushedLSN() <= lsn {
		t.Fatalf("WAL-first violated: flushedLSN=%d pageLSN=%d", lm.FlushedLSN(), lsn)
	}
}

func TestDirtyPagesSnapshot(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	dm, err := OpenDiskManager(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()
	bp := NewBufferPool(dm, nil, 8, nil)

	if dpt := bp.DirtyPages(); len(dpt) != 0 {
		t.Fatalf("clean pool has DPT %v", dpt)
	}
	p1, _ := bp.FetchPage(1)
	p1.LSN = 100
	bp.UnpinPage(1, true)
	p2, _ := bp.FetchPage(2)
	p2.LSN = 200
	bp.UnpinPage(2, true)
	// Re-dirty page 1: recLSN must stay at the first dirtying LSN.
	p1b, _ := bp.FetchPage(1)
	p1b.LSN = 150
	bp.UnpinPage(1, true)

	dpt := bp.DirtyPages()
	if len(dpt) != 2 || dpt[1] != 100 || dpt[2] != 200 {
		t.Fatalf("DPT = %v, want {1:100, 2:200}", dpt)
	}
	if err := bp.FlushPage(1); err != nil {
		t.Fatal(err)
	}
	dpt = bp.DirtyPages()
	if len(dpt) != 1 || dpt[2] != 200 {
		t.Fatalf("DPT after flush = %v, want {2:200}", dpt)
	}
}
