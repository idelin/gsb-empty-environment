package compactidx

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newLoadedIndex(t *testing.T, kvs []KV) *Index {
	t.Helper()
	idx := NewIndex()
	mustLoad(t, idx, mustBuild(t, kvs, 0))
	return idx
}

func TestQueryAfterClose(t *testing.T) {
	idx := newLoadedIndex(t, sampleKVs(8))

	v, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	v.Close()
	if _, _, err := v.Get("k0000"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get on closed view = %v, want ErrClosed", err)
	}
	if _, err := v.Cursor(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Cursor on closed view = %v, want ErrClosed", err)
	}

	v2, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	c, err := v2.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}
	idx.Close()
	if _, _, err := v2.Get("k0000"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after index close = %v, want ErrClosed", err)
	}
	if c.Next() || !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("cursor after index close: Next=%v Err=%v, want ErrClosed", c.Next() != false, c.Err())
	}
	if _, err := idx.View(); !errors.Is(err, ErrClosed) {
		t.Fatalf("View after close = %v, want ErrClosed", err)
	}
	c.Close()
	v2.Close()
}

func TestCloseOneViewKeepsOthers(t *testing.T) {
	idx := newLoadedIndex(t, sampleKVs(8))
	defer idx.Close()

	v1, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	v2, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	v1.Close()
	if got := idx.Unmapped(); got != 0 {
		t.Fatalf("Unmapped = %d after closing one of two views, want 0", got)
	}
	got, found, err := v2.Get("k0003")
	if err != nil || !found || string(got) != "v0003" {
		t.Fatalf("surviving view Get = %q,%v,%v", got, found, err)
	}
	c, err := v2.Cursor()
	if err != nil {
		t.Fatalf("Cursor on surviving view: %v", err)
	}
	n := 0
	for c.Next() {
		n++
	}
	if n != 8 || c.Err() != nil {
		t.Fatalf("surviving cursor iterated %d entries, err=%v", n, c.Err())
	}
	c.Close()
	v2.Close()
}

func TestCloseDeferredUntilLastBorrow(t *testing.T) {
	idx := newLoadedIndex(t, sampleKVs(8))

	v, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	c, err := v.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}

	// Controlled interleaving: the borrower releases only after main has
	// requested the close and observed the mapping is still alive.
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		<-release
		c.Close()
		v.Close()
		close(done)
	}()

	idx.Close()
	if got := idx.Unmapped(); got != 0 {
		t.Fatalf("Unmapped = %d while borrows outstanding, want 0 (close must be deferred)", got)
	}
	close(release)
	<-done
	if got := idx.Unmapped(); got != 1 {
		t.Fatalf("Unmapped = %d after last borrow released, want 1", got)
	}
}

func TestOldHandleKeepsOldGeneration(t *testing.T) {
	img1 := mustBuild(t, []KV{{Key: "k", Value: "old"}}, 0)
	img2 := mustBuild(t, []KV{{Key: "k", Value: "new"}}, 0)

	idx := NewIndex()
	mustLoad(t, idx, img1)
	oldView, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	mustLoad(t, idx, img2)
	if got := idx.Unmapped(); got != 0 {
		t.Fatalf("old mapping unmapped with view outstanding: %d", got)
	}
	newView, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if oldView.Generation() != 1 || newView.Generation() != 2 {
		t.Fatalf("generations = %d/%d, want 1/2", oldView.Generation(), newView.Generation())
	}

	// The old handle must not be rebound to the new mapping.
	got, found, err := oldView.Get("k")
	if err != nil || !found || string(got) != "old" {
		t.Fatalf("old view Get = %q,%v,%v, want \"old\"", got, found, err)
	}
	got, found, err = newView.Get("k")
	if err != nil || !found || string(got) != "new" {
		t.Fatalf("new view Get = %q,%v,%v, want \"new\"", got, found, err)
	}

	// The old view can still open cursors on its own (leased) generation.
	c, err := oldView.Cursor()
	if err != nil {
		t.Fatalf("old view Cursor: %v", err)
	}
	if !c.Next() || c.Key() != "k" || string(c.Value()) != "old" {
		t.Fatalf("old cursor = %q/%q", c.Key(), c.Value())
	}
	c.Close()

	oldView.Close()
	if got := idx.Unmapped(); got != 1 {
		t.Fatalf("Unmapped = %d after old view released, want 1", got)
	}
	newView.Close()
	idx.Close()
	if got := idx.Unmapped(); got != 2 {
		t.Fatalf("Unmapped = %d after close, want 2", got)
	}
}

func TestFailedLoadKeepsOldMapping(t *testing.T) {
	idx := newLoadedIndex(t, []KV{{Key: "k", Value: "v1"}})
	v, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	defer v.Close()

	corrupt := mustBuild(t, []KV{{Key: "k", Value: "v2"}}, 0)
	corrupt[0] = 'X'
	if err := idx.Load(corrupt); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("Load(corrupt) = %v, want ErrBadMagic", err)
	}
	if got := idx.Generation(); got != 1 {
		t.Fatalf("Generation = %d after failed load, want 1", got)
	}
	got, found, err := v.Get("k")
	if err != nil || !found || string(got) != "v1" {
		t.Fatalf("Get after failed load = %q,%v,%v, want v1", got, found, err)
	}

	// A subsequent good load still works.
	mustLoad(t, idx, mustBuild(t, []KV{{Key: "k", Value: "v2"}}, 0))
	if got := idx.Generation(); got != 2 {
		t.Fatalf("Generation = %d, want 2", got)
	}
	idx.Close()
}

func TestControlledInterleaveQueryFailedLoadClose(t *testing.T) {
	img := mustBuild(t, sampleKVs(16), 0)
	idx := NewIndex()
	mustLoad(t, idx, img)

	v, err := idx.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	c, err := v.Cursor()
	if err != nil {
		t.Fatalf("Cursor: %v", err)
	}

	// Step 1: borrow held, iteration started.
	if !c.Next() || c.Key() != "k0000" {
		t.Fatalf("first entry = %q", c.Key())
	}
	// Step 2: a failed load interleaved between queries must not disturb
	// the in-flight cursor.
	bad := mustBuild(t, sampleKVs(16), 0)
	bad[0] = 'X'
	if err := idx.Load(bad); err == nil {
		t.Fatal("Load(corrupt) succeeded")
	}
	if !c.Next() || c.Key() != "k0001" {
		t.Fatalf("entry after failed load = %q", c.Key())
	}
	// Step 3: a good load swaps generations under the cursor; it keeps
	// reading its own lease.
	mustLoad(t, idx, mustBuild(t, sampleKVs(16), 0))
	if !c.Next() || c.Key() != "k0002" {
		t.Fatalf("entry after reload = %q", c.Key())
	}
	// Step 4: close requested mid-iteration; further reads fail, but the
	// mapping is released only when the last borrow goes away.
	idx.Close()
	if c.Next() || !errors.Is(c.Err(), ErrClosed) {
		t.Fatalf("Next after close: Err=%v, want ErrClosed", c.Err())
	}
	if got := idx.Unmapped(); got != 1 {
		t.Fatalf("Unmapped = %d, want 1 (only the replaced generation)", got)
	}
	c.Close()
	v.Close()
	if got := idx.Unmapped(); got != 2 {
		t.Fatalf("Unmapped = %d after borrows released, want 2", got)
	}
}

func TestConcurrentCursorsShareLease(t *testing.T) {
	const entries = 500
	kvs := sampleKVs(entries)
	idx := newLoadedIndex(t, kvs)

	const workers = 8
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			v, err := idx.View()
			if errors.Is(err, ErrClosed) {
				return
			}
			if err != nil {
				errs <- fmt.Errorf("worker %d view: %w", id, err)
				return
			}
			defer v.Close()
			c, err := v.Cursor()
			if errors.Is(err, ErrClosed) {
				return
			}
			if err != nil {
				errs <- fmt.Errorf("worker %d cursor: %w", id, err)
				return
			}
			defer c.Close()
			n := 0
			prev := ""
			for c.Next() {
				k := c.Key()
				if n > 0 && k <= prev {
					errs <- fmt.Errorf("worker %d: keys out of order: %q after %q", id, k, prev)
					return
				}
				prev = k
				want := "v" + k[1:]
				if string(c.Value()) != want {
					errs <- fmt.Errorf("worker %d: %q -> %q, want %q", id, k, c.Value(), want)
					return
				}
				n++
			}
			if err := c.Err(); err != nil && !errors.Is(err, ErrClosed) {
				errs <- fmt.Errorf("worker %d: cursor err %v", id, err)
				return
			}
			if c.Err() == nil && n != entries {
				errs <- fmt.Errorf("worker %d: iterated %d entries, want %d", id, n, entries)
			}
		}(w)
	}
	close(start)
	time.Sleep(3 * time.Millisecond) // let some cursors get mid-flight
	idx.Close()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := idx.Unmapped(); got != 1 {
		t.Fatalf("Unmapped = %d after all cursors drained, want 1", got)
	}
}
