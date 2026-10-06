package cachemodel

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

// A write that lands while a writeback is in flight must survive the old
// writeback's completion.
func TestWritebackDoesNotClearConcurrentWrite(t *testing.T) {
	p := NewPage(8)
	d := NewDisk(8)

	p.Write(0, []byte("aaaa"))
	wb, ok := p.BeginWriteback()
	if !ok {
		t.Fatal("expected writeback to start")
	}
	p.Write(0, []byte("bbbb")) // re-dirty the same bytes at a newer version

	d.Write(0, wb.Data[0:4])
	p.CompleteWriteback(wb, []Interval{{0, 4}})

	if got := p.DirtyIntervals(); !reflect.DeepEqual(got, []Interval{{0, 4}}) {
		t.Fatalf("concurrent write was cleared, dirty=%v", got)
	}
	p.FlushAll(d)
	if !p.IsClean() {
		t.Fatal("page still dirty after flush")
	}
	want := append([]byte("bbbb"), make([]byte, 4)...)
	if !bytes.Equal(d.Bytes(), want) {
		t.Fatalf("disk = %q, want %q (newest data must win)", d.Bytes(), want)
	}
}

// A short write only clears the bytes it actually persisted, and only at the
// captured version.
func TestPartialWriteClearsOnlyWrittenOldBytes(t *testing.T) {
	p := NewPage(8)
	p.Write(0, []byte("abcdefgh")) // dirty [0,8) at v1

	wb, _ := p.BeginWriteback() // captures v1
	p.Write(2, []byte("XY"))    // [2,4) re-dirtied at v2

	// Short write: only [0,6) of the captured [0,8) made it to disk.
	p.CompleteWriteback(wb, []Interval{{0, 6}})

	// [0,2) and [4,6) were old-version and written -> cleared.
	// [2,4) was written but re-dirtied -> kept. [6,8) was never written -> kept.
	want := []Interval{{2, 4}, {6, 8}}
	if got := p.DirtyIntervals(); !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty=%v, want %v", got, want)
	}
}

// Evict must block while the page is dirty and a writeback is in flight, and
// return once the flush completes.
func TestEvictWaitsForWriteback(t *testing.T) {
	p := NewPage(8)
	d := NewDisk(8)
	p.Write(0, []byte("aaaa"))
	wb, _ := p.BeginWriteback()

	done := make(chan []byte, 1)
	go func() { done <- p.Evict() }()

	select {
	case <-done:
		t.Fatal("evict returned while page dirty and writeback in flight")
	case <-time.After(50 * time.Millisecond):
	}

	d.Write(wb.Dirty[0].Start, wb.Data[wb.Dirty[0].Start:wb.Dirty[0].End])
	p.CompleteWriteback(wb, wb.Dirty)

	select {
	case data := <-done:
		if !bytes.Equal(data[:4], []byte("aaaa")) {
			t.Fatalf("evicted data = %q", data[:4])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("evict did not return after page became clean")
	}
}

// Evict must keep waiting when a write re-dirties the page mid-writeback:
// the old writeback's completion is not enough.
func TestEvictWaitsForRedirtiedPage(t *testing.T) {
	p := NewPage(8)
	d := NewDisk(8)
	p.Write(0, []byte("aaaa"))
	wb, _ := p.BeginWriteback()

	done := make(chan []byte, 1)
	go func() { done <- p.Evict() }()

	p.Write(0, []byte("bbbb"))
	p.CompleteWriteback(wb, wb.Dirty) // completes, but page is dirty again

	select {
	case <-done:
		t.Fatal("evict returned although the page was re-dirtied")
	case <-time.After(50 * time.Millisecond):
	}

	p.FlushAll(d)
	select {
	case data := <-done:
		if !bytes.Equal(data[:4], []byte("bbbb")) {
			t.Fatalf("evicted data = %q, want newest write", data[:4])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("evict did not return after final flush")
	}
}

// Completing a writeback twice (or with a stale token) must not corrupt
// state.
func TestStaleWritebackPanics(t *testing.T) {
	p := NewPage(4)
	p.Write(0, []byte("aa"))
	wb, _ := p.BeginWriteback()
	p.CompleteWriteback(wb, wb.Dirty)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on stale writeback completion")
		}
	}()
	p.CompleteWriteback(wb, wb.Dirty)
}
