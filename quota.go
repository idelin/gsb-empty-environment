// Package quota implements per-user byte quotas that distinguish actual
// usage from pending write reservations.
//
// File growth follows a reserve-then-commit protocol: extending a file
// reserves bytes up front, a short write commits only the bytes actually
// written and releases the difference, and truncation releases actual
// usage. Cross-user transfers validate the target's quota and apply
// atomically, so a failed operation never leaks or fabricates bytes.
package quota

import (
	"errors"
	"sync"
)

var (
	ErrUnknownUser              = errors.New("quota: unknown user")
	ErrQuotaExceeded            = errors.New("quota: limit exceeded")
	ErrInvalidAmount            = errors.New("quota: amount must be positive")
	ErrInsufficientUsage        = errors.New("quota: amount exceeds current usage")
	ErrReservationClosed        = errors.New("quota: reservation already settled")
	ErrVersionMismatch          = errors.New("quota: reservation version mismatch")
	ErrCommitExceedsReservation = errors.New("quota: commit exceeds reserved bytes")
)

// Reservation is a handle for bytes reserved by a pending write. It is
// settled exactly once, by Commit or Abort. The version identifies the
// reservation generation so a stale handle from a concurrent writer is
// rejected instead of corrupting the account.
type Reservation struct {
	user    string
	id      uint64
	version uint64
	bytes   int64
}

// User returns the account the reservation is charged to.
func (r *Reservation) User() string { return r.user }

// Bytes returns the number of bytes held by the reservation.
func (r *Reservation) Bytes() int64 { return r.bytes }

// Version returns the reservation generation checked by Commit.
func (r *Reservation) Version() uint64 { return r.version }

// Stats is a point-in-time view of one account.
type Stats struct {
	Limit    int64
	Used     int64 // committed bytes (actual usage)
	Reserved int64 // outstanding write reservations
}

// Free returns the bytes still available for new reservations.
func (s Stats) Free() int64 { return s.Limit - s.Used - s.Reserved }

type openReservation struct {
	bytes   int64
	version uint64
}

type account struct {
	limit    int64
	used     int64
	reserved int64
	nextID   uint64
	open     map[uint64]openReservation
}

// Manager tracks quotas for a set of users. All methods are safe for
// concurrent use.
type Manager struct {
	mu    sync.Mutex
	users map[string]*account
}

// New returns an empty Manager.
func New() *Manager {
	return &Manager{users: make(map[string]*account)}
}

// SetLimit creates the user's account if needed and sets its byte limit.
// It fails without changing anything if current usage plus reservations
// already exceed the new limit.
func (m *Manager) SetLimit(user string, limit int64) error {
	if limit < 0 {
		return ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	acct, ok := m.users[user]
	if !ok {
		m.users[user] = &account{limit: limit, open: make(map[uint64]openReservation)}
		return nil
	}
	if acct.used+acct.reserved > limit {
		return ErrQuotaExceeded
	}
	acct.limit = limit
	return nil
}

// Reserve holds n bytes against the user's quota for a pending write
// (file extension). It fails without side effects if the bytes do not
// fit in used+reserved+limit.
func (m *Manager) Reserve(user string, n int64) (*Reservation, error) {
	if n <= 0 {
		return nil, ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	acct, ok := m.users[user]
	if !ok {
		return nil, ErrUnknownUser
	}
	if acct.used+acct.reserved+n > acct.limit {
		return nil, ErrQuotaExceeded
	}
	acct.nextID++
	res := &Reservation{user: user, id: acct.nextID, version: acct.nextID, bytes: n}
	acct.open[res.id] = openReservation{bytes: n, version: res.version}
	acct.reserved += n
	return res, nil
}

// Commit settles a reservation after the write completes, converting
// actual bytes into real usage and releasing the unwritten difference
// (short write). A commit larger than the reservation, a settled
// reservation, or a stale version fails and leaves the account untouched.
func (m *Manager) Commit(res *Reservation, actual int64) error {
	if actual < 0 {
		return ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	acct, ok := m.users[res.user]
	if !ok {
		return ErrUnknownUser
	}
	open, ok := acct.open[res.id]
	if !ok {
		return ErrReservationClosed
	}
	if open.version != res.version {
		return ErrVersionMismatch
	}
	if actual > open.bytes {
		return ErrCommitExceedsReservation
	}
	delete(acct.open, res.id)
	acct.reserved -= open.bytes
	acct.used += actual
	return nil
}

// Abort releases the entire reservation, e.g. when the write fails.
func (m *Manager) Abort(res *Reservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	acct, ok := m.users[res.user]
	if !ok {
		return ErrUnknownUser
	}
	open, ok := acct.open[res.id]
	if !ok {
		return ErrReservationClosed
	}
	if open.version != res.version {
		return ErrVersionMismatch
	}
	delete(acct.open, res.id)
	acct.reserved -= open.bytes
	return nil
}

// Release drops n bytes of actual usage, e.g. when a file is truncated.
func (m *Manager) Release(user string, n int64) error {
	if n <= 0 {
		return ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	acct, ok := m.users[user]
	if !ok {
		return ErrUnknownUser
	}
	if n > acct.used {
		return ErrInsufficientUsage
	}
	acct.used -= n
	return nil
}

// Transfer moves n bytes of actual usage from one user to another, e.g.
// when a file changes owner. The target's quota (used+reserved) is
// validated first and both accounts are updated atomically, so a failed
// transfer changes neither side.
func (m *Manager) Transfer(from, to string, n int64) error {
	if n <= 0 {
		return ErrInvalidAmount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	src, ok := m.users[from]
	if !ok {
		return ErrUnknownUser
	}
	dst, ok := m.users[to]
	if !ok {
		return ErrUnknownUser
	}
	if n > src.used {
		return ErrInsufficientUsage
	}
	if from == to {
		return nil
	}
	if dst.used+dst.reserved+n > dst.limit {
		return ErrQuotaExceeded
	}
	src.used -= n
	dst.used += n
	return nil
}

// Usage returns a snapshot of the user's account.
func (m *Manager) Usage(user string) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acct, ok := m.users[user]
	if !ok {
		return Stats{}, ErrUnknownUser
	}
	return Stats{Limit: acct.limit, Used: acct.used, Reserved: acct.reserved}, nil
}
