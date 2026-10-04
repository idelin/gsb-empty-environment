// Package seqwrite implements a sequential append manager for a single
// log/file shared by many producers.
//
// Producers reserve exclusive, never-reused file ranges up front, write
// their records into those ranges, and finalize them in any order
// (commit, fail, or cancel). Readers only ever observe the contiguous
// prefix of ranges that are finalized AND persisted to durable storage.
//
// Failure semantics:
//   - A failed or canceled reservation is overwritten with a recognizable
//     hole record (see hole.go) so the visible prefix can advance past it.
//   - If the hole record itself cannot be persisted, the manager is
//     poisoned: every subsequent Reserve/Commit/Fail/Cancel fails and the
//     visible prefix freezes.
//   - Cancel never reassigns an already-issued range: offsets are handed
//     out monotonically and a canceled range becomes a hole.
package seqwrite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

var (
	ErrClosed     = errors.New("seqwrite: manager closed")
	ErrSealed     = errors.New("seqwrite: reservation already finalized")
	ErrOutOfRange = errors.New("seqwrite: access outside reservation bounds")
	ErrCanceled   = errors.New("seqwrite: reservation canceled")
	ErrZeroLength = errors.New("seqwrite: zero-length reservation")
)

// Storage is the durable backing medium. *os.File satisfies it directly.
type Storage interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
}

const (
	statePending uint32 = iota
	stateCommitted
	stateHole
)

// Manager hands out append ranges and tracks the visible persisted prefix.
type Manager struct {
	st Storage

	mu      sync.Mutex
	ch      chan struct{}  // generation channel, closed on every state change
	next    uint64         // next offset to reserve; only moves forward
	visible uint64         // contiguous finalized+persisted prefix
	queue   []*Reservation // reservations in offset order
	poison  error
	closed  bool
}

// NewManager creates a Manager appending at offset 0 of st.
func NewManager(st Storage) *Manager {
	return &Manager{st: st, ch: make(chan struct{})}
}

// Reserve allocates the next n bytes and returns their exclusive owner.
// Offsets are monotonic: a reserved range is never reused, even if the
// reservation is later canceled or fails.
func (m *Manager) Reserve(n uint64) (*Reservation, error) {
	if n == 0 {
		return nil, ErrZeroLength
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poison != nil {
		return nil, m.poison
	}
	if m.closed {
		return nil, ErrClosed
	}
	r := &Reservation{mgr: m, Offset: m.next, Length: n}
	m.next += n
	m.queue = append(m.queue, r)
	return r, nil
}

// Visible returns the length of the contiguous finalized prefix that
// readers may safely consume.
func (m *Manager) Visible() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.visible
}

// Poisoned returns the non-nil fatal error once the log is poisoned.
func (m *Manager) Poisoned() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.poison
}

// ReadAt reads from the visible prefix only; reads never cross Visible().
func (m *Manager) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrOutOfRange
	}
	m.mu.Lock()
	vis := m.visible
	m.mu.Unlock()
	if uint64(off) >= vis {
		return 0, io.EOF
	}
	if end := uint64(off) + uint64(len(p)); end > vis {
		p = p[:vis-uint64(off)]
	}
	return m.st.ReadAt(p, off)
}

// WaitVisible blocks until the visible prefix reaches off, the manager is
// poisoned or closed, or ctx is done.
func (m *Manager) WaitVisible(ctx context.Context, off uint64) error {
	for {
		m.mu.Lock()
		if m.visible >= off {
			m.mu.Unlock()
			return nil
		}
		if m.poison != nil {
			err := m.poison
			m.mu.Unlock()
			return err
		}
		if m.closed {
			m.mu.Unlock()
			return ErrClosed
		}
		ch := m.ch
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
		}
	}
}

// Close seals the manager against new reservations and fsyncs storage.
// In-flight reservations may still be finalized after Close.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.signalLocked()
	m.mu.Unlock()
	return m.st.Sync()
}

// publish advances the visible prefix over finalized head reservations.
func (m *Manager) publish() {
	m.mu.Lock()
	advanced := false
	for len(m.queue) > 0 && atomic.LoadUint32(&m.queue[0].state) != statePending {
		head := m.queue[0]
		m.visible = head.Offset + head.Length
		m.queue[0] = nil
		m.queue = m.queue[1:]
		advanced = true
	}
	if advanced {
		m.signalLocked()
	}
	m.mu.Unlock()
}

func (m *Manager) poisonWith(err error) {
	m.mu.Lock()
	if m.poison == nil {
		m.poison = err
		m.signalLocked()
	}
	m.mu.Unlock()
}

func (m *Manager) poisonedErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.poison
}

// signalLocked wakes all WaitVisible waiters. m.mu must be held.
func (m *Manager) signalLocked() {
	close(m.ch)
	m.ch = make(chan struct{})
}

// Reservation owns the exclusive range [Offset, Offset+Length).
type Reservation struct {
	Offset uint64
	Length uint64

	mgr   *Manager
	wmu   sync.Mutex // serializes writes against finalization
	state uint32     // atomic: statePending / stateCommitted / stateHole
}

// Pending reports whether the reservation is still open for writes.
func (r *Reservation) Pending() bool {
	return atomic.LoadUint32(&r.state) == statePending
}

// WriteAt writes p at offset off relative to the reservation start.
// The write must lie entirely inside the reserved range.
func (r *Reservation) WriteAt(p []byte, off int64) (int, error) {
	if off < 0 || uint64(off)+uint64(len(p)) > r.Length {
		return 0, ErrOutOfRange
	}
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if atomic.LoadUint32(&r.state) != statePending {
		return 0, ErrSealed
	}
	return r.mgr.st.WriteAt(p, int64(r.Offset)+off)
}

// Commit fsyncs the written data and marks the reservation complete.
// If the fsync fails, Commit falls back to filling a hole record (and
// poisons the log if even that cannot be persisted).
func (r *Reservation) Commit() error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if atomic.LoadUint32(&r.state) != statePending {
		return ErrSealed
	}
	if err := r.mgr.poisonedErr(); err != nil {
		return err
	}
	if err := r.mgr.st.Sync(); err != nil {
		return r.fillHoleLocked(HoleReasonFailed,
			fmt.Errorf("seqwrite: commit sync failed: %v", err))
	}
	atomic.StoreUint32(&r.state, stateCommitted)
	r.mgr.publish()
	return nil
}

// Fail finalizes the reservation as failed, overwriting its start with a
// recognizable hole record. If the hole cannot be persisted, the manager
// is poisoned and subsequent publishes fail.
func (r *Reservation) Fail(cause error) error {
	if cause == nil {
		cause = errors.New("seqwrite: reservation failed")
	}
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if atomic.LoadUint32(&r.state) != statePending {
		return ErrSealed
	}
	return r.fillHoleLocked(HoleReasonFailed, cause)
}

// Cancel finalizes the reservation as canceled. The range is turned into
// a hole and is never reassigned to another record.
func (r *Reservation) Cancel() error {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	if atomic.LoadUint32(&r.state) != statePending {
		return ErrSealed
	}
	return r.fillHoleLocked(HoleReasonCanceled, ErrCanceled)
}

// fillHoleLocked writes and persists the hole record. r.wmu must be held.
func (r *Reservation) fillHoleLocked(reason byte, cause error) error {
	m := r.mgr
	if err := m.poisonedErr(); err != nil {
		return err
	}
	if r.Length < HoleHeaderSize {
		err := fmt.Errorf("seqwrite: reservation [%d,%d) too small for hole record",
			r.Offset, r.Offset+r.Length)
		m.poisonWith(err)
		return err
	}
	header := EncodeHole(r.Offset, r.Length, reason, cause)
	if _, err := m.st.WriteAt(header, int64(r.Offset)); err != nil {
		perr := fmt.Errorf("seqwrite: cannot write hole record at %d: %v", r.Offset, err)
		m.poisonWith(perr)
		return perr
	}
	if err := m.st.Sync(); err != nil {
		perr := fmt.Errorf("seqwrite: cannot persist hole record at %d: %v", r.Offset, err)
		m.poisonWith(perr)
		return perr
	}
	atomic.StoreUint32(&r.state, stateHole)
	m.publish()
	return nil
}
