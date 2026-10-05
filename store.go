package sliding

import "sort"

// Event is a single observation keyed by ID.
type Event struct {
	ID    string
	Value float64
	Time  int64 // event time
}

// Store keeps the live event set, addressable by ID for revision/retraction.
type Store struct {
	events map[string]Event
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{events: make(map[string]Event)}
}

// Upsert inserts a new event or revises the existing one with the same ID.
// It returns the previous event and whether one existed.
func (s *Store) Upsert(e Event) (old Event, replaced bool) {
	old, replaced = s.events[e.ID]
	s.events[e.ID] = e
	return old, replaced
}

// Retract removes the event with the given ID.
func (s *Store) Retract(id string) (Event, bool) {
	e, ok := s.events[id]
	if ok {
		delete(s.events, id)
	}
	return e, ok
}

// Get returns the event with the given ID.
func (s *Store) Get(id string) (Event, bool) {
	e, ok := s.events[id]
	return e, ok
}

// Len returns the number of live events.
func (s *Store) Len() int {
	return len(s.events)
}

// windowValues rebuilds the sorted multiset of values with start < t <= end.
func (s *Store) windowValues(start, end int64) []float64 {
	vals := make([]float64, 0, len(s.events))
	for _, e := range s.events {
		if e.Time > start && e.Time <= end {
			vals = append(vals, e.Value)
		}
	}
	sort.Float64s(vals)
	return vals
}
