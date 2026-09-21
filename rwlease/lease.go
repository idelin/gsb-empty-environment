package rwlease

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Mode identifies the kind of access a token grants.
type Mode int

const (
	Read Mode = iota
	Write
)

func (m Mode) String() string {
	switch m {
	case Read:
		return "read"
	case Write:
		return "write"
	default:
		return "unknown"
	}
}

// Sentinel errors reported by the manager.
var (
	// ErrNotHeld is returned for a token that was never registered (forged
	// identifier), that was issued by another manager, or whose mode does not
	// match the operation.
	ErrNotHeld = errors.New("rwlease: token is not held")
	// ErrAlreadyReleased guards against double Release on the same token.
	ErrAlreadyReleased = errors.New("rwlease: token already released")
	// ErrLeaseRevoked is returned when the whole generation was revoked,
	// either while waiting in the queue or when touching an invalidated token.
	ErrLeaseRevoked = errors.New("rwlease: lease revoked")
	// ErrUpgradeConflict means another reader is currently upgrading. Only one
	// upgrade may be attempted at a time, otherwise two readers could wait on
	// each other forever. The caller keeps its read lease.
	ErrUpgradeConflict = errors.New("rwlease: another upgrade is in progress")
	// ErrUpgradeImpossible means the request cannot be queued right now because
	// a writer is active or already queued (a waiting writer cannot be
	// overtaken). The caller keeps its read lease.
	ErrUpgradeImpossible = errors.New("rwlease: upgrade cannot be granted")
	// ErrTokenReplaced is returned when releasing the old read token after a
	// successful upgrade: the token was converted into the write token.
	ErrTokenReplaced = errors.New("rwlease: token replaced by upgraded token")
	// ErrTimeout is returned by the bounded acquire helpers when the lease
	// could not be obtained within the configured wait.
	ErrTimeout = errors.New("rwlease: timed out waiting for lease")
)

// Clock abstracts time so tests can drive deadlines deterministically.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Token is an opaque lease handle. The zero value and tokens created outside
// the package are treated as invalid (ErrNotHeld).
type Token struct {
	mode Mode
	id   uint64
	gen  uint64
}

// Mode reports the access mode the token was acquired with.
func (t Token) Mode() Mode { return t.mode }

// ID is the unique, monotonically increasing token identifier.
func (t Token) ID() uint64 { return t.id }

// Generation is the manager generation at acquisition time. Revoke advances
// the generation and invalidates older tokens.
func (t Token) Generation() uint64 { return t.gen }

type waiter struct {
	mode      Mode
	upgrading bool // queued write that atomically converts the caller's read
	ctx       context.Context
	timer     <-chan time.Time
	done      chan struct{}
	tok       Token
	err       error
}

// Manager is a revocable, writer-preferring read/write lease coordinator.
type Manager struct {
	mu sync.Mutex

	gen     uint64 // current generation; advanced by Revoke
	nextID  uint64
	readers int  // currently granted readers (includes an active upgrader)
	writer  bool // a writer (or upgraded reader) holds the lease
	upgrade bool // an upgrade request is queued or active
	// activeUpgradeID is the write-token id of a completed upgrade whose write
	// is currently held; 0 otherwise.
	activeUpgradeID uint64

	queue []*waiter

	// active maps a live token id to its generation.
	active map[uint64]uint64
	// terminal records the terminal error for revoked/superseded ids so that a
	// late Release yields a precise cause instead of ErrNotHeld.
	terminal map[uint64]error

	clock Clock
}

// Option configures a Manager at construction time.
type Option func(*Manager)

// WithClock installs a custom clock (mainly for tests).
func WithClock(c Clock) Option {
	return func(m *Manager) { m.clock = c }
}

// NewManager creates an empty lease coordinator.
func NewManager(opts ...Option) *Manager {
	m := &Manager{
		active:   make(map[uint64]uint64),
		terminal: make(map[uint64]error),
		clock:    realClock{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	if m.clock == nil {
		m.clock = realClock{}
	}
	return m
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Acquire requests a read or write lease, queuing until it can be granted.
// Context cancellation removes the caller's queue node and returns the context
// error. Revoke fails queued and live requests in the current generation.
func (m *Manager) Acquire(ctx context.Context, mode Mode) (Token, error) {
	return m.acquire(ctx, mode, 0)
}

// AcquireFor behaves like Acquire but gives up after maxWait using the
// configured Clock. On timeout the queue node is removed and ErrTimeout is
// returned. A non-positive maxWait means "wait forever".
func (m *Manager) AcquireFor(ctx context.Context, mode Mode, maxWait time.Duration) (Token, error) {
	tok, err := m.acquire(ctx, mode, maxWait)
	if errors.Is(err, errTimedOut) {
		return Token{}, ErrTimeout
	}
	return tok, err
}

var errTimedOut = errors.New("rwlease: internal timeout")

func (m *Manager) acquire(ctx context.Context, mode Mode, maxWait time.Duration) (Token, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return Token{}, err
	}
	w := &waiter{mode: mode, ctx: ctx, done: make(chan struct{})}
	if maxWait > 0 {
		w.timer = m.clock.After(maxWait)
	}
	m.queue = append(m.queue, w)
	m.pumpLocked()
	return m.waitLocked(w)
}

// waitLocked blocks until w is granted, revoked, canceled or timed out.
//
// The mutex must be held on entry and is always unlocked by the time this
// returns. If the wake-up was an abort (context or timer) rather than a
// terminal decision, the node is removed from the queue and waiting grants
// are re-evaluated, all under the lock.
func (m *Manager) waitLocked(w *waiter) (Token, error) {
	select {
	case <-w.done:
		m.mu.Unlock()
		return w.tok, w.err
	default:
	}
	m.mu.Unlock()

	select {
	case <-w.done:
		return w.tok, w.err
	case <-w.ctx.Done():
		err := w.ctx.Err()
		return m.abortWait(w, err)
	case <-w.timer:
		return m.abortWait(w, errTimedOut)
	}
}

// abortWait reacquires the lock and, unless a terminal decision raced in,
// removes the waiter and pumps the queue.
func (m *Manager) abortWait(w *waiter, cause error) (Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-w.done:
		return w.tok, w.err
	default:
	}
	m.removeQueuedLocked(w)
	if w.upgrading {
		m.upgrade = false
	}
	m.pumpLocked()
	return Token{}, cause
}

func (m *Manager) removeQueuedLocked(w *waiter) {
	for i, q := range m.queue {
		if q == w {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			return
		}
	}
}

// pumpLocked walks the FIFO queue and grants everything currently grantable.
// Once a writer (including an upgrader) is reached, no later node is granted,
// so a queued writer blocks subsequent new readers: that gate prevents writer
// starvation under a continuous read stream.
func (m *Manager) pumpLocked() {
	for len(m.queue) > 0 {
		w := m.queue[0]
		if w.upgrading {
			// The upgrader's own read is already counted in m.readers.
			if m.writer || m.readers != 1 {
				return
			}
			m.queue = m.queue[1:]
			m.writer = true
			m.grantLocked(w, Write, false)
			m.activeUpgradeID = w.tok.id
			return
		}
		switch w.mode {
		case Read:
			if m.writer || m.upgrade {
				return
			}
			m.queue = m.queue[1:]
			m.grantLocked(w, Read, true)
		case Write:
			if m.writer || m.readers > 0 {
				return
			}
			m.queue = m.queue[1:]
			m.writer = true
			m.grantLocked(w, Write, false)
			return
		default:
			m.queue = m.queue[1:]
			w.err = ErrNotHeld
			close(w.done)
		}
	}
}

func (m *Manager) grantLocked(w *waiter, mode Mode, countRead bool) {
	m.nextID++
	tok := Token{mode: mode, id: m.nextID, gen: m.gen}
	m.active[tok.id] = m.gen
	if countRead {
		m.readers++
	}
	w.tok = tok
	close(w.done)
}

// Upgrade converts an existing read token into a write token. It never
// consumes the caller's read right:
//   - ErrUpgradeConflict / ErrUpgradeImpossible / ErrTimeout / ctx error:
//     the original read token stays valid and usable.
//   - success: a new write token is returned; releasing the old read token
//     reports ErrTokenReplaced.
//   - revocation: the whole generation (old token included) is invalidated.
//
// Only one upgrade may be queued at a time, so two readers upgrading together
// cannot deadlock waiting for each other's reads to drain.
func (m *Manager) Upgrade(ctx context.Context, read Token) (Token, error) {
	return m.doUpgrade(ctx, read, 0)
}

// UpgradeFor is the bounded-wait variant of Upgrade.
func (m *Manager) UpgradeFor(ctx context.Context, read Token, maxWait time.Duration) (Token, error) {
	tok, err := m.doUpgrade(ctx, read, maxWait)
	if errors.Is(err, errTimedOut) {
		return Token{}, ErrTimeout
	}
	return tok, err
}

func (m *Manager) doUpgrade(ctx context.Context, read Token, maxWait time.Duration) (Token, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	if err := m.validateLocked(read, Read); err != nil {
		m.mu.Unlock()
		return Token{}, err
	}
	// Claim the single upgrade slot atomically with queue insertion so two
	// simultaneous upgraders can never both enter the waiting state.
	if m.upgrade {
		m.mu.Unlock()
		return Token{}, ErrUpgradeConflict
	}
	if m.writer || m.queuedWriterLocked() {
		m.mu.Unlock()
		return Token{}, ErrUpgradeImpossible
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return Token{}, err
	}
	w := &waiter{
		mode:      Write,
		upgrading: true,
		ctx:       ctx,
		done:      make(chan struct{}),
	}
	if maxWait > 0 {
		w.timer = m.clock.After(maxWait)
	}
	m.upgrade = true
	m.queue = append(m.queue, w)
	m.pumpLocked()

	tok, err := m.waitLocked(w)
	if err != nil {
		// abortWait already dropped the node and cleared m.upgrade. The caller's
		// read right is untouched.
		return Token{}, err
	}
	// Success: the old read token was atomically converted into tok.
	m.mu.Lock()
	delete(m.active, read.id)
	m.terminal[read.id] = ErrTokenReplaced
	m.mu.Unlock()
	return tok, nil
}

func (m *Manager) queuedWriterLocked() bool {
	for _, w := range m.queue {
		if w.mode == Write && !w.upgrading {
			return true
		}
	}
	return false
}

// Release returns a leased token. Releasing a token twice returns
// ErrAlreadyReleased; releasing a token from an older (revoked) generation
// returns ErrLeaseRevoked; a forged/foreign token returns ErrNotHeld.
func (m *Manager) Release(t Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if t.id == 0 {
		return ErrNotHeld
	}
	gen, ok := m.active[t.id]
	if !ok {
		if cause, known := m.terminal[t.id]; known {
			return cause
		}
		return ErrNotHeld
	}
	if gen != t.gen {
		return ErrLeaseRevoked
	}
	switch t.mode {
	case Read:
		if m.readers > 0 {
			m.readers--
		}
	case Write:
		m.writer = false
		if m.activeUpgradeID == t.id {
			// The active upgrader's write absorbed its own read.
			if m.readers > 0 {
				m.readers--
			}
			m.upgrade = false
			m.activeUpgradeID = 0
		}
	default:
		return ErrNotHeld
	}
	delete(m.active, t.id)
	m.terminal[t.id] = ErrAlreadyReleased
	m.pruneTerminalLocked()
	// Any release (read or write) may unblock queued waiters.
	m.pumpLocked()
	return nil
}

// validateLocked returns nil iff t is a live token of want in the current
// generation.
func (m *Manager) validateLocked(t Token, want Mode) error {
	if t.id == 0 {
		return ErrNotHeld
	}
	gen, ok := m.active[t.id]
	if !ok {
		if cause, known := m.terminal[t.id]; known {
			return cause
		}
		return ErrNotHeld
	}
	if gen != t.gen {
		return ErrLeaseRevoked
	}
	if t.mode != want {
		return ErrNotHeld
	}
	return nil
}

// Revoke invalidates the current generation: every queued waiter fails with
// ErrLeaseRevoked and every live token becomes stale. New acquisitions use a
// fresh generation. The manager returns to an idle state regardless of which
// tokens holders still believe they own.
func (m *Manager) Revoke() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.gen++
	old := m.queue
	m.queue = nil
	m.readers = 0
	m.writer = false
	m.upgrade = false
	m.activeUpgradeID = 0
	for _, w := range old {
		w.err = ErrLeaseRevoked
		close(w.done)
	}
	for id := range m.active {
		m.terminal[id] = ErrLeaseRevoked
	}
	m.active = make(map[uint64]uint64)
	m.pruneTerminalLocked()
}

// pruneTerminalLocked bounds the size of the terminal-error table so it cannot
// grow without limit over a long process lifetime.
func (m *Manager) pruneTerminalLocked() {
	const maxTerminal = 4096
	for len(m.terminal) > maxTerminal {
		for id := range m.terminal {
			delete(m.terminal, id)
			break
		}
	}
}

// WithLease acquires a lease of mode, runs task with the issued token, and
// guarantees Release once task returns. A task failure is propagated (joined
// with any release error). A panic is not recovered: the deferred Release
// still runs as the stack unwinds.
func (m *Manager) WithLease(ctx context.Context, mode Mode, task func(Token) error) error {
	tok, err := m.Acquire(ctx, mode)
	if err != nil {
		return err
	}
	taskErr := task(tok)
	relErr := m.Release(tok)
	return errors.Join(taskErr, relErr)
}
