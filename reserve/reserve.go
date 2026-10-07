// Package reserve implements a concurrency-safe resource reservation store.
//
// Each reservation occupies a half-open interval [Start, End) and an integer
// capacity. The store enforces that the summed capacity of overlapping
// reservations never exceeds the store's total capacity. Overlap is computed
// with an endpoint-scan (sweep-line) model.
//
// All mutations are optimistic-concurrency checked: callers pass the version
// they observed, and the mutation fails with ErrVersionMismatch if the store
// has since changed. Failed mutations never change state or the version.
package reserve

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrVersionMismatch = errors.New("reserve: expected version mismatch")
	ErrNotFound        = errors.New("reserve: reservation not found")
	ErrConflict        = errors.New("reserve: capacity conflict")
	ErrInvalidInterval = errors.New("reserve: invalid interval (start > end)")
	ErrInvalidCapacity = errors.New("reserve: negative capacity")
)

// Reservation occupies the half-open interval [Start, End) with Capacity
// units of the resource. A zero-length reservation (Start == End) is legal
// and occupies no capacity at any instant.
type Reservation struct {
	ID       string
	Start    int64
	End      int64
	Capacity int64
}

// Store is a concurrency-safe reservation book for a single resource pool.
type Store struct {
	mu       sync.Mutex
	capacity int64
	version  uint64
	res      map[string]Reservation
}

// NewStore returns a Store with the given total capacity.
func NewStore(capacity int64) *Store {
	return &Store{capacity: capacity, res: make(map[string]Reservation)}
}

// Version returns the current store version. Every successful mutation
// increments it by exactly one.
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Get returns a copy of the reservation with the given ID.
func (s *Store) Get(id string) (Reservation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.res[id]
	return r, ok
}

// Book inserts a new reservation. expectedVersion must equal the current
// version. On any error the store is unchanged.
func (s *Store) Book(expectedVersion uint64, r Reservation) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkVersion(expectedVersion); err != nil {
		return s.version, err
	}
	if err := validate(r); err != nil {
		return s.version, err
	}
	if _, exists := s.res[r.ID]; exists {
		return s.version, ErrConflict
	}
	if !s.fits(r, r.ID) {
		return s.version, ErrConflict
	}
	s.res[r.ID] = r
	s.version++
	return s.version, nil
}

// Modify atomically replaces the interval and capacity of an existing
// reservation. The replacement is validated against all other reservations
// while the old one is still held; if the new placement conflicts, the old
// reservation is kept intact. It is never the case that the old reservation
// is released and the new one then fails.
func (s *Store) Modify(expectedVersion uint64, id string, start, end, capacity int64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkVersion(expectedVersion); err != nil {
		return s.version, err
	}
	old, ok := s.res[id]
	if !ok {
		return s.version, ErrNotFound
	}
	next := Reservation{ID: id, Start: start, End: end, Capacity: capacity}
	if err := validate(next); err != nil {
		return s.version, err
	}
	// Validate against everyone except the reservation being replaced; the
	// old reservation still occupies its interval until the swap succeeds.
	if !s.fits(next, id) {
		return s.version, ErrConflict
	}
	_ = old // old is simply overwritten below; kept for clarity of the swap
	s.res[id] = next
	s.version++
	return s.version, nil
}

// Cancel removes a reservation. It is idempotent: canceling an ID that is
// not present (e.g. a retried cancel) succeeds without changing the version,
// so retries are safe. A version mismatch is still an error.
func (s *Store) Cancel(expectedVersion uint64, id string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkVersion(expectedVersion); err != nil {
		return s.version, err
	}
	if _, ok := s.res[id]; !ok {
		return s.version, nil // idempotent retry
	}
	delete(s.res, id)
	s.version++
	return s.version, nil
}

// MaxLoad returns the maximum concurrent load across all time, computed by
// an endpoint scan. Exported so tests and callers can audit consistency.
func (s *Store) MaxLoad() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maxLoad(s.all(), "")
}

func (s *Store) checkVersion(expected uint64) error {
	if expected != s.version {
		return ErrVersionMismatch
	}
	return nil
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

func (s *Store) all() []Reservation {
	out := make([]Reservation, 0, len(s.res))
	for _, r := range s.res {
		out = append(out, r)
	}
	return out
}

// fits reports whether candidate can be placed without exceeding total
// capacity, ignoring the reservation with ID ignoreID (the one being
// replaced, if any). Zero-length candidates always fit.
func (s *Store) fits(candidate Reservation, ignoreID string) bool {
	if candidate.Start == candidate.End || candidate.Capacity == 0 {
		return true
	}
	return maxLoad(s.overlapping(candidate.Start, candidate.End, ignoreID), ignoreID)+candidate.Capacity <= s.capacity
}

func (s *Store) overlapping(start, end int64, ignoreID string) []Reservation {
	var out []Reservation
	for _, r := range s.res {
		if r.ID == ignoreID {
			continue
		}
		if r.Start < end && r.End > start {
			out = append(out, r)
		}
	}
	return out
}

// endpoint is one sweep-line event: at time t the load changes by delta.
type endpoint struct {
	t     int64
	delta int64
}

// maxLoad computes the maximum simultaneous load of the given reservations
// using an endpoint scan: ends are processed before starts at the same
// instant, matching half-open interval semantics.
func maxLoad(res []Reservation, ignoreID string) int64 {
	events := make([]endpoint, 0, 2*len(res))
	for _, r := range res {
		if r.ID == ignoreID || r.Start == r.End || r.Capacity == 0 {
			continue
		}
		events = append(events, endpoint{r.Start, r.Capacity}, endpoint{r.End, -r.Capacity})
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		return events[i].delta < events[j].delta // ends (-) before starts (+)
	})
	var load, max int64
	for _, e := range events {
		load += e.delta
		if load > max {
			max = load
		}
	}
	return max
}
