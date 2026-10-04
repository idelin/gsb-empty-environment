// Package bufpool implements a bounded, thread-safe pool of byte vectors.
//
// Buffers are checked out through capability-style handles. Every handle
// carries the generation of its buffer; each time a buffer is re-borrowed
// from the idle pool its generation is bumped, so stale handles fail every
// operation, including a second Release.
//
// Length and capacity are accounted separately: the pool budget limits
// capacity bytes (idle + in-use), while live length bytes are tracked as an
// independent metric.
package bufpool

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrStale is returned by every operation on a handle whose buffer
	// generation no longer matches, including handles consumed by
	// Release, Transfer, Downgrade or Upgrade.
	ErrStale = errors.New("bufpool: stale handle")
	// ErrWouldBlock is returned by TryBorrow when the capacity budget
	// cannot satisfy the request right now.
	ErrWouldBlock = errors.New("bufpool: insufficient capacity")
	// ErrOversize is returned when the request exceeds the pool budget
	// and could never be satisfied.
	ErrOversize = errors.New("bufpool: request exceeds pool capacity limit")
	// ErrMode is returned when a mutable operation is attempted through
	// a read-only handle.
	ErrMode = errors.New("bufpool: handle is read-only")
	// ErrShared is returned when a mutable borrow is requested while the
	// buffer is shared by read-only handles.
	ErrShared = errors.New("bufpool: buffer is shared read-only")
)

type mode int

const (
	modeWrite mode = iota
	modeRead
)

// EventKind classifies pool lifecycle events delivered to the observer.
type EventKind int

const (
	EventAlloc   EventKind = iota // new physical buffer created and charged
	EventBorrow                   // buffer checked out; generation bumped
	EventRelease                  // one handle released; refs still > 0
	EventIdle                     // refcount reached zero; buffer recyclable
	EventFree                     // buffer destroyed (Clear); capacity uncharged
)

// Event describes a single pool lifecycle transition. ID is the physical
// identity of the buffer and is never reused.
type Event struct {
	Kind EventKind
	ID   uint64
	Gen  uint64
	Len  int
	Cap  int
	Refs int
}

// Stats is a point-in-time snapshot of pool accounting.
type Stats struct {
	MaxCapBytes  int64 // configured capacity budget
	CapBytes     int64 // charged capacity: idle + in-use
	IdleCapBytes int64 // charged capacity held by idle buffers
	UseCapBytes  int64 // charged capacity held by in-use buffers
	LenBytes     int64 // sum of lengths of in-use buffers
	IdleBuffers  int
	UseBuffers   int
}

type buffer struct {
	id      uint64 // physical identity, never reused
	data    []byte
	gen     uint64
	refs    int  // live handles
	readers int  // live read-mode handles
	writer  bool // a live write-mode handle exists
}

// Pool is a bounded pool of byte vectors. The zero value is not usable;
// construct with NewPool.
type Pool struct {
	maxCap int64

	mu      sync.Mutex
	charged int64 // idle + in-use capacity bytes
	idleCap int64
	useCap  int64
	lenB    int64 // live length bytes of in-use buffers
	idle    []*buffer
	useN    int
	nextID  uint64
	waitCh  chan struct{} // closed and replaced on every state change
	observe func(Event)
}

// NewPool returns a pool whose total charged capacity (idle plus in-use)
// never exceeds maxCapBytes.
func NewPool(maxCapBytes int64) *Pool {
	if maxCapBytes < 0 {
		maxCapBytes = 0
	}
	return &Pool{maxCap: maxCapBytes, waitCh: make(chan struct{})}
}

// SetObserver installs a hook receiving lifecycle events. It is invoked
// while the pool lock is held and must not call back into the pool.
func (p *Pool) SetObserver(fn func(Event)) {
	p.mu.Lock()
	p.observe = fn
	p.mu.Unlock()
}

// Stats returns a snapshot of the pool accounting.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.statsLocked()
}

func (p *Pool) statsLocked() Stats {
	return Stats{
		MaxCapBytes:  p.maxCap,
		CapBytes:     p.charged,
		IdleCapBytes: p.idleCap,
		UseCapBytes:  p.useCap,
		LenBytes:     p.lenB,
		IdleBuffers:  len(p.idle),
		UseBuffers:   p.useN,
	}
}

// Borrow checks out a buffer of length n, waiting while capacity is
// unavailable. The returned handle is a mutable (write) borrow. The
// context cancels the wait; a failed borrow changes no accounting.
func (p *Pool) Borrow(ctx context.Context, n int) (*Handle, error) {
	if n < 0 || int64(n) > p.maxCap {
		return nil, ErrOversize
	}
	for {
		p.mu.Lock()
		if h, ok := p.borrowLocked(n); ok {
			p.mu.Unlock()
			return h, nil
		}
		ch := p.waitCh
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}
	}
}

// TryBorrow is Borrow without waiting.
func (p *Pool) TryBorrow(n int) (*Handle, error) {
	if n < 0 || int64(n) > p.maxCap {
		return nil, ErrOversize
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if h, ok := p.borrowLocked(n); ok {
		return h, nil
	}
	return nil, ErrWouldBlock
}

// borrowLocked either checks out a buffer or fails without touching any
// accounting, so a failed request never alters refcounts or charges.
func (p *Pool) borrowLocked(n int) (*Handle, bool) {
	best := -1
	for i, b := range p.idle {
		if cap(b.data) >= n && (best < 0 || cap(b.data) < cap(p.idle[best].data)) {
			best = i
		}
	}
	var b *buffer
	if best >= 0 {
		b = p.idle[best]
		p.idle = append(p.idle[:best], p.idle[best+1:]...)
		p.idleCap -= int64(cap(b.data))
	} else {
		if p.charged+int64(n) > p.maxCap {
			return nil, false
		}
		p.nextID++
		b = &buffer{id: p.nextID, data: make([]byte, n)}
		p.charged += int64(n)
		p.emitLocked(Event{Kind: EventAlloc, ID: b.id, Cap: n})
	}
	b.gen++
	b.data = b.data[:n]
	b.refs = 1
	b.readers = 0
	b.writer = true
	p.useCap += int64(cap(b.data))
	p.useN++
	p.lenB += int64(n)
	p.emitLocked(Event{Kind: EventBorrow, ID: b.id, Gen: b.gen, Len: n, Cap: cap(b.data), Refs: 1})
	return &Handle{pool: p, buf: b, gen: b.gen, m: modeWrite}, true
}

// Clear destroys every idle buffer and uncharges its capacity. In-use
// buffers are unaffected and remain charged until released.
func (p *Pool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range p.idle {
		c := int64(cap(b.data))
		p.charged -= c
		p.idleCap -= c
		p.emitLocked(Event{Kind: EventFree, ID: b.id, Cap: cap(b.data)})
	}
	p.idle = nil
	p.broadcastLocked()
}

func (p *Pool) broadcastLocked() {
	close(p.waitCh)
	p.waitCh = make(chan struct{})
}

func (p *Pool) emitLocked(e Event) {
	if p.observe != nil {
		p.observe(e)
	}
}

// Handle is a capability to one borrow of a pooled buffer. Handles are
// safe to pass between goroutines; all validation happens under the pool
// lock. A handle is consumed (becomes stale) by Release, Transfer,
// Downgrade and Upgrade.
type Handle struct {
	pool *Pool
	buf  *buffer
	gen  uint64 // 0 once consumed
	m    mode
}

// validLocked reports whether the handle still refers to a live borrow.
func (h *Handle) validLocked() bool {
	return h.gen != 0 && h.buf.gen == h.gen
}

// PhysicalID returns the identity of the underlying physical buffer. It
// is stable for the buffer's lifetime and never reused by the pool.
func (h *Handle) PhysicalID() uint64 { return h.buf.id }

// Generation returns the buffer generation this handle belongs to.
func (h *Handle) Generation() uint64 { return h.gen }

// Len returns the length of the borrowed vector.
func (h *Handle) Len() int {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	return len(h.buf.data)
}

// Cap returns the capacity of the borrowed vector.
func (h *Handle) Cap() int {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	return cap(h.buf.data)
}

// View returns the borrowed bytes for reading. It works on both read and
// write handles. While read handles are alive the pool guarantees no
// mutable borrow of the same buffer exists.
func (h *Handle) View() ([]byte, error) {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	if !h.validLocked() {
		return nil, ErrStale
	}
	return h.buf.data, nil
}

// Bytes returns the borrowed bytes for mutation. It requires a write
// handle; read-only handles receive ErrMode.
func (h *Handle) Bytes() ([]byte, error) {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	if !h.validLocked() {
		return nil, ErrStale
	}
	if h.m != modeWrite {
		return nil, ErrMode
	}
	return h.buf.data, nil
}

// Retain derives an additional handle to the same borrow, incrementing
// the buffer refcount. The new handle has the same access mode.
func (h *Handle) Retain() (*Handle, error) {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	if !h.validLocked() {
		return nil, ErrStale
	}
	h.buf.refs++
	if h.m == modeRead {
		h.buf.readers++
	}
	return &Handle{pool: h.pool, buf: h.buf, gen: h.gen, m: h.m}, nil
}

// Transfer consumes h and returns a fresh handle to the same borrow
// without changing the refcount. The old handle becomes stale.
func (h *Handle) Transfer() (*Handle, error) {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	if !h.validLocked() {
		return nil, ErrStale
	}
	nh := &Handle{pool: h.pool, buf: h.buf, gen: h.gen, m: h.m}
	h.gen = 0
	return nh, nil
}

// Downgrade consumes a write handle and returns a read-only handle to
// the same borrow, enabling shared read-only access.
func (h *Handle) Downgrade() (*Handle, error) {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	if !h.validLocked() {
		return nil, ErrStale
	}
	if h.m != modeWrite {
		return nil, ErrMode
	}
	h.buf.writer = false
	h.buf.readers++
	h.gen = 0
	return &Handle{pool: h.pool, buf: h.buf, gen: h.buf.gen, m: modeRead}, nil
}

// Upgrade consumes the sole read-only handle and returns a mutable
// handle. It fails with ErrShared while any other read handle is alive,
// so shared read-only borrows cannot be mutated underneath.
func (h *Handle) Upgrade() (*Handle, error) {
	h.pool.mu.Lock()
	defer h.pool.mu.Unlock()
	if !h.validLocked() {
		return nil, ErrStale
	}
	if h.m != modeRead {
		return nil, ErrMode
	}
	if h.buf.readers != 1 || h.buf.writer {
		return nil, ErrShared
	}
	h.buf.readers--
	h.buf.writer = true
	h.gen = 0
	return &Handle{pool: h.pool, buf: h.buf, gen: h.buf.gen, m: modeWrite}, nil
}

// Release returns this handle's share of the borrow. When the refcount
// reaches zero the buffer becomes idle and recyclable. Releasing a stale
// or already-consumed handle fails without touching the refcount.
func (h *Handle) Release() error {
	p := h.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if !h.validLocked() {
		return ErrStale
	}
	h.gen = 0
	b := h.buf
	b.refs--
	if h.m == modeRead {
		b.readers--
	} else {
		b.writer = false
	}
	if b.refs > 0 {
		p.emitLocked(Event{Kind: EventRelease, ID: b.id, Gen: b.gen, Refs: b.refs})
		return nil
	}
	c := int64(cap(b.data))
	p.useCap -= c
	p.useN--
	p.lenB -= int64(len(b.data))
	p.idleCap += c
	p.idle = append(p.idle, b)
	p.emitLocked(Event{Kind: EventIdle, ID: b.id, Gen: b.gen, Cap: cap(b.data)})
	p.broadcastLocked()
	return nil
}
