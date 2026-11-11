package lease

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestBlockingConsumerMinimalPool reproduces the original leak scenario with a
// pool of a single buffer: an async consumer holds a derived slice while the
// owner returns the lease. The buffer must stay pinned until the consumer
// finishes, reads after return must be rejected, and the next tenant must
// never observe the previous tenant's bytes.
func TestBlockingConsumerMinimalPool(t *testing.T) {
	const capacity = 64
	pool := NewPool(1, capacity)
	ctx := context.Background()

	tenantA, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	secret := []byte("tenant-A-secret-payload")
	seg, err := tenantA.Write(0, secret)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if got, err := seg.Bytes(); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("live read = %q, %v; want %q", got, err, secret)
	}

	// Owner returns the lease while the consumer still holds the segment.
	tenantA.Release()

	// Read after return must be rejected even though the buffer is pinned.
	if _, err := seg.Bytes(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("read after release err = %v; want ErrLeaseClosed", err)
	}

	// The single buffer is pinned: a waiting acquirer must block.
	acquired := make(chan *Lease, 1)
	go func() {
		nl, acqErr := pool.Acquire(ctx)
		if acqErr == nil {
			acquired <- nl
		}
	}()

	blockCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(blockCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pinned pool acquire err = %v; want DeadlineExceeded", err)
	}
	if pool.Clears() != 0 {
		t.Fatalf("clears = %d while segment outstanding; want 0", pool.Clears())
	}

	// Consumer finishes: the last reference drops, the buffer is zeroed once
	// and the blocked new-tenant acquire unblocks over the same physical buffer.
	seg.Release()
	seg.Release() // idempotent: must not corrupt the reference count

	var tenantB *Lease
	select {
	case tenantB = <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked acquire never unblocked after segment release")
	}

	if tenantB.BufferID() != tenantA.BufferID() {
		t.Fatalf("new tenant got buffer %d; expected reuse of %d",
			tenantB.BufferID(), tenantA.BufferID())
	}
	if tenantB.Generation() <= tenantA.Generation() {
		t.Fatalf("generation not bumped: old=%d new=%d",
			tenantA.Generation(), tenantB.Generation())
	}
	if pool.Clears() != 1 {
		t.Fatalf("clears = %d; want exactly 1", pool.Clears())
	}

	// New tenant reads the reused region: only zeroes, never the old secret.
	fresh, err := tenantB.Segment(0, len(secret))
	if err != nil {
		t.Fatalf("new tenant segment: %v", err)
	}
	view, err := fresh.Bytes()
	if err != nil {
		t.Fatalf("new tenant read: %v", err)
	}
	if bytes.Equal(view, secret) || bytes.Contains(view, secret) {
		t.Fatalf("new tenant observed previous tenant data: %q", view)
	}
	for i, c := range view {
		if c != 0 {
			t.Fatalf("reused buffer not zeroed at byte %d: %q", i, view)
		}
	}

	// The stale segment remains permanently unreadable after reuse.
	if _, err := seg.Bytes(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("stale segment read err = %v; want ErrLeaseClosed", err)
	}

	fresh.Release()
	tenantB.Release()
}

// TestConcurrentReleasePathsZeroOnce storms cancel, timeout, normal release
// and segment release concurrently. The buffer must be zeroed exactly once
// and every derived slice must become unreadable.
func TestConcurrentReleasePathsZeroOnce(t *testing.T) {
	pool := NewPool(2, 128)

	for iter := 0; iter < 200; iter++ {
		before := pool.Clears()
		lease, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatalf("iter %d acquire: %v", iter, err)
		}

		segments := make([]*Segment, 0, 8)
		for i := range segmentsCap(8) {
			s, segErr := lease.Segment(i*4, 4)
			if segErr != nil {
				t.Fatalf("iter %d segment: %v", iter, segErr)
			}
			payload, _ := s.Bytes()
			copy(payload, []byte("DATA"))
			segments = append(segments, s)
		}

		var wg sync.WaitGroup
		for range 4 {
			wg.Add(2)
			go func() { defer wg.Done(); lease.Release() }()
			go func() { defer wg.Done(); lease.Cancel() }()
		}
		lease.WithTimeout(time.Microsecond)
		for _, s := range segments {
			wg.Add(1)
			go func(seg *Segment) {
				defer wg.Done()
				seg.Release()
				seg.Release() // duplicate release must not underflow refs
			}(s)
		}
		wg.Wait()

		if got := pool.Clears(); got != before+1 {
			t.Fatalf("iter %d clears = %d; want %d", iter, got, before+1)
		}
		for i, s := range segments {
			if _, err := s.Bytes(); !errors.Is(err, ErrLeaseClosed) {
				t.Fatalf("iter %d segment %d read err = %v; want ErrLeaseClosed",
					iter, i, err)
			}
		}

		// Further release/cancel calls after finalization change nothing.
		lease.Release()
		lease.Cancel()
		if got := pool.Clears(); got != before+1 {
			t.Fatalf("iter %d clears after redundant release = %d; want %d",
				iter, got, before+1)
		}
	}
}

// TestSegmentSharedOwnership verifies that each derived slice independently
// pins the buffer: releasing some references keeps the rest valid (until the
// owner terminates) and reuse waits for the final reference.
func TestSegmentSharedOwnership(t *testing.T) {
	pool := NewPool(1, 64)
	lease, _ := pool.Acquire(context.Background())

	s1, err := lease.Segment(0, 4)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := lease.Segment(4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := s1.Bytes(); len(b) != 4 {
		t.Fatalf("s1 len = %d; want 4", len(b))
	}

	lease.Release() // owner gone, two segment references remain

	blockCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(blockCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire with outstanding refs err = %v; want DeadlineExceeded", err)
	}

	s1.Release()
	blockCtx2, cancel2 := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel2()
	if _, err := pool.Acquire(blockCtx2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire with one ref left err = %v; want DeadlineExceeded", err)
	}

	s2.Release()
	next, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire after all refs released: %v", err)
	}
	if pool.Clears() != 1 {
		t.Fatalf("clears = %d; want 1", pool.Clears())
	}
	next.Release()
}

// TestAcquireCanceledWhilePinned ensures cancellation wakes a blocked
// acquirer and leaves the pool usable once the pinning reference drops.
func TestAcquireCanceledWhilePinned(t *testing.T) {
	pool := NewPool(1, 32)
	lease, _ := pool.Acquire(context.Background())
	seg, _ := lease.Segment(0, 1)
	lease.Release() // buffer pinned by seg

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := pool.Acquire(ctx)
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled acquire err = %v; want Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled acquire never returned")
	}

	seg.Release()
	next, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("pool not reusable: %v", err)
	}
	next.Release()
}

// TestTimeoutRejectsReadsButPinsBuffer checks the timeout path against normal
// release and outstanding references: timeout closes reads immediately, the
// buffer stays pinned until the last segment drops, and it zeroes once.
func TestTimeoutRejectsReadsButPinsBuffer(t *testing.T) {
	pool := NewPool(1, 32)
	lease, _ := pool.Acquire(context.Background())
	seg, _ := lease.Write(0, []byte("timed-out-data"))

	lease.WithTimeout(15 * time.Millisecond)

	// Wait for the timer to fire, racing an explicit release.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !lease.Done() {
		lease.Release()
		time.Sleep(time.Millisecond)
	}
	if !lease.Done() {
		t.Fatal("lease never became closed after timeout")
	}

	if _, err := seg.Bytes(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("read after timeout err = %v; want ErrLeaseClosed", err)
	}

	assertBlocked(t, pool)

	seg.Release()
	if pool.Clears() != 1 {
		t.Fatalf("clears = %d; want 1", pool.Clears())
	}
	next, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire after timeout finalize: %v", err)
	}
	view, _ := next.Segment(0, 14)
	data, _ := view.Bytes()
	for i, c := range data {
		if c != 0 {
			t.Fatalf("stale byte after timeout recycle at %d: %q", i, data)
		}
	}
	view.Release()
	next.Release()
}

// TestGenerationMismatchRejectsStaleReference exercises the defense-in-depth
// generation tag: if the physical buffer is ever handed out under a newer
// generation, a captured old segment cannot read it.
func TestGenerationMismatchRejectsStaleReference(t *testing.T) {
	pool := NewPool(1, 32)
	lease, _ := pool.Acquire(context.Background())
	seg, _ := lease.Segment(0, 8)

	if _, err := seg.Bytes(); err != nil {
		t.Fatalf("fresh segment unreadable: %v", err)
	}

	lease.mu.Lock()
	lease.buf.gen++ // simulate reuse under a new generation
	lease.mu.Unlock()

	if _, err := seg.Bytes(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("stale generation read err = %v; want ErrLeaseClosed", err)
	}

	lease.mu.Lock()
	lease.buf.gen--
	lease.mu.Unlock()
	seg.Release()
	lease.Release()
}

// TestSegmentRangeGuards checks out-of-bounds derivation and closed leases.
func TestSegmentRangeGuards(t *testing.T) {
	pool := NewPool(1, 16)
	lease, _ := pool.Acquire(context.Background())

	if _, err := lease.Segment(0, 17); !errors.Is(err, ErrSegmentRange) {
		t.Fatalf("overshoot err = %v; want ErrSegmentRange", err)
	}
	if _, err := lease.Segment(15, 2); !errors.Is(err, ErrSegmentRange) {
		t.Fatalf("edge overshoot err = %v; want ErrSegmentRange", err)
	}
	if _, err := lease.Write(16, []byte{1}); !errors.Is(err, ErrSegmentRange) {
		t.Fatalf("write overshoot err = %v; want ErrSegmentRange", err)
	}

	lease.Release()
	if _, err := lease.Segment(0, 1); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("segment after close err = %v; want ErrLeaseClosed", err)
	}
}

// segmentsCap returns a zero-length range helper to keep loop syntax simple.
func segmentsCap(n int) []struct{} { return make([]struct{}, n) }

// assertBlocked proves a single-buffer pool cannot be acquired while a
// reference remains outstanding, even after the deadline expires.
func assertBlocked(t *testing.T, pool *Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pinned acquire err = %v; want DeadlineExceeded", err)
	}
}
