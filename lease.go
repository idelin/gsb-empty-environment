// Package lease implements a revocable read-write lease lock using only the
// Go standard library. Writers queue in FIFO order and, once queued, block
// new readers so a continuous reader stream cannot starve them. A reader may
// upgrade to a write lease; if the upgrade fails (e.g. its context is
// cancelled) the original read lease is kept. Every lease is represented by
// a generation-stamped Token that rejects double release and release across
// a revocation boundary (stale generation).
//
// The revocation task, the time source, and injected failures are all
// supplied by the caller (tests) via options.
package lease

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrDoubleRelease is returned when a Token is released more than once.
	ErrDoubleRelease = errors.New("lease: token already released")
	// ErrStaleToken is returned when a Token from a revoked generation is
	// released; the release is refused and lock counters are left untouched.
	ErrStaleToken = errors.New("lease: token belongs to a revoked generation")
	// ErrNotReadLease is returned when Upgrade is called with a token that
	// is not a live read lease.
	ErrNotReadLease = errors.New("lease: token is not an active read lease")
	// ErrNilToken is returned on a nil token.
	ErrNilToken = errors.New("lease: nil token")
)

// Kind identifies the lease kind held by a Token.
type Kind int

const (
	// Read is a shared read lease.
	Read Kind = iota
	// Write is an exclusive write lease.
	Write
)

// Clock abstracts time so tests can inject a fake clock.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Token is a capability for one lease. It is stamped with the lock
// generation at grant time; a Revoke bumps the generation, which makes old
// tokens stale so they can neither be released again nor corrupt the lock.
type Token struct {
	kind Kind
	gen  uint64

	mu   sync.Mutex
	used bool
}

// Kind reports whether the token is a read or write lease.
func (t *Token) Kind() Kind { return t.kind }

// Generation reports the lock generation the token belongs to.
func (t *Token) Generation() uint64 { return t.gen }

// waiter is one queued writer (or upgrader) node.
type waiter struct {
	ch      chan struct{} // closed when the lease is granted
	tok     *Token        // set before ch is closed
	upgrade bool          // node came from Upgrade
	src     *Token        // original read token, consumed on grant
}

// LeaseLock is a revocable read-write lease lock.
type LeaseLock struct {
	mu         sync.Mutex
	readers    int
	writing    bool
	queue      []*waiter     // FIFO writer queue; non-empty blocks new readers
	readGate   chan struct{} // closed to wake blocked readers
	gen        uint64        // bumped on every Revoke
	writeEpoch uint64        // bumped on every write grant, invalidates TTL timers

	clock    Clock
	writeTTL time.Duration // 0 disables automatic revocation
	onRevoke func(gen uint64) error
}

// Option configures a LeaseLock.
type Option func(*LeaseLock)

// WithClock injects the time source (tests use a fake clock).
func WithClock(c Clock) Option {
	return func(l *LeaseLock) {
		if c != nil {
			l.clock = c
		}
	}
}

// WithWriteTTL sets a time-to-live on write leases. When a write lease
// outlives the TTL the lock revokes it automatically.
func WithWriteTTL(d time.Duration) Option {
	return func(l *LeaseLock) { l.writeTTL = d }
}

// WithRevokeHook injects the revocation task. Its error (an injected
// failure, for instance) is returned by Revoke.
func WithRevokeHook(f func(gen uint64) error) Option {
	return func(l *LeaseLock) { l.onRevoke = f }
}

// New creates a LeaseLock.
func New(opts ...Option) *LeaseLock {
	l := &LeaseLock{
		readGate: make(chan struct{}),
		clock:    realClock{},
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// AcquireRead takes a read lease. It blocks while a writer holds the lock or
// any writer is queued, so queued writers are never starved by new readers.
func (l *LeaseLock) AcquireRead(ctx context.Context) (*Token, error) {
	l.mu.Lock()
	for {
		if !l.writing && len(l.queue) == 0 {
			l.readers++
			tok := &Token{kind: Read, gen: l.gen}
			l.mu.Unlock()
			return tok, nil
		}
		gate := l.readGate
		l.mu.Unlock()
		select {
		case <-gate:
			l.mu.Lock()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// AcquireWrite takes an exclusive write lease, queuing FIFO behind other
// writers. If ctx is cancelled while queued, the queue node is removed.
func (l *LeaseLock) AcquireWrite(ctx context.Context) (*Token, error) {
	l.mu.Lock()
	if !l.writing && l.readers == 0 && len(l.queue) == 0 {
		tok := l.grantWriteLocked()
		l.mu.Unlock()
		return tok, nil
	}
	w := &waiter{ch: make(chan struct{})}
	l.queue = append(l.queue, w)
	l.mu.Unlock()

	select {
	case <-w.ch:
		return w.tok, nil
	case <-ctx.Done():
		l.mu.Lock()
		if w.tok != nil { // grant raced with cancellation; keep the grant
			l.mu.Unlock()
			return w.tok, nil
		}
		l.removeWaiterLocked(w)
		l.maybeGrantLocked()
		l.wakeReadersLocked()
		l.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Upgrade turns a live read lease into a write lease. The read hold is
// released from the lock's bookkeeping while waiting (so two concurrent
// upgraders cannot deadlock each other), but if the upgrade fails the
// original read lease is restored and tok stays valid.
func (l *LeaseLock) Upgrade(ctx context.Context, tok *Token) (*Token, error) {
	l.mu.Lock()
	if err := l.validateReadLocked(tok); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	if !l.writing && len(l.queue) == 0 && l.readers == 1 {
		// Sole reader: upgrade in place.
		l.readers--
		consume(tok)
		wt := l.grantWriteLocked()
		l.mu.Unlock()
		return wt, nil
	}
	// Surrender the read hold while queued; restored on failure below.
	l.readers--
	w := &waiter{ch: make(chan struct{}), upgrade: true, src: tok}
	l.queue = append(l.queue, w)
	l.maybeGrantLocked() // readers may have dropped to zero
	l.mu.Unlock()

	select {
	case <-w.ch:
		return w.tok, nil
	case <-ctx.Done():
		l.mu.Lock()
		if w.tok != nil { // grant raced with cancellation
			l.mu.Unlock()
			return w.tok, nil
		}
		l.removeWaiterLocked(w)
		l.readers++ // upgrade failed: the original read lease survives
		l.maybeGrantLocked()
		l.wakeReadersLocked()
		l.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Release returns a lease. A second release of the same token fails with
// ErrDoubleRelease; releasing a token from a revoked generation fails with
// ErrStaleToken and leaves the lock state untouched.
func (l *LeaseLock) Release(tok *Token) error {
	if tok == nil {
		return ErrNilToken
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	tok.mu.Lock()
	if tok.used {
		tok.mu.Unlock()
		return ErrDoubleRelease
	}
	tok.used = true
	tok.mu.Unlock()
	if tok.gen != l.gen {
		return ErrStaleToken
	}
	switch tok.kind {
	case Read:
		l.readers--
	case Write:
		l.writing = false
	}
	l.maybeGrantLocked()
	l.wakeReadersLocked()
	return nil
}

// Revoke invalidates every outstanding lease: the generation is bumped (so
// old tokens become stale), the bookkeeping is reset, the injected
// revocation task runs, and the next queued writer is granted.
func (l *LeaseLock) Revoke() error {
	l.mu.Lock()
	l.gen++
	l.readers = 0
	l.writing = false
	hook := l.onRevoke
	gen := l.gen
	l.mu.Unlock()

	var err error
	if hook != nil {
		err = hook(gen)
	}

	l.mu.Lock()
	l.maybeGrantLocked()
	l.wakeReadersLocked()
	l.mu.Unlock()
	return err
}

// validateReadLocked checks that tok is a live read lease of the current
// generation. l.mu must be held.
func (l *LeaseLock) validateReadLocked(tok *Token) error {
	if tok == nil {
		return ErrNilToken
	}
	if tok.kind != Read {
		return ErrNotReadLease
	}
	tok.mu.Lock()
	defer tok.mu.Unlock()
	if tok.used {
		return ErrDoubleRelease
	}
	if tok.gen != l.gen {
		return ErrStaleToken
	}
	return nil
}

// grantWriteLocked switches the lock to the write state and returns a fresh
// token. l.mu must be held and the lock must be grantable.
func (l *LeaseLock) grantWriteLocked() *Token {
	l.writing = true
	l.writeEpoch++
	tok := &Token{kind: Write, gen: l.gen}
	if l.writeTTL > 0 {
		epoch := l.writeEpoch
		go func() {
			<-l.clock.After(l.writeTTL)
			l.mu.Lock()
			stale := !l.writing || l.writeEpoch != epoch
			l.mu.Unlock()
			if stale {
				return
			}
			l.Revoke()
		}()
	}
	return tok
}

// maybeGrantLocked grants the head queued writer when the lock is free.
// l.mu must be held.
func (l *LeaseLock) maybeGrantLocked() {
	if l.writing || l.readers > 0 || len(l.queue) == 0 {
		return
	}
	w := l.queue[0]
	l.queue = l.queue[1:]
	if w.upgrade {
		consume(w.src) // the read lease is replaced by the write lease
	}
	w.tok = l.grantWriteLocked()
	close(w.ch)
}

// wakeReadersLocked lets blocked readers re-check the gate. l.mu must be held.
func (l *LeaseLock) wakeReadersLocked() {
	if !l.writing && len(l.queue) == 0 {
		close(l.readGate)
		l.readGate = make(chan struct{})
	}
}

// removeWaiterLocked deletes a queue node. l.mu must be held.
func (l *LeaseLock) removeWaiterLocked(w *waiter) {
	for i, q := range l.queue {
		if q == w {
			l.queue = append(l.queue[:i], l.queue[i+1:]...)
			return
		}
	}
}

// consume marks a token as released without touching lock counters.
func consume(tok *Token) {
	tok.mu.Lock()
	tok.used = true
	tok.mu.Unlock()
}
