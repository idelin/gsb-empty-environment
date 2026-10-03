package storage

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"
)

func openDisk(t *testing.T) *DiskStore {
	t.Helper()
	d, err := OpenDiskStore(filepath.Join(t.TempDir(), "pages.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestPageRoundTrip(t *testing.T) {
	d := openDisk(t)
	if lsn, data, err := d.ReadPage(7); err != nil || lsn != 0 || data != nil {
		t.Fatalf("unwritten page: lsn=%d data=%v err=%v", lsn, data, err)
	}
	if err := d.WritePage(7, 55, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	lsn, data, err := d.ReadPage(7)
	if err != nil || lsn != 55 || string(data) != "hello" {
		t.Fatalf("lsn=%d data=%q err=%v", lsn, data, err)
	}
	if got := d.PageLSN(7); got != 55 {
		t.Fatalf("PageLSN = %d", got)
	}
	if got := d.PageLSN(8); got != 0 {
		t.Fatalf("PageLSN(8) = %d", got)
	}
}

func TestWritePageOversize(t *testing.T) {
	d := openDisk(t)
	if err := d.WritePage(1, 1, make([]byte, PageSize+1)); err == nil {
		t.Fatal("expected oversize error")
	}
	if err := d.WritePage(1, 1, make([]byte, PageSize)); err != nil {
		t.Fatalf("full-page write should succeed: %v", err)
	}
}

func TestBufferPoolLifecycle(t *testing.T) {
	d := openDisk(t)
	p := NewBufferPool(d)
	if err := p.Apply(1, []byte("v1"), 10); err != nil {
		t.Fatal(err)
	}
	snap := p.Snapshot()
	if !snap[1].Dirty || snap[1].LSN != 10 {
		t.Fatalf("snapshot = %+v", snap[1])
	}
	if err := p.Flush(1); err != nil {
		t.Fatal(err)
	}
	snap = p.Snapshot()
	if snap[1].Dirty {
		t.Fatal("page still dirty after flush")
	}
	if got := d.PageLSN(1); got != 10 {
		t.Fatalf("disk pageLSN = %d", got)
	}
	// A fresh pool (post-crash) must observe the flushed contents.
	p2 := NewBufferPool(d)
	data, lsn, err := p2.Fetch(1)
	if err != nil || lsn != 10 || string(data) != "v1" {
		t.Fatalf("fetch = %q lsn=%d err=%v", data, lsn, err)
	}
}

func TestBufferPoolFlushAll(t *testing.T) {
	d := openDisk(t)
	p := NewBufferPool(d)
	for page := 1; page <= 3; page++ {
		if err := p.Apply(page, []byte{byte(page)}, uint64(page)); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.FlushAll(); err != nil {
		t.Fatal(err)
	}
	for page := 1; page <= 3; page++ {
		_, data, _ := d.ReadPage(page)
		if !bytes.Equal(data, []byte{byte(page)}) {
			t.Fatalf("page %d = %v", page, data)
		}
	}
	if snap := p.Snapshot(); snap[1].Dirty || snap[2].Dirty || snap[3].Dirty {
		t.Fatalf("dirty frames remain: %+v", snap)
	}
}

func TestApplyOversize(t *testing.T) {
	p := NewBufferPool(openDisk(t))
	if err := p.Apply(1, make([]byte, PageSize+1), 1); err == nil {
		t.Fatal("expected oversize error")
	}
}

func TestConcurrentPoolAccess(t *testing.T) {
	d := openDisk(t)
	p := NewBufferPool(d)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				page := (w*50 + i) % 16
				if err := p.Apply(page, []byte{byte(w)}, uint64(w*50+i+1)); err != nil {
					t.Error(err)
					return
				}
				if _, _, err := p.Fetch(page); err != nil {
					t.Error(err)
					return
				}
				_ = p.Snapshot()
			}
		}(w)
	}
	wg.Wait()
	if err := p.FlushAll(); err != nil {
		t.Fatal(err)
	}
}
