package sim

import "sort"

// ObsKey identifies one observation. Observations sharing a timestamp are
// processed in Seq order.
type ObsKey struct {
	Time int
	Seq  int
}

type EventKind int

const (
	EventObserve EventKind = iota
	EventRevise
)

// Event is one entry of the arrival-ordered event log. A revision targets
// the same key as the observation it corrects.
type Event struct {
	Kind  EventKind
	Key   ObsKey
	Value int
}

// FactVersion is one immutable version of an observation's value.
type FactVersion struct {
	Version int
	Value   int
}

// FactStore keeps the full version history of every observed fact.
type FactStore struct {
	history map[ObsKey][]FactVersion
}

func NewFactStore() *FactStore {
	return &FactStore{history: make(map[ObsKey][]FactVersion)}
}

// Apply ingests one event. It reports whether the store changed and the
// version of the affected fact. Duplicate observations are ignored so
// replaying the same event is idempotent; revising an unknown key is a
// no-op.
func (s *FactStore) Apply(ev Event) (bool, int) {
	switch ev.Kind {
	case EventObserve:
		if _, dup := s.history[ev.Key]; dup {
			return false, 0
		}
		s.history[ev.Key] = []FactVersion{{Version: 1, Value: ev.Value}}
		return true, 1
	case EventRevise:
		versions, ok := s.history[ev.Key]
		if !ok {
			return false, 0
		}
		next := versions[len(versions)-1].Version + 1
		s.history[ev.Key] = append(versions, FactVersion{Version: next, Value: ev.Value})
		return true, next
	}
	return false, 0
}

// Versions returns the version history of one fact, oldest first.
func (s *FactStore) Versions(key ObsKey) []FactVersion {
	return append([]FactVersion(nil), s.history[key]...)
}

// Observation is the newest value of one fact.
type Observation struct {
	Key   ObsKey
	Value int
}

// Latest returns the newest version of every fact ordered by (Time, Seq),
// which is the processing order for observations sharing a timestamp.
func (s *FactStore) Latest() []Observation {
	obs := make([]Observation, 0, len(s.history))
	for key, versions := range s.history {
		obs = append(obs, Observation{Key: key, Value: versions[len(versions)-1].Value})
	}
	SortObservations(obs)
	return obs
}

// SortObservations orders observations by time, then by sequence number.
func SortObservations(obs []Observation) {
	sort.Slice(obs, func(i, j int) bool {
		if obs[i].Key.Time != obs[j].Key.Time {
			return obs[i].Key.Time < obs[j].Key.Time
		}
		return obs[i].Key.Seq < obs[j].Key.Seq
	})
}
