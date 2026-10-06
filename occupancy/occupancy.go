// Package occupancy tracks capacity reservations over half-open time
// intervals for a single resource with a fixed total capacity.
//
// Concurrency model: every mutation carries the caller's expected store
// version (optimistic concurrency). A mismatch fails with
// ErrVersionConflict and leaves the store untouched. Modify atomically
// replaces the old interval: the new placement is validated while the old
// reservation is still held, so a failed Modify never loses the old
// reservation.
package occupancy

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrVersionConflict       = errors.New("occupancy: version conflict")
	ErrNotFound              = errors.New("occupancy: reservation not found")
	ErrDuplicateID           = errors.New("occupancy: duplicate reservation id")
	ErrInvalidInterval       = errors.New("occupancy: invalid interval (start > end)")
	ErrInvalidCapacity       = errors.New("occupancy: invalid capacity")
	ErrInsufficientCapacity  = errors.New("occupancy: insufficient capacity")
)

// Reservation occupies the half-open interval [Start, End) with Capacity
// units. A zero-length reservation (Start == End) occupies no time and
// never conflicts on capacity.
type Reservation struct {
	ID       string
	Start    int64
	End      int64
	Capacity int64
}

// Store is a concurrency-safe occupancy ledger for one resource.
type Store struct {
	mu      sync.Mutex
	total   int64
	version uint64
	res     map[string]Reservation
}

// NewStore returns a Store with the given total capacity.
func NewStore(total int64) *Store {
	return &Store{total: total, res: make(map[string]Reservation)}
}

// Version returns the current store version. Every successful mutation
// increments it by exactly one.
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Get returns a copy of the reservation with the given id.
func (s *Store) Get(id string) (Reservation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.res[id]
	return r, ok
}

// Snapshot returns all reservations, sorted by id.
func (s *Store) Snapshot() []Reservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Reservation, 0, len(s.res))
	for _, r := range s.res {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func validate(r Reservation) error {
	if r.Start > r.End {
		return ErrInvalidInterval
	}
	if r.Capacity < 0 {
		return ErrInvalidCapacity
	}
	return nil
}

// overlaps reports whether half-open intervals [a1,a2) and [b1,b2) share
// any time. Zero-length intervals never overlap anything.
func overlaps(a1, a2, b1, b2 int64) bool {
	return a1 < b2 && b1 < a2
}

// fitsLocked checks, via an endpoint sweep over existing reservations
// (excluding excludeID), whether placing cap units over [start, end)
// stays within total capacity at every point.
func (s *Store) fitsLocked(excludeID string, start, end, cap int64) bool {
	if start == end || cap == 0 {
		return true
	}
	type event struct {
		at    int64
		delta int64
	}
	var events []event
	for id, r := range s.res {
		if id == excludeID || r.Start == r.End || r.Capacity == 0 {
			continue
		}
		if !overlaps(start, end, r.Start, r.End) {
			continue
		}
		lo := r.Start
		if start > lo {
			lo = start
		}
		hi := r.End
		if end < hi {
			hi = end
		}
		events = append(events, event{lo, r.Capacity}, event{hi, -r.Capacity})
	}
	if len(events) == 0 {
		return cap <= s.total
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].at != events[j].at {
			return events[i].at < events[j].at
		}
		return events[i].delta < events[j].delta // ends before starts at same point
	})
	var used int64
	for _, e := range events {
		used += e.delta
		if used+cap > s.total {
			return false
		}
	}
	return true
}

// Reserve places a new reservation. expected must equal the current store
// version; on any failure the store is unchanged.
func (s *Store) Reserve(expected uint64, r Reservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.version {
		return ErrVersionConflict
	}
	if err := validate(r); err != nil {
		return err
	}
	if _, ok := s.res[r.ID]; ok {
		return ErrDuplicateID
	}
	if !s.fitsLocked("", r.Start, r.End, r.Capacity) {
		return ErrInsufficientCapacity
	}
	s.res[r.ID] = r
	s.version++
	return nil
}

// Modify atomically replaces the interval and capacity of an existing
// reservation. The new placement is validated while the old reservation
// still holds its capacity, so a failed Modify leaves the old reservation
// fully intact (no release-then-lose). On any failure the store is
// unchanged.
func (s *Store) Modify(expected uint64, id string, start, end, cap int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.version {
		return ErrVersionConflict
	}
	_, ok := s.res[id]
	if !ok {
		return ErrNotFound
	}
	next := Reservation{ID: id, Start: start, End: end, Capacity: cap}
	if err := validate(next); err != nil {
		return err
	}
	if !s.fitsLocked(id, start, end, cap) {
		return ErrInsufficientCapacity
	}
	s.res[id] = next
	s.version++
	return nil
}

// Cancel removes a reservation. Retrying a cancel for an already-removed
// id fails with ErrNotFound and leaves the store (and version) unchanged.
func (s *Store) Cancel(expected uint64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.version {
		return ErrVersionConflict
	}
	if _, ok := s.res[id]; !ok {
		return ErrNotFound
	}
	delete(s.res, id)
	s.version++
	return nil
}
