package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

const (
	testPageSize = uint64(8)
	testFileSize = uint64(30) // 4 pages; last page holds 6 valid bytes
)

// fakeBlockingReader serves deterministic, "authenticated" page contents and
// can block any page until an explicit gate is released.
type fakeBlockingReader struct {
	mu       sync.Mutex
	calls    map[uint64]int
	failPage uint64
	gates    map[uint64]chan struct{}
	release  map[uint64]chan struct{}
}

func newFakeReader() *fakeBlockingReader {
	return &fakeBlockingReader{
		calls:    map[uint64]int{},
		gates:    map[uint64]chan struct{}{},
		release:  map[uint64]chan struct{}{},
		failPage: ^uint64(0),
	}
}

func (r *fakeBlockingReader) block(page uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gates[page] = make(chan struct{})
	r.release[page] = make(chan struct{})
}

func (r *fakeBlockingReader) waitBlocked(t *testing.T, page uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := r.calls[page]
		r.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("reader for page %d never started", page)
}

func (r *fakeBlockingReader) unblock(page uint64) {
	r.mu.Lock()
	ch, ok := r.release[page]
	r.mu.Unlock()
	if ok {
		close(ch)
	}
}

func (r *fakeBlockingReader) callCount(page uint64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[page]
}

func (r *fakeBlockingReader) ReadPage(ctx context.Context, page uint64) ([]byte, error) {
	r.mu.Lock()
	r.calls[page]++
	gate := r.gates[page]
	rel := r.release[page]
	fail := page == r.failPage
	r.mu.Unlock()

	if gate != nil {
		close(gate)
		select {
		case <-rel:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fail {
		return nil, fmt.Errorf("auth failure on page %d", page)
	}

	start := page * testPageSize
	end := start + testPageSize
	if end > testFileSize {
		end = testFileSize
	}
	out := make([]byte, end-start)
	for i := range out {
		out[i] = byte(start + uint64(i))
	}
	return out, nil
}

func newTestCache(t *testing.T, r *fakeBlockingReader, maxFrames int) *Cache {
	t.Helper()
	c, err := NewCache(r, testPageSize, testFileSize, maxFrames)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return c
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}

func expectedWindow(offset, length uint64) []byte {
	out := make([]byte, length)
	for i := range out {
		out[i] = byte(offset + uint64(i))
	}
	return out
}

// Overlapping windows spanning multiple pages must share frames and return
// the exact authenticated byte ranges.
func TestOverlappingWindowsSharePages(t *testing.T) {
	r := newFakeReader()
	c := newTestCache(t, r, 4)

	l1, err := c.Borrow(context.Background(), 2, 10) // pages 0..1
	if err != nil {
		t.Fatalf("Borrow l1: %v", err)
	}
	if got, want := l1.Bytes(), expectedWindow(2, 10); !bytes.Equal(got, want) {
		t.Fatalf("l1 = %v, want %v", got, want)
	}

	l2, err := c.Borrow(context.Background(), 6, 12) // pages 0..2, overlaps l1
	if err != nil {
		t.Fatalf("Borrow l2: %v", err)
	}
	if got, want := l2.Bytes(), expectedWindow(6, 12); !bytes.Equal(got, want) {
		t.Fatalf("l2 = %v, want %v", got, want)
	}

	l3, err := c.Borrow(context.Background(), 16, 14) // pages 2..3 incl. partial tail
	if err != nil {
		t.Fatalf("Borrow l3: %v", err)
	}
	if got, want := l3.Bytes(), expectedWindow(16, 14); !bytes.Equal(got, want) {
		t.Fatalf("l3 = %v, want %v", got, want)
	}

	// Each page is fetched exactly once despite overlapping borrowers.
	for page := uint64(0); page < 4; page++ {
		if got := r.callCount(page); got != 1 {
			t.Fatalf("page %d fetched %d times, want 1", page, got)
		}
	}
	if got := c.Stats().Leases; got != 3 {
		t.Fatalf("Leases = %d, want 3", got)
	}

	l1.Release()
	l2.Release()
	l3.Release()
	if got := c.Stats().Leases; got != 0 {
		t.Fatalf("Leases after release = %d, want 0", got)
	}
}

// A smaller pool than the working set must evict only unpinned frames and
// block a borrower when every frame is pinned.
func TestSmallPoolEvictionAndBlocking(t *testing.T) {
	r := newFakeReader()
	c := newTestCache(t, r, 2)

	l1, err := c.Borrow(context.Background(), 0, 8) // page 0
	if err != nil {
		t.Fatalf("Borrow l1: %v", err)
	}
	l2, err := c.Borrow(context.Background(), 8, 8) // page 1; pool full, all pinned
	if err != nil {
		t.Fatalf("Borrow l2: %v", err)
	}

	got := make(chan *Lease, 1)
	errs := make(chan error, 1)
	go func() {
		l, err := c.Borrow(context.Background(), 16, 8) // needs page 2
		if err != nil {
			errs <- err
			return
		}
		got <- l
	}()

	waitFor(t, func() bool { return c.Stats().Waiting == 1 }, "third borrower never blocked")
	if s := c.Stats(); s.Frames != 2 || s.Pinned != 2 {
		t.Fatalf("stats while blocked = %+v", s)
	}

	// Releasing page 0 frees a frame; the blocked borrower proceeds and page
	// 0 is evicted.
	l1.Release()
	select {
	case l3 := <-got:
		if got, want := l3.Bytes(), expectedWindow(16, 8); !bytes.Equal(got, want) {
			t.Fatalf("l3 = %v, want %v", got, want)
		}
		l3.Release()
	case err := <-errs:
		t.Fatalf("blocked Borrow failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Borrow never resumed")
	}

	c.mu.Lock()
	_, page0Resident := c.frames[0]
	page2 := c.frames[2]
	c.mu.Unlock()
	if page0Resident {
		t.Fatal("page 0 should have been evicted")
	}
	if page2 == nil || page2.state != stateReady {
		t.Fatal("page 2 should be resident and ready")
	}

	// Re-borrowing evicted page 0 triggers exactly one new fetch.
	l4, err := c.Borrow(context.Background(), 0, 4)
	if err != nil {
		t.Fatalf("re-Borrow page 0: %v", err)
	}
	if got, want := l4.Bytes(), expectedWindow(0, 4); !bytes.Equal(got, want) {
		t.Fatalf("l4 = %v, want %v", got, want)
	}
	if got := r.callCount(0); got != 2 {
		t.Fatalf("page 0 fetched %d times, want 2", got)
	}
	l2.Release()
	l4.Release()
}

// Concurrent borrowers of the same blocked page share one fetch.
func TestConcurrentBorrowCoalescesFetch(t *testing.T) {
	r := newFakeReader()
	r.block(1)
	c := newTestCache(t, r, 4)

	const n = 5
	leases := make(chan *Lease, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := c.Borrow(context.Background(), 8, 8)
			if err != nil {
				errs <- err
				return
			}
			leases <- l
		}()
	}

	r.waitBlocked(t, 1)
	if got := r.callCount(1); got != 1 {
		t.Fatalf("in-flight fetches = %d, want 1", got)
	}
	r.unblock(1)

	wg.Wait()
	close(leases)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Borrow: %v", err)
	}
	count := 0
	for l := range leases {
		if got, want := l.Bytes(), expectedWindow(8, 8); !bytes.Equal(got, want) {
			t.Fatalf("lease bytes = %v, want %v", got, want)
		}
		l.Release()
		count++
	}
	if count != n {
		t.Fatalf("delivered %d leases, want %d", count, n)
	}
	if got := r.callCount(1); got != 1 {
		t.Fatalf("page 1 fetched %d times, want 1", got)
	}
}

// A mid-window read failure fails the whole borrow: earlier authenticated
// pages stay cached, the failed page is never lent and holds no buffer.
func TestPartialReadFailureCleanup(t *testing.T) {
	r := newFakeReader()
	r.failPage = 2
	c := newTestCache(t, r, 4)

	_, err := c.Borrow(context.Background(), 0, 24) // pages 0,1,2
	if !errors.Is(err, ErrPageUnavailable) {
		t.Fatalf("err = %v, want ErrPageUnavailable", err)
	}

	// Pages 0 and 1 were authenticated before the failure and remain cached.
	c.mu.Lock()
	p0, ok0 := c.frames[0]
	p1, ok1 := c.frames[1]
	failed, ok2 := c.frames[2]
	c.mu.Unlock()
	if !ok0 || p0.state != stateReady || !ok1 || p1.state != stateReady {
		t.Fatal("authenticated pages 0 and 1 must remain resident")
	}
	if !ok2 || failed.state != stateErr || failed.refs != 0 || !isAllZero(failed.buf) {
		t.Fatal("failed page must be an unpinned error frame with a zeroed buffer")
	}
	if s := c.Stats(); s.Leases != 0 || s.Pinned != 0 {
		t.Fatalf("no lease may survive failure, stats = %+v", s)
	}

	// Cached good pages are still lendable without a second fetch.
	l, err := c.Borrow(context.Background(), 2, 10)
	if err != nil {
		t.Fatalf("Borrow good pages after failure: %v", err)
	}
	if got, want := l.Bytes(), expectedWindow(2, 10); !bytes.Equal(got, want) {
		t.Fatalf("good window = %v, want %v", got, want)
	}
	l.Release()

	// After the reader recovers, fill the pool so LRU evicts the error
	// frame; retrying then re-fetches only the failed page.
	r.mu.Lock()
	r.failPage = ^uint64(0)
	r.mu.Unlock()
	occupy, err := c.Borrow(context.Background(), 24, 6) // page 3 displaces an LRU frame
	if err != nil {
		t.Fatalf("Borrow displacing frame: %v", err)
	}
	occupy.Release()
	l2, err := c.Borrow(context.Background(), 16, 8)
	if err != nil {
		t.Fatalf("retry Borrow: %v", err)
	}
	if got, want := l2.Bytes(), expectedWindow(16, 8); !bytes.Equal(got, want) {
		t.Fatalf("retried page = %v, want %v", got, want)
	}
	if got := r.callCount(2); got != 2 {
		t.Fatalf("page 2 fetched %d times, want 2", got)
	}
	l2.Release()
}

// Borrows must never expose bytes beyond EOF, including the partial tail.
func TestUninitializedTailCannotBeLent(t *testing.T) {
	r := newFakeReader()
	c := newTestCache(t, r, 4)

	if _, err := c.Borrow(context.Background(), testFileSize, 1); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("window at EOF err = %v, want ErrInvalidWindow", err)
	}
	if _, err := c.Borrow(context.Background(), testFileSize-1, 2); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("window past EOF err = %v, want ErrInvalidWindow", err)
	}
	if _, err := c.Borrow(context.Background(), 0, 0); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("zero-length err = %v, want ErrInvalidWindow", err)
	}
	if _, err := c.Borrow(context.Background(), ^uint64(0)-3, 10); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("overflow err = %v, want ErrInvalidWindow", err)
	}

	// The partial last page yields only its six authenticated bytes.
	l, err := c.Borrow(context.Background(), 24, 6)
	if err != nil {
		t.Fatalf("tail Borrow: %v", err)
	}
	if got, want := l.Bytes(), expectedWindow(24, 6); !bytes.Equal(got, want) {
		t.Fatalf("tail = %v, want %v", got, want)
	}
	if len(l.Bytes()) != 6 {
		t.Fatalf("tail length = %d, want 6", len(l.Bytes()))
	}
	l.Release()
}

// Revocation rejects new borrows, zeroes unpinned frames immediately, and
// only reports completion after the last outstanding lease is returned.
func TestRevokeZeroesAfterLastReturn(t *testing.T) {
	r := newFakeReader()
	c := newTestCache(t, r, 4)

	l1, err := c.Borrow(context.Background(), 0, 16) // pages 0,1
	if err != nil {
		t.Fatalf("Borrow l1: %v", err)
	}
	l2, err := c.Borrow(context.Background(), 16, 8) // page 2
	if err != nil {
		t.Fatalf("Borrow l2: %v", err)
	}

	// Snapshot the page-0 backing array to verify zeroing after release.
	c.mu.Lock()
	pinnedBuf := c.frames[0].buf
	c.mu.Unlock()

	c.Revoke()

	if _, err := c.Borrow(context.Background(), 0, 8); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Borrow after revoke err = %v, want ErrRevoked", err)
	}

	closed := make(chan error, 1)
	go func() { closed <- c.WaitForZero(context.Background()) }()

	// Pinned frames survive until their leases come back.
	waitFor(t, func() bool { return c.Stats().Revoked && c.Stats().Frames == 3 }, "pinned frames drained early")
	select {
	case err := <-closed:
		t.Fatalf("WaitForZero returned before last release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	l1.Release()
	waitFor(t, func() bool { return c.Stats().Frames == 1 }, "page 0/1 frames not zeroed on release")
	if !isAllZero(pinnedBuf) {
		t.Fatal("released frame buffer was not zeroed")
	}

	l2.Release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("WaitForZero: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForZero never completed")
	}
	s := c.Stats()
	if !s.Closed || s.Frames != 0 || s.Leases != 0 {
		t.Fatalf("post-revoke stats = %+v", s)
	}

	// Double release is harmless.
	l1.Release()
	l2.Release()
}

// Revocation while a page fetch is blocked still completes once the fetch
// finishes and the waiting borrower gives its pin back.
func TestRevokeDuringInFlightFetch(t *testing.T) {
	r := newFakeReader()
	r.block(1)
	c := newTestCache(t, r, 2)

	errs := make(chan error, 1)
	go func() {
		_, err := c.Borrow(context.Background(), 8, 8)
		errs <- err
	}()
	r.waitBlocked(t, 1)

	c.Revoke()
	r.unblock(1)

	select {
	case err := <-errs:
		if !errors.Is(err, ErrRevoked) {
			t.Fatalf("in-flight Borrow err = %v, want ErrRevoked", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight Borrow never returned")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.WaitForZero(ctx); err != nil {
		t.Fatalf("WaitForZero: %v", err)
	}
}

// Context cancellation while waiting for pool room must not pin any frame.
func TestBorrowContextCancellation(t *testing.T) {
	r := newFakeReader()
	c := newTestCache(t, r, 1)

	l1, err := c.Borrow(context.Background(), 0, 8)
	if err != nil {
		t.Fatalf("Borrow l1: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := c.Borrow(ctx, 8, 8)
		errs <- err
	}()
	waitFor(t, func() bool { return c.Stats().Waiting == 1 }, "borrower never waited")
	cancel()

	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled Borrow never returned")
	}

	waitFor(t, func() bool { return c.Stats().Waiting == 0 }, "waiter leaked")
	l1.Release()

	// Pool is fully usable after a canceled wait.
	l2, err := c.Borrow(context.Background(), 8, 8)
	if err != nil {
		t.Fatalf("Borrow after cancel: %v", err)
	}
	if got, want := l2.Bytes(), expectedWindow(8, 8); !bytes.Equal(got, want) {
		t.Fatalf("l2 = %v, want %v", got, want)
	}
	l2.Release()
}

func isAllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// Stress: many overlapping borrowers on a tiny pool, with a concurrent
// revoke. After WaitForZero, no frame may remain and no secret may leak.
func TestConcurrentStressWithRevoke(t *testing.T) {
	r := newFakeReader()
	c := newTestCache(t, r, 2)

	const workers = 12
	var wg sync.WaitGroup
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				length := uint64(1 + (seed+i)%int(testFileSize))
				if length > testFileSize {
					length = testFileSize
				}
				offset := uint64((seed*7 + i*3) % int(testFileSize-length+1))
				ctx, cancel := context.WithTimeout(workerCtx, time.Second)
				l, err := c.Borrow(ctx, offset, length)
				cancel()
				if err != nil {
					if errors.Is(err, ErrRevoked) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
						continue
					}
					t.Errorf("unexpected Borrow error: %v", err)
					return
				}
				if got, want := l.Bytes(), expectedWindow(offset, length); !bytes.Equal(got, want) {
					t.Errorf("window mismatch at %d len %d", offset, length)
					l.Release()
					return
				}
				if (seed+i)%3 == 0 {
					c.Revoke()
				}
				l.Release()
			}
		}(w + 1)
	}

	time.Sleep(10 * time.Millisecond)
	c.Revoke()
	workerCancel()
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.WaitForZero(ctx); err != nil {
		t.Fatalf("WaitForZero: %v", err)
	}
	s := c.Stats()
	if !s.Closed || s.Frames != 0 || s.Pinned != 0 || s.Leases != 0 || s.InFlight != 0 {
		t.Fatalf("post-stress stats = %+v", s)
	}
}
