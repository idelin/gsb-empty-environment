package agglog

// Model is the per-event set reference model. It processes events
// one at a time, keeping for each key the set of event ids and the
// delta each contributed. Adding the same event twice is a no-op,
// so the model defines the ground truth that package application
// (any overlap, replay, or unpack order) must converge to.
type Model struct {
	events map[string]map[string]int64 // key -> event id -> delta
}

// NewModel returns an empty Model.
func NewModel() *Model {
	return &Model{events: make(map[string]map[string]int64)}
}

// Add records one event. Re-adding an id with the same delta is a
// no-op; re-adding with a different delta returns a Conflict and
// keeps the original.
func (m *Model) Add(ev Event) *Conflict {
	set, ok := m.events[ev.Key]
	if !ok {
		set = make(map[string]int64)
		m.events[ev.Key] = set
	}
	if prev, ok := set[ev.ID]; ok {
		if prev != ev.Delta {
			return &Conflict{Key: ev.Key, EventID: ev.ID, Applied: prev, Incoming: ev.Delta}
		}
		return nil
	}
	set[ev.ID] = ev.Delta
	return nil
}

// AddPackage feeds every entry of p through the per-event model.
func (m *Model) AddPackage(p Package) []Conflict {
	var conflicts []Conflict
	for _, e := range p.Entries {
		if c := m.Add(Event{ID: e.EventID, Key: p.Key, Delta: e.Delta}); c != nil {
			conflicts = append(conflicts, *c)
		}
	}
	return conflicts
}

// Total returns the sum of recorded deltas for key.
func (m *Model) Total(key string) int64 {
	var sum int64
	for _, d := range m.events[key] {
		sum += d
	}
	return sum
}

// Events returns a copy of the event set for key.
func (m *Model) Events(key string) map[string]int64 {
	out := make(map[string]int64, len(m.events[key]))
	for id, d := range m.events[key] {
		out[id] = d
	}
	return out
}

// Equivalent reports whether the store and the model hold the same
// per-event set for key (same ids, same deltas).
func Equivalent(s *Store, m *Model, key string) bool {
	se, me := s.Events(key), m.Events(key)
	if len(se) != len(me) {
		return false
	}
	for id, d := range me {
		if se[id] != d {
			return false
		}
	}
	return true
}
