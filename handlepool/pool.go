// Package handlepool implements an ownership-tracked pool of file handles for
// a Go subprocess proxy.
//
// Each registered handle lives in a fixed slot and is bound to a tenant. The
// pool hands out opaque, generation-stamped tokens so that a slot can be
// released, reclaimed and reused while late references to an older token are
// still reliably rejected. Batches of handles can be transferred between
// tenants atomically: either every handle changes owner or none of them do.
//
// All synchronization is internal; the returned [Token] values are safe for
// concurrent use but are not comparable across pool lifetimes.
package handlepool

import (
	"errors"
	"sync"
)

// TenantID identifies the owner of a pooled handle.
type TenantID string

// Sentinel errors returned by pool operations. Callers should use errors.Is
// rather than matching on message text.
var (
	// ErrInvalidToken is returned when a token does not identify any live
	// handle, including stale tokens whose slot has been released and reused.
	ErrInvalidToken = errors.New("handlepool: invalid or stale token")

	// ErrNotOwner is returned when a tenant operates on a handle owned by a
	// different tenant.
	ErrNotOwner = errors.New("handlepool: tenant does not own handle")

	// ErrBorrowed is returned when an operation requires the handle to be in
	// the pool but the handle is currently borrowed out.
	ErrBorrowed = errors.New("handlepool: handle is borrowed")

	// ErrDuplicateToken is returned when a batch operation names the same
	// handle more than once.
	ErrDuplicateToken = errors.New("handlepool: duplicate token in batch")

	// ErrFaultInjected is the canonical fault returned by test fault hooks. It is
	// never produced by the pool itself; callers may wrap or compare against it.
	ErrFaultInjected = errors.New("handlepool: close fault injected")
)

// Handle is the resource managed by the pool. A real *os.File satisfies it
// directly because it exposes Fd and Close with matching signatures.
type Handle interface {
	// Fd returns the raw descriptor backing the handle.
	Fd() uintptr
	// Close releases the underlying descriptor.
	Close() error
}

// Closer abstracts the close system call so tests can inject failures without
// touching real descriptors. The pool calls Close at most once per registered
// handle and only after it has been released from every token.
type Closer interface {
	Close(h Handle) error
}

// CloserFunc adapts a plain function to the [Closer] interface.
type CloserFunc func(h Handle) error

// Close implements [Closer].
func (f CloserFunc) Close(h Handle) error { return f(h) }

// SyscallCloser closes handles through the operating system. It is the default
// [Closer] and simply delegates to the handle's own Close method (which for
// *os.File invokes the close(2) system call).
type SyscallCloser struct{}

// Close implements [Closer].
func (SyscallCloser) Close(h Handle) error { return h.Close() }

// Token is an opaque, unforgeable reference to a pooled handle. The zero value
// is never valid. Tokens embed a generation counter so that tokens handed out
// for a previous occupant of a slot remain invalid after the slot is reused.
type Token struct {
	slot       int
	generation uint64
}

// Valid reports whether the token is non-zero. A valid-looking token may still
// be rejected by the pool if its generation is stale.
func (t Token) Valid() bool { return t.generation != 0 }

// slotState is one entry in the pool.
type slotState struct {
	handle     Handle
	owner      TenantID
	generation uint64
	borrower   TenantID
	closed     bool
}

// Pool owns a fixed-capacity collection of handle slots.
type Pool struct {
	mu     sync.Mutex
	slots  []slotState
	free   []int
	closer Closer

	// fault, when non-nil, is consulted immediately before every close. A
	// non-nil result aborts the close without mutating pool ownership.
	fault func(h Handle) error

	// Metrics counters, guarded by mu.
	closeAttempts int
	closeFailures int
	closeSuccess  int
}

// Option customizes a [Pool] at construction.
type Option func(*Pool)

// WithCloser injects the [Closer] used to shut handles.
func WithCloser(c Closer) Option {
	return func(p *Pool) {
		if c != nil {
			p.closer = c
		}
	}
}

// New creates a pool able to hold at most capacity concurrently registered
// handles. Slots are reused once their handles are released.
func New(capacity int, opts ...Option) *Pool {
	if capacity < 0 {
		capacity = 0
	}
	p := &Pool{
		slots:  make([]slotState, capacity),
		free:   make([]int, 0, capacity),
		closer: SyscallCloser{},
	}
	for i := range p.slots {
		p.free = append(p.free, i)
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SetFault installs a fault hook consulted before every close. Passing nil
// removes the hook. A non-nil error from the hook makes [Pool.Release] return
// that error while leaving the handle open and under its current ownership.
func (p *Pool) SetFault(fn func(h Handle) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fault = fn
}

// Register adds a handle owned by tenant and returns its token. The pool takes
// logical ownership of the handle and is responsible for closing it exactly
// once, on [Pool.Release].
func (p *Pool) Register(tenant TenantID, h Handle) (Token, error) {
	if h == nil {
		return Token{}, errors.New("handlepool: cannot register nil handle")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.free) == 0 {
		return Token{}, errors.New("handlepool: pool capacity exhausted")
	}

	slot := p.free[len(p.free)-1]
	p.free = p.free[:len(p.free)-1]

	s := &p.slots[slot]
	s.generation++
	s.handle = h
	s.owner = tenant
	s.borrower = ""
	s.closed = false

	return Token{slot: slot, generation: s.generation}, nil
}

// lookup resolves a token to its live slot.
func (p *Pool) lookup(t Token) (*slotState, error) {
	if t.generation == 0 || t.slot < 0 || t.slot >= len(p.slots) {
		return nil, ErrInvalidToken
	}
	s := &p.slots[t.slot]
	if s.generation != t.generation || s.handle == nil || s.closed {
		return nil, ErrInvalidToken
	}
	return s, nil
}

// Borrow hands the handle behind token to borrower for exclusive use. The
// handle remains owned by its current tenant; it must be returned with
// [Pool.Return] before it can be transferred or released.
func (p *Pool) Borrow(borrower TenantID, token Token) (Handle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	s, err := p.lookup(token)
	if err != nil {
		return nil, err
	}
	if s.owner != borrower {
		return nil, ErrNotOwner
	}
	if s.borrower != "" {
		return nil, ErrBorrowed
	}
	s.borrower = borrower
	return s.handle, nil
}

// Return gives a borrowed handle back to the pool. borrower must match the
// tenant that holds the outstanding borrow.
func (p *Pool) Return(borrower TenantID, token Token) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	s, err := p.lookup(token)
	if err != nil {
		return err
	}
	if s.borrower == "" {
		return errors.New("handlepool: handle is not borrowed")
	}
	if s.borrower != borrower {
		return ErrNotOwner
	}
	s.borrower = ""
	return nil
}

// Release closes the handle behind token and returns its slot to the free
// list. The token and any token minted for a previous occupant of the same
// slot become invalid. If the close fails (including an injected fault) the
// handle stays open, ownership is unchanged and the slot is not reused.
func (p *Pool) Release(token Token) error {
	p.mu.Lock()

	s, err := p.lookup(token)
	if err != nil {
		p.mu.Unlock()
		return err
	}
	if s.borrower != "" {
		p.mu.Unlock()
		return ErrBorrowed
	}

	h := s.handle
	if p.fault != nil {
		if ferr := p.fault(h); ferr != nil {
			p.closeAttempts++
			p.closeFailures++
			p.mu.Unlock()
			return ferr
		}
	}

	p.closeAttempts++
	cerr := p.closer.Close(h)
	if cerr != nil {
		p.closeFailures++
		p.mu.Unlock()
		return cerr
	}

	p.closeSuccess++
	s.closed = true
	s.handle = nil
	s.owner = ""
	s.borrower = ""
	p.slots[token.slot] = *s
	p.free = append(p.free, token.slot)
	p.mu.Unlock()
	return nil
}

// Transfer atomically reassigns every token from srcTenant to dstTenant.
// The operation is all-or-nothing: if any token is unknown, stale, borrowed,
// owned by another tenant, or duplicated, no handle changes owner.
func (p *Pool) Transfer(srcTenant, dstTenant TenantID, tokens []Token) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	seen := make(map[Token]struct{}, len(tokens))
	targets := make([]*slotState, 0, len(tokens))

	for _, t := range tokens {
		if _, dup := seen[t]; dup {
			return ErrDuplicateToken
		}
		seen[t] = struct{}{}

		s, err := p.lookup(t)
		if err != nil {
			return err
		}
		if s.owner != srcTenant {
			return ErrNotOwner
		}
		if s.borrower != "" {
			return ErrBorrowed
		}
		targets = append(targets, s)
	}

	for _, s := range targets {
		s.owner = dstTenant
	}
	return nil
}

// OwnerOf reports the current owner of the handle behind token.
func (p *Pool) OwnerOf(token Token) (TenantID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, err := p.lookup(token)
	if err != nil {
		return "", err
	}
	return s.owner, nil
}

// Stats is a point-in-time snapshot of pool bookkeeping.
type Stats struct {
	Capacity      int
	Live          int // occupied, non-closed slots
	Borrowed      int // handles currently lent out
	FreeSlots     int // slots available for Register
	CloseAttempts int
	CloseFailures int
	CloseSuccess  int
}

// Stats returns a snapshot of pool counters for diagnostics and tests.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()

	var live, borrowed int
	for i := range p.slots {
		s := &p.slots[i]
		if s.handle != nil && !s.closed {
			live++
			if s.borrower != "" {
				borrowed++
			}
		}
	}
	return Stats{
		Capacity:      len(p.slots),
		Live:          live,
		FreeSlots:     len(p.free),
		Borrowed:      borrowed,
		CloseAttempts: p.closeAttempts,
		CloseFailures: p.closeFailures,
		CloseSuccess:  p.closeSuccess,
	}
}
