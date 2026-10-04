package writebacksim

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func hasPending(d *VersionedDisk, pageID, version uint64) bool {
	for _, comp := range d.Pending() {
		if comp.PageID == pageID && comp.Version == version {
			return true
		}
	}
	return false
}

func mustAlloc(t *testing.T, c *Cache, id uint64) *Page {
	t.Helper()
	p, err := c.Alloc(context.Background(), id)
	if err != nil {
		t.Fatalf("Alloc(%d): %v", id, err)
	}
	return p
}

func mustWrite(t *testing.T, c *Cache, p *Page, data string) {
	t.Helper()
	if err := c.Write(p, []byte(data)); err != nil {
		t.Fatalf("Write(%d): %v", p.ID(), err)
	}
}

// A page modified while its writeback is in flight must be re-dirtied and
// written back again; the first receipt only confirms the first version.
func TestRedirtyDuringWriteback(t *testing.T) {
	c := NewCache(4)
	d := c.Disk()
	d.Hold()

	p := mustAlloc(t, c, 1)
	mustWrite(t, c, p, "v1")

	waitFor(t, "v1 writeback in flight", func() bool { return hasPending(d, 1, 1) })
	if st, _ := c.State(1); st != StateWriteback {
		t.Fatalf("state = %v, want writeback", st)
	}

	// Modify the page while v1 is being written back.
	mustWrite(t, c, p, "v2")

	// The v1 receipt confirms only v1: the page must stay dirty and a
	// second writeback round must carry v2.
	d.ReleaseMatching(func(comp Completion) bool { return comp.Version == 1 })
	waitFor(t, "v2 writeback in flight", func() bool { return hasPending(d, 1, 2) })
	if _, acked, _ := c.Versions(1); acked != 1 {
		t.Fatalf("acked = %d, want 1", acked)
	}

	d.ReleaseAll()
	waitFor(t, "page clean", func() bool {
		st, _ := c.State(1)
		return st == StateClean
	})
	version, acked, _ := c.Versions(1)
	if version != 2 || acked != 2 {
		t.Fatalf("version=%d acked=%d, want 2/2", version, acked)
	}
	sv, data, ok := d.Stored(1)
	if !ok || sv != 2 || string(data) != "v2" {
		t.Fatalf("stored = v%d %q, want v2 %q", sv, data, "v2")
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// A late or duplicated receipt from an old writeback round must be ignored:
// it can neither clean the page nor rewind the acknowledged version.
func TestStaleReceiptIgnored(t *testing.T) {
	c := NewCache(4)
	d := c.Disk()

	p := mustAlloc(t, c, 1)
	mustWrite(t, c, p, "v1")
	waitFor(t, "v1 clean", func() bool {
		st, _ := c.State(1)
		return st == StateClean
	})

	d.Hold()
	mustWrite(t, c, p, "v2")
	waitFor(t, "v2 writeback in flight", func() bool { return hasPending(d, 1, 2) })

	// Straggler receipts from the previous round arrive late.
	d.Inject(Completion{PageID: 1, Version: 1})
	d.Inject(Completion{PageID: 1, Version: 1})
	// Give the cache a moment to (wrongly) process them if it would.
	time.Sleep(20 * time.Millisecond)

	version, acked, _ := c.Versions(1)
	if version != 2 || acked != 1 {
		t.Fatalf("version=%d acked=%d, want 2/1 (stale receipt leaked)", version, acked)
	}
	if st, _ := c.State(1); st != StateWriteback {
		t.Fatalf("state = %v, want writeback", st)
	}

	d.ReleaseAll()
	waitFor(t, "v2 clean", func() bool {
		st, _ := c.State(1)
		return st == StateClean
	})

	// A duplicated receipt for the just-completed round is also ignored.
	d.Inject(Completion{PageID: 1, Version: 2})
	time.Sleep(20 * time.Millisecond)
	if _, acked, _ := c.Versions(1); acked != 2 {
		t.Fatalf("acked = %d, want 2", acked)
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// A failed writeback leaves the page dirty and it is retried until the
// version is confirmed.
func TestWritebackFailureRetries(t *testing.T) {
	c := NewCache(4)
	c.retryBackoff = time.Millisecond
	d := c.Disk()
	d.FailNext(errors.New("simulated io error"), 2)

	p := mustAlloc(t, c, 1)
	mustWrite(t, c, p, "v1")

	waitFor(t, "page clean after retries", func() bool {
		st, _ := c.State(1)
		return st == StateClean
	})
	if got := d.Writes(); got != 3 {
		t.Fatalf("disk writes = %d, want 3 (2 failures + 1 success)", got)
	}
	_, acked, _ := c.Versions(1)
	if acked != 1 {
		t.Fatalf("acked = %d, want 1", acked)
	}
	sv, data, _ := d.Stored(1)
	if sv != 1 || string(data) != "v1" {
		t.Fatalf("stored = v%d %q, want v1", sv, data)
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// When the cache is full of dirty pages, allocation applies backpressure
// and honors cancellation; dirty pages are never dropped to make room.
func TestBackpressureAndCancel(t *testing.T) {
	c := NewCache(1)
	c.retryBackoff = time.Millisecond
	d := c.Disk()
	d.Hold()

	pa := mustAlloc(t, c, 1)
	mustWrite(t, c, pa, "a1")
	waitFor(t, "writeback in flight", func() bool { return hasPending(d, 1, 1) })

	// Non-blocking attempt fails immediately.
	if _, err := c.TryAlloc(2); err != ErrNoCleanPage {
		t.Fatalf("TryAlloc = %v, want ErrNoCleanPage", err)
	}
	// Blocking attempt times out: backpressure.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Alloc(ctx, 2); err != context.DeadlineExceeded {
		t.Fatalf("Alloc = %v, want DeadlineExceeded", err)
	}
	// Blocking attempt is canceled explicitly.
	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel2()
	}()
	if _, err := c.Alloc(ctx2, 3); err != context.Canceled {
		t.Fatalf("Alloc = %v, want Canceled", err)
	}

	// Complete the writeback: the page becomes clean and evictable, which
	// unblocks allocation.
	d.ReleaseAll()
	waitFor(t, "page clean", func() bool {
		st, _ := c.State(1)
		return st == StateClean
	})
	pb, err := c.Alloc(context.Background(), 2)
	if err != nil {
		t.Fatalf("Alloc after drain: %v", err)
	}
	if pb.ID() != 2 {
		t.Fatalf("got page %d, want 2", pb.ID())
	}

	// The evicted dirty page was written back, not dropped.
	sv, data, ok := d.Stored(1)
	if !ok || sv != 1 || string(data) != "a1" {
		t.Fatalf("stored = v%d %q (ok=%v), want v1 %q", sv, data, ok, "a1")
	}
	// The stale handle is rejected.
	if err := c.Write(pa, []byte("x")); err != ErrEvicted {
		t.Fatalf("Write on evicted page = %v, want ErrEvicted", err)
	}

	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Graceful close flushes everything; afterwards the durable image of every
// page is exactly its latest confirmed version.
func TestCloseFlushesLatestVersion(t *testing.T) {
	c := NewCache(8)
	c.retryBackoff = time.Millisecond
	d := c.Disk()
	d.Hold()

	pages := make(map[uint64]*Page)
	for id := uint64(1); id <= 3; id++ {
		pages[id] = mustAlloc(t, c, id)
		mustWrite(t, c, pages[id], "v1")
	}
	waitFor(t, "v1 writebacks in flight", func() bool { return len(d.Pending()) == 3 })

	// Re-dirty every page during writeback, then let both rounds complete.
	for id := uint64(1); id <= 3; id++ {
		mustWrite(t, c, pages[id], "v2")
	}
	d.ReleaseAll()
	waitFor(t, "v2 writebacks in flight", func() bool { return len(d.Pending()) == 3 })
	d.Unhold()
	waitFor(t, "all clean", func() bool {
		for id := uint64(1); id <= 3; id++ {
			if st, _ := c.State(id); st != StateClean {
				return false
			}
		}
		return true
	})

	// One more modification after the dust settles, then close.
	mustWrite(t, c, pages[2], "v3")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := map[uint64]string{1: "v2", 2: "v3", 3: "v2"}
	wantVersion := map[uint64]uint64{1: 2, 2: 3, 3: 2}
	for id, data := range want {
		version, acked, _ := c.Versions(id)
		if acked != version || version != wantVersion[id] {
			t.Fatalf("page %d: version=%d acked=%d, want both %d", id, version, acked, wantVersion[id])
		}
		sv, sdata, ok := d.Stored(id)
		if !ok || sv != acked || string(sdata) != data {
			t.Fatalf("page %d: stored v%d %q, want v%d %q", id, sv, sdata, acked, data)
		}
	}

	// The cache is closed for new work.
	if _, err := c.Alloc(context.Background(), 9); err != ErrClosed {
		t.Fatalf("Alloc after close = %v, want ErrClosed", err)
	}
	if err := c.Write(pages[1], []byte("x")); err != ErrClosed {
		t.Fatalf("Write after close = %v, want ErrClosed", err)
	}
}

// If the disk keeps failing, close cannot confirm the dirty page: it
// reports the timeout and the page is never marked clean.
func TestCloseWithFailingDisk(t *testing.T) {
	c := NewCache(2)
	c.retryBackoff = time.Millisecond
	d := c.Disk()
	d.SetFailAll(errors.New("disk on fire"))

	p := mustAlloc(t, c, 1)
	mustWrite(t, c, p, "v1")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := c.Close(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Close = %v, want DeadlineExceeded", err)
	}

	version, acked, _ := c.Versions(1)
	if version != 1 || acked != 0 {
		t.Fatalf("version=%d acked=%d, want 1/0 (unconfirmed data marked clean)", version, acked)
	}
	if _, _, ok := d.Stored(1); ok {
		t.Fatal("disk holds an image for a page whose writes all failed")
	}
}
