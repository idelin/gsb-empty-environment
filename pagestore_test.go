package aries

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestPageStoreApplyBounds(t *testing.T) {
	ps, err := OpenPageStore(filepath.Join(t.TempDir(), "db.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.ApplyUpdate(1, PageSize-4, []byte("1234"), 10); err != nil {
		t.Fatalf("edge-of-page write should fit: %v", err)
	}
	if err := ps.ApplyUpdate(1, PageSize-3, []byte("1234"), 11); !errors.Is(err, ErrPageOverflow) {
		t.Fatalf("overflowing write: %v", err)
	}
	if err := ps.ApplyUpdate(1, PageSize, []byte("x"), 12); !errors.Is(err, ErrPageOverflow) {
		t.Fatalf("write past end: %v", err)
	}
	if _, err := ps.ReadAt(1, PageSize-2, 4); !errors.Is(err, ErrPageOverflow) {
		t.Fatalf("read past end: %v", err)
	}
}

func TestPageStorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.dat")
	ps, err := OpenPageStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.ApplyUpdate(1, 0, []byte("hello-page-1"), 100); err != nil {
		t.Fatal(err)
	}
	if err := ps.ApplyUpdate(2, 128, []byte("hello-page-2"), 200); err != nil {
		t.Fatal(err)
	}
	if ps.DirtyCount() != 2 {
		t.Fatalf("dirty count %d, want 2", ps.DirtyCount())
	}
	if err := ps.Flush(); err != nil {
		t.Fatal(err)
	}
	if ps.DirtyCount() != 0 {
		t.Fatalf("dirty count after flush %d, want 0", ps.DirtyCount())
	}
	ps.Close()

	reopened, err := OpenPageStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.PageLSN(1); got != 100 {
		t.Fatalf("page 1 LSN %d, want 100", got)
	}
	if got := reopened.PageLSN(2); got != 200 {
		t.Fatalf("page 2 LSN %d, want 200", got)
	}
	data, err := reopened.ReadAt(1, 0, 12)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("hello-page-1")) {
		t.Fatalf("page 1 data %q", data)
	}
	data, err = reopened.ReadAt(2, 128, 12)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("hello-page-2")) {
		t.Fatalf("page 2 data %q", data)
	}
}

// TestFlushPagePartial: only the flushed page survives a "crash" (reopen
// without Flush); unflushed pages are lost, as on a real crash.
func TestFlushPagePartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.dat")
	ps, err := OpenPageStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ps.ApplyUpdate(1, 0, []byte("persisted"), 10)
	ps.ApplyUpdate(2, 0, []byte("volatile"), 20)
	if err := ps.FlushPage(1); err != nil {
		t.Fatal(err)
	}
	// crash: discard without Flush

	reopened, err := OpenPageStore(path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := reopened.ReadAt(1, 0, 9)
	if !bytes.Equal(data, []byte("persisted")) {
		t.Fatalf("flushed page lost: %q", data)
	}
	data, _ = reopened.ReadAt(2, 0, 8)
	if !bytes.Equal(data, make([]byte, 8)) {
		t.Fatalf("unflushed page should be zeros, got %q", data)
	}
	if got := reopened.PageLSN(2); got != 0 {
		t.Fatalf("unflushed page LSN %d, want 0", got)
	}
}

func TestPageStoreConcurrent(t *testing.T) {
	ps, err := OpenPageStore(filepath.Join(t.TempDir(), "db.dat"))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			page := uint32(g)
			for i := 0; i < 200; i++ {
				if err := ps.ApplyUpdate(page, uint32(i%64), []byte{byte(g), byte(i)}, uint64(i+1)); err != nil {
					t.Errorf("apply: %v", err)
					return
				}
				if _, err := ps.ReadAt(page, uint32(i%64), 2); err != nil {
					t.Errorf("read: %v", err)
					return
				}
				_ = ps.PageLSN(page)
			}
		}(g)
	}
	wg.Wait()
	if err := ps.Flush(); err != nil {
		t.Fatal(err)
	}
	if ps.DirtyCount() != 0 {
		t.Fatalf("dirty pages remain after flush")
	}
}
