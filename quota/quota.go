// Package quota implements a per-user byte quota manager that splits
// accounting into committed usage and outstanding write reservations.
//
// Writers reserve bytes before extending a file, commit the number of
// bytes actually written (releasing the difference on a short write),
// or abort the reservation entirely. Truncation releases committed
// usage. Transfers move committed bytes between users atomically,
// validating the target's quota before any state changes.
package quota

import (
	"errors"
	"sync"
)

var (
	ErrQuotaExceeded     = errors.New("quota: limit exceeded")
	ErrInvalidAmount     = errors.New("quota: amount must be positive")
	ErrInsufficientUsage = errors.New("quota: insufficient committed usage")
	ErrReservationClosed = errors.New("quota: reservation already settled")
	ErrCommitExceedsSize = errors.New("quota: commit exceeds reserved size")
)

type account struct {
	limit    int64
	used     int64 // committed bytes actually consumed
	reserved int64 // outstanding write reservations
}

// Manager tracks quotas for many users. All state transitions are
// serialized; a failed operation never mutates state, so failed
// operations cannot leak quota.
type Manager struct {
	mu      sync.Mutex
	users   map[string]*account
	version uint64 // increments once per successful mutation
}

func NewManager() *Manager {
	return &Manager{users: make(map[string]*account)}
}

func (m *Manager) accountLocked(user string) *account {
	a, ok := m.users[user]
	if !ok {
		a = &account{}
		m.users[user] = a
	}
	return a
}

// SetLimit sets the quota limit for a user.
func (m *Manager) SetLimit(user string, limit int64) error {
	if limit < 0 {
		return ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accountLocked(user).limit = limit
	m.version++
	return nil
}

// Stats returns committed usage, outstanding reservations, and limit.
func (m *Manager) Stats(user string) (used, reserved, limit int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accountLocked(user)
	return a.used, a.reserved, a.limit
}

// Version returns the number of successful mutations so far.
func (m *Manager) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version
}

// Reserve holds size bytes against the user's quota for a pending
// write. It fails without side effects if the reservation would push
// used+reserved beyond the limit.
func (m *Manager) Reserve(user string, size int64) (*Reservation, error) {
	if size <= 0 {
		return nil, ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accountLocked(user)
	if a.used+a.reserved+size > a.limit {
		return nil, ErrQuotaExceeded
	}
	a.reserved += size
	m.version++
	return &Reservation{mgr: m, user: user, size: size}, nil
}

// Release frees n bytes of committed usage, e.g. after truncating or
// deleting a file. It fails without side effects if n exceeds the
// user's committed usage.
func (m *Manager) Release(user string, n int64) error {
	if n <= 0 {
		return ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accountLocked(user)
	if n > a.used {
		return ErrInsufficientUsage
	}
	a.used -= n
	m.version++
	return nil
}

// Transfer moves n committed bytes from one user to another. The
// target's quota (including its outstanding reservations) is validated
// before any state changes; on failure neither account is modified.
func (m *Manager) Transfer(from, to string, n int64) error {
	if n <= 0 {
		return ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.accountLocked(from)
	dst := m.accountLocked(to)
	if n > src.used {
		return ErrInsufficientUsage
	}
	if dst.used+dst.reserved+n > dst.limit {
		return ErrQuotaExceeded
	}
	src.used -= n
	dst.used += n
	m.version++
	return nil
}

// Reservation is an outstanding hold on a user's quota. It must be
// settled exactly once via Commit or Abort.
type Reservation struct {
	mgr  *Manager
	user string
	size int64
	done bool
}

// Size returns the reserved byte count.
func (r *Reservation) Size() int64 { return r.size }

// Commit records actual bytes written (actual <= reserved size) as
// committed usage and releases the unwritten difference back to the
// quota. A short write therefore leaks nothing.
func (r *Reservation) Commit(actual int64) error {
	if actual < 0 || actual > r.size {
		return ErrCommitExceedsSize
	}
	m := r.mgr
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.done {
		return ErrReservationClosed
	}
	r.done = true
	a := m.accountLocked(r.user)
	a.reserved -= r.size
	a.used += actual
	m.version++
	return nil
}

// Abort releases the entire reservation without committing anything.
func (r *Reservation) Abort() error {
	m := r.mgr
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.done {
		return ErrReservationClosed
	}
	r.done = true
	a := m.accountLocked(r.user)
	a.reserved -= r.size
	m.version++
	return nil
}
