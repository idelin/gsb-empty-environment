package bufpool

// Handle is a borrow token for one physical buffer at one generation. A
// handle obtained via Pool.Acquire may be writable; handles obtained via
// Retain are always read-only. Handles are safe to pass across goroutines
// (handoff), but each handle must be released exactly once.
type Handle struct {
	pool     *Pool
	buf      *buffer
	gen      uint64
	writable bool
	released bool
}

// ID returns the physical buffer identity tracked by the ledger.
func (h *Handle) ID() uint64 { return h.buf.id }

// Generation returns the generation this handle belongs to.
func (h *Handle) Generation() uint64 { return h.gen }

// checkLocked validates the handle under p.mu.
func (h *Handle) checkLocked() error {
	if h.buf.generation != h.gen {
		return ErrStale
	}
	if h.released {
		return ErrReleased
	}
	return nil
}

// Bytes returns a read-only view of the buffer's live length. The caller must
// not mutate the returned slice; use MutableBytes for writes.
func (h *Handle) Bytes() ([]byte, error) {
	p := h.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := h.checkLocked(); err != nil {
		return nil, err
	}
	return h.buf.data[:h.buf.length], nil
}

// MutableBytes returns the writable view. It requires a writable handle with
// no outstanding shared read-only borrows (refs == 1).
func (h *Handle) MutableBytes() ([]byte, error) {
	p := h.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := h.checkLocked(); err != nil {
		return nil, err
	}
	if !h.writable {
		return nil, ErrNotWritable
	}
	if h.buf.refs != 1 {
		return nil, ErrShared
	}
	return h.buf.data[:h.buf.length], nil
}

// Retain adds a read-only reference and returns a new handle for it. While
// any retained handle is outstanding, mutable access is denied.
func (h *Handle) Retain() (*Handle, error) {
	p := h.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := h.checkLocked(); err != nil {
		return nil, err
	}
	h.buf.refs++
	p.ledger.noteRetain(h.buf)
	return &Handle{pool: p, buf: h.buf, gen: h.gen}, nil
}

// Release drops this handle's reference. When the last reference goes away
// the buffer returns to the idle pool. Releasing twice, or releasing a stale
// handle, is an error and changes nothing.
func (h *Handle) Release() error {
	p := h.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := h.checkLocked(); err != nil {
		return err
	}
	h.released = true
	h.buf.refs--
	p.ledger.noteRelease(h.buf)
	if h.buf.refs == 0 {
		h.buf.writable = false
		p.releaseLocked(h.buf)
	}
	return nil
}
