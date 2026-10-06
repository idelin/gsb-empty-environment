package agglog

import "fmt"

// Conflict reports an event id whose packaged delta disagrees with
// the delta already applied for that id.
type Conflict struct {
	Key      string
	EventID  string
	Applied  int64 // delta already in the store
	Incoming int64 // delta carried by the package
}

func (c Conflict) Error() string {
	return fmt.Sprintf("agglog: conflict on %s/%s: applied %d, incoming %d",
		c.Key, c.EventID, c.Applied, c.Incoming)
}

// Receipt summarizes the application of one Package.
type Receipt struct {
	Applied   []string   // event ids newly applied by this call
	Skipped   []string   // event ids already applied with equal delta
	Conflicts []Conflict // overlapping ids with a different delta
}

// OK reports whether the package applied without conflicts.
func (r Receipt) OK() bool { return len(r.Conflicts) == 0 }

// Store applies packages against a per-event set: each key maps
// event id to the delta that was applied. Applying a package only
// adds unseen events, so replays and overlapping packages are safe.
type Store struct {
	seen map[string]map[string]int64 // key -> event id -> applied delta
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{seen: make(map[string]map[string]int64)}
}

// Apply unpacks p against the per-event set. Unseen events are
// applied; already-seen ids with an equal delta are skipped; an
// already-seen id with a different delta is reported as a Conflict
// and left untouched. The call never partially rolls back: unseen
// events are applied even when the package also carries conflicts.
func (s *Store) Apply(p Package) Receipt {
	r := Receipt{}
	for _, e := range p.Entries {
		applied, seen := s.lookup(p.Key, e.EventID)
		switch {
		case !seen:
			s.put(p.Key, e.EventID, e.Delta)
			r.Applied = append(r.Applied, e.EventID)
		case applied == e.Delta:
			r.Skipped = append(r.Skipped, e.EventID)
		default:
			r.Conflicts = append(r.Conflicts, Conflict{
				Key:      p.Key,
				EventID:  e.EventID,
				Applied:  applied,
				Incoming: e.Delta,
			})
		}
	}
	return r
}

// Total returns the sum of applied deltas for key.
func (s *Store) Total(key string) int64 {
	var sum int64
	for _, d := range s.seen[key] {
		sum += d
	}
	return sum
}

// Has reports whether event id has been applied under key.
func (s *Store) Has(key, id string) bool {
	_, ok := s.lookup(key, id)
	return ok
}

// Events returns the applied event ids for key, unsorted.
func (s *Store) Events(key string) map[string]int64 {
	out := make(map[string]int64, len(s.seen[key]))
	for id, d := range s.seen[key] {
		out[id] = d
	}
	return out
}

func (s *Store) lookup(key, id string) (int64, bool) {
	m, ok := s.seen[key]
	if !ok {
		return 0, false
	}
	d, ok := m[id]
	return d, ok
}

func (s *Store) put(key, id string, delta int64) {
	m, ok := s.seen[key]
	if !ok {
		m = make(map[string]int64)
		s.seen[key] = m
	}
	m[id] = delta
}
