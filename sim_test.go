package writeback

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitFor(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for: %s", msg)
		}
		time.Sleep(time.Millisecond)
	}
}

func mustWrite(t *testing.T, c *Cache, id uint64, data string) {
	t.Helper()
	if err := c.Write(context.Background(), id, []byte(data)); err != nil {
		t.Fatalf("write page %d: %v", id, err)
	}
}

func mustFlush(t *testing.T, c *Cache, id uint64) {
	t.Helper()
	if err := c.Flush(id); err != nil {
		t.Fatalf("flush page %d: %v", id, err)
	}
}

func pageStateOf(t *testing.T, c *Cache, id uint64) Page {
	t.Helper()
	p, ok := c.Snapshot(id)
	if !ok {
		t.Fatalf("page %d not resident", id)
	}
	return p
}

// A writeback completion confirms only the snapshot version it carries; a
// modification made while the writeback was in flight must stay dirty.
func TestAckConfirmsOnlySnapshotVersion(t *testing.T) {
	disk := NewSimDisk()
	c := NewCache(disk, 4)

	mustWrite(t, c, 1, "v1")
	mustFlush(t, c, 1)
	if got := pageStateOf(t, c, 1).state; got != stateWriting {
		t.Fatalf("state = %v, want writing", got)
	}

	// Modify the page while its v1 writeback is in flight.
	mustWrite(t, c, 1, "v2")
	p := pageStateOf(t, c, 1)
	if p.state != stateWriting || p.version != 2 || p.writingVer != 1 {
		t.Fatalf("during writeback: %+v", p)
	}

	// The v1 ack must not mark the page clean.
	disk.CompleteNext()
	waitFor(t, "v1 ack reconciled", func() bool {
		return pageStateOf(t, c, 1).cleanVersion == 1
	})
	p = pageStateOf(t, c, 1)
	if p.state != stateDirty {
		t.Fatalf("after stale-snapshot ack: state = %v, want dirty", p.state)
	}
	if data, ver, _ := disk.Read(1); string(data) != "v1" || ver != 1 {
		t.Fatalf("disk = %q v%d, want v1", data, ver)
	}

	// A second writeback confirms v2.
	mustFlush(t, c, 1)
	disk.CompleteNext()
	waitFor(t, "v2 ack reconciled", func() bool {
		return pageStateOf(t, c, 1).state == stateClean
	})
	p = pageStateOf(t, c, 1)
	if p.cleanVersion != 2 || p.version != 2 {
		t.Fatalf("after v2 ack: %+v", p)
	}
	if data, ver, _ := disk.Read(1); string(data) != "v2" || ver != 2 {
		t.Fatalf("disk = %q v%d, want v2", data, ver)
	}
}

// A late ack for an old writeback (e.g. a duplicate delivery or a write that
// succeeded after being reported failed) must not regress cleanVersion nor
// disturb a newer in-flight writeback.
func TestStaleAckArrivesLate(t *testing.T) {
	disk := NewSimDisk()
	c := NewCache(disk, 4)

	mustWrite(t, c, 1, "v1")
	mustFlush(t, c, 1)
	disk.CompleteNext()
	waitFor(t, "clean v1", func() bool {
		return pageStateOf(t, c, 1).state == stateClean
	})

	mustWrite(t, c, 1, "v2")
	mustFlush(t, c, 1)
	disk.CompleteNext()
	waitFor(t, "clean v2", func() bool {
		return pageStateOf(t, c, 1).cleanVersion == 2
	})

	// Late duplicate ack for v1: page is clean at v2, nothing may change.
	c.onAck(1, 1, nil)
	p := pageStateOf(t, c, 1)
	if p.state != stateClean || p.cleanVersion != 2 {
		t.Fatalf("late ack regressed page: %+v", p)
	}

	// Late ack for v2 while a v3 writeback is in flight: state must stay
	// writing and cleanVersion must not move backwards.
	mustWrite(t, c, 1, "v3")
	mustFlush(t, c, 1)
	c.onAck(1, 2, nil)
	p = pageStateOf(t, c, 1)
	if p.state != stateWriting || p.writingVer != 3 || p.cleanVersion != 2 {
		t.Fatalf("stale ack disturbed in-flight writeback: %+v", p)
	}
	disk.CompleteNext()
	waitFor(t, "clean v3", func() bool {
		return pageStateOf(t, c, 1).state == stateClean
	})
	if got := pageStateOf(t, c, 1).cleanVersion; got != 3 {
		t.Fatalf("cleanVersion = %d, want 3", got)
	}
}

// A failed writeback confirms nothing: the page goes back to dirty with its
// data intact and a retry persists it.
func TestWritebackFailure(t *testing.T) {
	disk := NewSimDisk()
	c := NewCache(disk, 4)

	mustWrite(t, c, 1, "important")
	mustFlush(t, c, 1)
	disk.FailNext(errors.New("io error"))
	waitFor(t, "failure reconciled", func() bool {
		return pageStateOf(t, c, 1).state == stateDirty
	})
	p := pageStateOf(t, c, 1)
	if p.cleanVersion != 0 || string(p.data) != "important" {
		t.Fatalf("after failure: %+v", p)
	}
	if _, _, ok := disk.Read(1); ok {
		t.Fatal("failed write must not persist")
	}

	// Retry succeeds.
	mustFlush(t, c, 1)
	disk.CompleteNext()
	waitFor(t, "retry confirmed", func() bool {
		return pageStateOf(t, c, 1).state == stateClean
	})
	if data, ver, _ := disk.Read(1); string(data) != "important" || ver != 1 {
		t.Fatalf("disk = %q v%d", data, ver)
	}
}

// A failure followed by a new modification must not confuse the retry: the
// page stays dirty and the retry carries the newest version.
func TestWritebackFailureThenModify(t *testing.T) {
	disk := NewSimDisk()
	c := NewCache(disk, 4)

	mustWrite(t, c, 1, "v1")
	mustFlush(t, c, 1)
	disk.FailNext(errors.New("io error"))
	waitFor(t, "dirty again", func() bool {
		return pageStateOf(t, c, 1).state == stateDirty
	})

	mustWrite(t, c, 1, "v2")
	mustFlush(t, c, 1)
	disk.CompleteNext()
	waitFor(t, "v2 confirmed", func() bool {
		return pageStateOf(t, c, 1).state == stateClean
	})
	p := pageStateOf(t, c, 1)
	if p.cleanVersion != 2 {
		t.Fatalf("cleanVersion = %d, want 2", p.cleanVersion)
	}
	if data, _, _ := disk.Read(1); string(data) != "v2" {
		t.Fatalf("disk = %q, want v2", data)
	}
}

// When the cache is full of dirty pages, a load blocks (backpressure) until
// a writeback frees a clean victim; the caller may cancel instead. Dirty
// pages are never dropped to make room.
func TestBackpressureAndCancellation(t *testing.T) {
	disk := NewSimDisk()
	c := NewCache(disk, 1)

	mustWrite(t, c, 1, "dirty-data") // fills the only slot, dirty

	// Cancelled load: returns the context error, dirty page untouched.
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Read(ctx, 2)
		errCh <- err
	}()
	waitFor(t, "writeback kicked by backpressure", func() bool {
		return disk.PendingWrites() == 1
	})
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled load err = %v, want context.Canceled", err)
	}
	p := pageStateOf(t, c, 1)
	if string(p.data) != "dirty-data" {
		t.Fatalf("dirty page clobbered: %+v", p)
	}
	if c.Resident() != 1 {
		t.Fatalf("resident = %d, dirty page must not be evicted", c.Resident())
	}

	// Uncancelled load: blocks until the in-flight writeback (kicked by the
	// backpressure above) completes and frees a clean victim.
	done := make(chan error, 1)
	go func() {
		done <- c.Write(context.Background(), 2, []byte("new-page"))
	}()
	select {
	case err := <-done:
		t.Fatalf("load returned %v while cache full of non-clean pages", err)
	case <-time.After(50 * time.Millisecond):
	}
	disk.CompleteNext() // page 1 becomes clean and evictable
	if err := <-done; err != nil {
		t.Fatalf("blocked load: %v", err)
	}
	waitFor(t, "page 1 evicted", func() bool {
		_, ok := c.Snapshot(1)
		return !ok
	})
	if data, ver, _ := disk.Read(1); string(data) != "dirty-data" || ver != 1 {
		t.Fatalf("evicted page not durable: %q v%d", data, ver)
	}
	if got := pageStateOf(t, c, 2); string(got.data) != "new-page" {
		t.Fatalf("page 2 = %q", got.data)
	}
}

// A barrier is released exactly when every write submitted before it has
// completed; later writes are irrelevant to it.
func TestBarrier(t *testing.T) {
	disk := NewSimDisk()
	c := NewCache(disk, 4)

	mustWrite(t, c, 1, "a")
	mustWrite(t, c, 2, "b")
	if err := c.FlushAll(); err != nil {
		t.Fatal(err)
	}
	barrier := disk.Barrier()

	assertOpen := func(msg string) {
		t.Helper()
		select {
		case <-barrier:
			t.Fatalf("barrier released early: %s", msg)
		default:
		}
	}
	assertOpen("no writes completed")

	disk.CompleteNext()
	assertOpen("only first write completed")

	disk.CompleteNext()
	select {
	case <-barrier:
	case <-time.After(time.Second):
		t.Fatal("barrier not released after all prior writes completed")
	}
	if data, _, _ := disk.Read(1); string(data) != "a" {
		t.Fatalf("page 1 = %q", data)
	}
	if data, _, _ := disk.Read(2); string(data) != "b" {
		t.Fatalf("page 2 = %q", data)
	}
}

// Checkpoint = flush everything dirty + wait on a barrier.
func TestCheckpoint(t *testing.T) {
	disk := NewSimDisk()
	c := NewCache(disk, 4)

	mustWrite(t, c, 1, "x")
	mustWrite(t, c, 2, "y")

	done := make(chan error, 1)
	go func() {
		done <- c.Checkpoint(context.Background())
	}()
	waitFor(t, "writebacks submitted", func() bool {
		return disk.PendingWrites() == 2
	})
	select {
	case err := <-done:
		t.Fatalf("checkpoint returned %v before writes completed", err)
	case <-time.After(50 * time.Millisecond):
	}
	for disk.CompleteNext() {
	}
	if err := <-done; err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	waitFor(t, "all clean", func() bool {
		p1 := pageStateOf(t, c, 1)
		p2 := pageStateOf(t, c, 2)
		return p1.state == stateClean && p2.state == stateClean
	})
}

// The versioned disk never lets an older write overwrite a newer durable
// version, even if completions are applied in submission order.
func TestDiskIgnoresStaleWrite(t *testing.T) {
	disk := NewSimDisk()
	disk.Submit(1, 2, []byte("new"))
	disk.Submit(1, 1, []byte("old"))
	disk.CompleteNext()
	disk.CompleteNext()
	data, ver, ok := disk.Read(1)
	if !ok || ver != 2 || string(data) != "new" {
		t.Fatalf("disk = %q v%d ok=%v, want new v2", data, ver, ok)
	}
}

// Graceful shutdown: in-flight writebacks complete and their acks are
// reconciled; unflushed dirty data is lost, but the durable image of every
// page is exactly its latest confirmed version.
func TestShutdownPersistsLatestConfirmed(t *testing.T) {
	disk := NewSimDisk()
	c := NewCache(disk, 8)

	// Page 1: v1 confirmed, then modified to v2 but never flushed.
	mustWrite(t, c, 1, "p1-v1")
	mustFlush(t, c, 1)
	disk.CompleteNext()
	waitFor(t, "p1 v1 confirmed", func() bool {
		return pageStateOf(t, c, 1).state == stateClean
	})
	mustWrite(t, c, 1, "p1-v2-unflushed")

	// Page 2: writeback in flight at shutdown.
	mustWrite(t, c, 2, "p2-v1")
	mustFlush(t, c, 2)

	// Page 3: dirty, never flushed.
	mustWrite(t, c, 3, "p3-v1-unflushed")

	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Page 1: durable image is the confirmed v1, not the unflushed v2.
	if data, ver, _ := disk.Read(1); string(data) != "p1-v1" || ver != 1 {
		t.Fatalf("page 1 durable = %q v%d, want p1-v1 v1", data, ver)
	}
	// Page 2: the in-flight writeback completed during shutdown.
	if data, ver, _ := disk.Read(2); string(data) != "p2-v1" || ver != 1 {
		t.Fatalf("page 2 durable = %q v%d, want p2-v1 v1", data, ver)
	}
	// Page 3: never flushed, nothing persisted.
	if _, _, ok := disk.Read(3); ok {
		t.Fatal("page 3 must not be persisted")
	}

	// The core invariant: for every resident page, the durable version is
	// exactly the latest confirmed version.
	for _, id := range []uint64{1, 2, 3} {
		p := pageStateOf(t, c, id)
		_, diskVer, ok := disk.Read(id)
		if !ok {
			diskVer = 0
		}
		if diskVer != p.cleanVersion {
			t.Fatalf("page %d: durable v%d != confirmed v%d", id, diskVer, p.cleanVersion)
		}
	}

	// The cache rejects new work after close.
	if err := c.Write(context.Background(), 4, []byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close = %v, want ErrClosed", err)
	}
}
