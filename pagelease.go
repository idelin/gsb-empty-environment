package netsec

import (
	"context"
	"errors"
	"sync"
)

// ErrRevoked is returned by Borrow after the file lease set has been revoked.
// Leases already handed out before revocation remain valid until Release.
var ErrRevoked = errors.New("pagelease: file has been revoked")

// ErrInvalidWindow is returned when a Borrow window is empty, negative,
// overflows, or extends past the end of the file.
var ErrInvalidWindow = errors.New("pagelease: borrow window is outside the file")

// ErrPageUnavailable wraps an authenticated-reader failure for one page. A
// Borrow returns it when at least one requested page cannot be delivered;
// only pages authenticated before the failure stay resident in the pool.
var ErrPageUnavailable = errors.New("pagelease: page could not be authenticated")

// PageReader is supplied by the host. ReadPage must return the fully
// authenticated contents of page pageIndex: exactly pageSize bytes for every
// page except the final one, whose length is pageSize minus the trailing gap.
// A non-nil error means the page failed authentication or retrieval and must
// never be lent to a caller.
type PageReader interface {
	ReadPage(ctx context.Context, pageIndex uint64) (data []byte, err error)
}

// frameState tracks the lifecycle of one pool frame.
type frameState uint8

const (
	stateLoading frameState = iota
	stateReady
	stateErr
)

// pageJob coalesces concurrent fetches of the same page.
type pageJob struct {
	done chan struct{}
}

// frame holds one cached page inside the bounded pool.
type frame struct {
	page  uint64
	state frameState
	buf   []byte
	err   error
	refs  int // loading waiters, then live leases; zero makes the frame evictable
	lru   uint64
	job   *pageJob
}

// Stats is a point-in-time snapshot of pool activity.
type Stats struct {
	Frames   int // frames currently occupied (loading, ready, or failed)
	InFlight int // pages currently being fetched
	Pinned   int // frames referenced by a waiter or live lease
	Waiting  int // borrowers blocked waiting for a free frame
	Leases   int // leases currently checked out
	Revoked  bool
	Closed   bool // revocation finished and every frame has been zeroed
}

// Cache is a bounded pool of authenticated file pages handed out through
// reference-counted Leases. A page is fetched at most once for any number of
// overlapping windows; eviction only considers frames with no active lease.
type Cache struct {
	reader    PageReader
	pageSize  uint64
	fileSize  uint64
	maxFrames int

	mu      sync.Mutex
	frames  map[uint64]*frame
	jobs    map[uint64]*pageJob
	leases  int
	waiting int
	lruSeq  uint64
	revoked bool
	closed  bool
	cond    *sync.Cond
}

// NewCache creates a lease pool. pageSize is the host page length, fileSize
// the exact file length (the partial tail page never exposes bytes past it),
// and maxFrames the hard cap on resident pages.
func NewCache(reader PageReader, pageSize, fileSize uint64, maxFrames int) (*Cache, error) {
	if reader == nil {
		return nil, errors.New("pagelease: reader is nil")
	}
	if pageSize == 0 {
		return nil, errors.New("pagelease: pageSize must be positive")
	}
	if maxFrames <= 0 {
		return nil, errors.New("pagelease: maxFrames must be positive")
	}
	c := &Cache{
		reader:    reader,
		pageSize:  pageSize,
		fileSize:  fileSize,
		maxFrames: maxFrames,
		frames:    make(map[uint64]*frame),
		jobs:      make(map[uint64]*pageJob),
	}
	c.cond = sync.NewCond(&c.mu)
	return c, nil
}

func addOverflow(a, b uint64) (uint64, bool) {
	sum := a + b
	return sum, sum < a
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Lease is a contiguous, read-only window over authenticated pages. The
// underlying pool frames stay pinned until Release is called.
type Lease struct {
	cache  *Cache
	pages  []*frame
	window []byte
	oncer  sync.Once
}

// Bytes returns the borrowed window. The slice must not be retained after
// Release; its backing memory is zeroed when the lease is returned.
func (l *Lease) Bytes() []byte {
	return l.window
}

// Release returns every pinned page. It is safe to call more than once.
func (l *Lease) Release() {
	l.oncer.Do(func() {
		c := l.cache
		c.mu.Lock()
		c.leases--
		for _, f := range l.pages {
			c.releaseRefLocked(f)
		}
		// Zero the assembled window so no plaintext copy outlives the lease.
		zero(l.window)
		l.pages = nil
		l.window = nil
		c.mu.Unlock()
	})
}

// Borrow pins the half-open window [offset, offset+length), spanning as many
// pages as needed and sharing frames with every other overlapping lease. It
// blocks while the pool is full, while pages are being authenticated, or
// while concurrent fetches of the same page are in flight. On failure no
// lease is returned and any pages pinned for the attempt are released.
func (c *Cache) Borrow(ctx context.Context, offset, length uint64) (*Lease, error) {
	if length == 0 {
		return nil, ErrInvalidWindow
	}
	end, overflow := addOverflow(offset, length)
	if overflow || end > c.fileSize {
		return nil, ErrInvalidWindow
	}
	firstPage := offset / c.pageSize
	lastPage := (end - 1) / c.pageSize
	pageCount := int(lastPage-firstPage) + 1

	pinned := make([]*frame, 0, pageCount)
	releasePinned := func() {
		c.mu.Lock()
		for _, f := range pinned {
			c.releaseRefLocked(f)
		}
		pinned = pinned[:0]
		c.mu.Unlock()
	}

	for page := firstPage; page <= lastPage; page++ {
		if err := ctx.Err(); err != nil {
			releasePinned()
			return nil, err
		}

		c.mu.Lock()
		if c.revoked {
			c.mu.Unlock()
			releasePinned()
			return nil, ErrRevoked
		}
		if err := c.waitRoomLocked(ctx); err != nil {
			c.mu.Unlock()
			releasePinned()
			return nil, err
		}

		f, job, err := c.ensureFrameLocked(page)
		if err != nil {
			c.mu.Unlock()
			releasePinned()
			return nil, err
		}
		pinned = append(pinned, f)
		c.mu.Unlock()

		if job != nil {
			select {
			case <-job.done:
			case <-ctx.Done():
				releasePinned()
				return nil, ctx.Err()
			}
			c.mu.Lock()
			if c.revoked {
				c.mu.Unlock()
				releasePinned()
				return nil, ErrRevoked
			}
			if f.state == stateErr {
				err = f.err
				c.mu.Unlock()
				releasePinned()
				return nil, err
			}
			if f.state != stateReady {
				c.mu.Unlock()
				releasePinned()
				return nil, ErrPageUnavailable
			}
			c.mu.Unlock()
		}
	}

	return c.assembleLocked(pinned, firstPage, lastPage, offset, end, length)
}

// assembleLocked builds the exact caller window from the authenticated
// frames. The window is clipped to each frame's valid bytes, so the
// uninitialized tail of the partial last page can never be lent out.
func (c *Cache) assembleLocked(pinned []*frame, firstPage, lastPage, offset, end, length uint64) (*Lease, error) {
	window := make([]byte, length)
	dest := 0

	c.mu.Lock()
	if c.revoked {
		// Revocation raced the final assembly: no plaintext is copied out.
		for _, f := range pinned {
			c.releaseRefLocked(f)
		}
		c.mu.Unlock()
		zero(window)
		return nil, ErrRevoked
	}

	idx := 0
	for page := firstPage; page <= lastPage; page++ {
		f := pinned[idx]
		idx++
		pageStart := page * c.pageSize
		pageEnd := pageStart + uint64(len(f.buf))
		lo := offset - pageStart
		if offset < pageStart {
			lo = 0
		}
		if lo > uint64(len(f.buf)) {
			lo = uint64(len(f.buf))
		}
		hi := end - pageStart
		if end > pageEnd {
			hi = uint64(len(f.buf))
		}
		if hi > uint64(len(f.buf)) {
			hi = uint64(len(f.buf))
		}
		dest += copy(window[dest:], f.buf[lo:hi])
	}

	if dest != int(length) {
		// Defensive: a validated window must always be fully covered.
		for _, f := range pinned {
			c.releaseRefLocked(f)
		}
		c.mu.Unlock()
		zero(window)
		return nil, ErrPageUnavailable
	}

	lease := &Lease{cache: c, pages: pinned, window: window}
	c.leases++
	c.mu.Unlock()
	return lease, nil
}

// ensureFrameLocked returns the frame for page, pinning one reference. A
// non-nil job means the caller must wait for the shared fetch to finish.
func (c *Cache) ensureFrameLocked(page uint64) (*frame, *pageJob, error) {
	if f, ok := c.frames[page]; ok {
		if f.state == stateErr {
			f.lru = c.nextLRULocked()
			return nil, nil, f.err
		}
		f.refs++
		if f.state == stateReady {
			f.lru = c.nextLRULocked()
			return f, nil, nil
		}
		// Loading frames keep their original insertion stamp so eviction
		// stays true to recency of use.
		return f, f.job, nil
	}

	f := &frame{
		page:  page,
		state: stateLoading,
		refs:  1,
		lru:   c.nextLRULocked(),
		buf:   make([]byte, c.pageSize),
	}
	c.frames[page] = f

	job := &pageJob{done: make(chan struct{})}
	f.job = job
	c.jobs[page] = job
	go c.runLoad(page, job, f)
	return f, job, nil
}

// runLoad performs the authenticated fetch outside the pool lock, then
// publishes the result to every waiter sharing the job.
func (c *Cache) runLoad(page uint64, job *pageJob, f *frame) {
	data, err := c.reader.ReadPage(context.Background(), page)

	c.mu.Lock()
	defer c.mu.Unlock()

	current, ok := c.frames[page]
	if !ok || current != f {
		// The frame was evicted while loading with no waiters; drop the
		// result without touching pool state.
		close(job.done)
		delete(c.jobs, page)
		return
	}

	delete(c.jobs, page)
	f.job = nil

	if c.revoked {
		// Revocation happened mid-fetch: authenticated bytes must never be
		// published; hand the frame back for immediate zeroing once waiters
		// release their pins.
		f.state = stateErr
		f.err = ErrRevoked
		f.lru = c.nextLRULocked()
		close(job.done)
		return
	}

	want := c.pageSize
	pageStart := page * c.pageSize
	if c.fileSize-pageStart < c.pageSize {
		want = c.fileSize - pageStart
	}
	if err != nil || uint64(len(data)) != want {
		// A failed or short/oversized read is an authentication failure:
		// never lend uninitialized bytes from the preallocated frame buffer.
		f.state = stateErr
		if err != nil {
			f.err = errors.Join(ErrPageUnavailable, err)
		} else {
			f.err = ErrPageUnavailable
		}
		f.lru = c.nextLRULocked()
		close(job.done)
		return
	}

	copy(f.buf, data)
	f.state = stateReady
	f.lru = c.nextLRULocked()
	close(job.done)
}

// waitRoomLocked blocks while every frame is pinned. Frames with no active
// reference are evicted (and zeroed) to make room, least-recently-used first.
func (c *Cache) waitRoomLocked(ctx context.Context) error {
	// A watcher cancels the Cond wait when ctx is done; Broadcast then
	// reclaims the waiter and the loop surfaces the context error.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		c.cond.Broadcast()
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(c.frames) < c.maxFrames || c.evictOneLocked() {
			return nil
		}
		c.waiting++
		c.cond.Wait()
		c.waiting--
	}
}

// evictOneLocked drops the unpinned frame with the oldest LRU stamp. A
// failed frame is evicted like any other unpinned frame; its error is simply
// retried on the next borrow.
func (c *Cache) evictOneLocked() bool {
	var victim *frame
	for _, f := range c.frames {
		if f.refs != 0 || f.state == stateLoading {
			continue
		}
		if victim == nil {
			victim = f
			continue
		}
		if f.lru < victim.lru {
			victim = f
		}
	}
	if victim == nil {
		return false
	}
	c.removeFrameLocked(victim)
	return true
}

func (c *Cache) removeFrameLocked(f *frame) {
	delete(c.frames, f.page)
	zero(f.buf)
	f.buf = nil
	f.state = stateErr
	f.err = nil
	c.wakeWaitersLocked()
}

// releaseRefLocked drops one lease or waiter reference. A frame that becomes
// unpinned during revocation is zeroed immediately; otherwise it becomes
// evictable and unblocks any borrower waiting for room.
func (c *Cache) releaseRefLocked(f *frame) {
	if f.refs <= 0 {
		return
	}
	f.refs--
	f.lru = c.nextLRULocked()
	if f.refs == 0 {
		if c.revoked {
			if _, ok := c.frames[f.page]; ok {
				c.removeFrameLocked(f)
			}
		} else {
			c.wakeWaitersLocked()
		}
	}
	c.checkCompleteLocked()
}

func (c *Cache) wakeWaitersLocked() {
	c.cond.Broadcast()
}

func (c *Cache) nextLRULocked() uint64 {
	c.lruSeq++
	return c.lruSeq
}

// Revoke stops new borrows. In-flight fetches and existing leases are allowed
// to finish; frames without an active lease are zeroed at once, and the last
// returned lease zeroes the rest.
func (c *Cache) Revoke() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.revoked {
		return
	}
	c.revoked = true
	for _, f := range c.frames {
		if f.refs == 0 {
			c.removeFrameLocked(f)
		}
	}
	c.checkCompleteLocked()
}

// checkCompleteLocked marks the pool closed once revocation has drained every
// frame and every lease.
func (c *Cache) checkCompleteLocked() {
	if c.revoked && !c.closed && len(c.frames) == 0 && c.leases == 0 && len(c.jobs) == 0 {
		c.closed = true
		c.wakeWaitersLocked()
	}
}

// WaitForZero blocks until revocation has finished and all cached page bytes
// have been zeroed, or ctx is canceled.
func (c *Cache) WaitForZero(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		c.cond.Broadcast()
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil
		}
		c.cond.Wait()
		c.mu.Unlock()
	}
}

// Stats returns a point-in-time snapshot of pool activity.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := Stats{
		Frames:   len(c.frames),
		InFlight: len(c.jobs),
		Waiting:  c.waiting,
		Leases:   c.leases,
		Revoked:  c.revoked,
		Closed:   c.closed,
	}
	for _, f := range c.frames {
		if f.refs > 0 {
			s.Pinned++
		}
	}
	return s
}
