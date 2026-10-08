package backtest

import "time"

// version is one published revision of an event's payload. The original
// event is version 0; each arrived correction appends a new version.
type version struct {
	event       Event
	arrivalTime time.Time
}

// store holds only events that have already arrived. Because the engine
// inserts an event (or correction) into the store exactly when it is
// dispatched, anything still in the queue is physically absent from the
// store: the strategy cannot read the future even by accident.
type store struct {
	versions map[string][]version
}

func newStore() *store {
	return &store{versions: make(map[string][]version)}
}

// apply inserts the event (or the corrected revision) at the current
// engine time. Corrections are appended to the corrected event's history;
// earlier versions are retained so the audit trail stays complete.
func (s *store) apply(ev Event, now time.Time) {
	id := ev.ID
	if ev.Type == EventCorrection {
		id = ev.CorrectsID
	}
	s.versions[id] = append(s.versions[id], version{event: ev, arrivalTime: now})
}

// latest returns the newest revision visible at time now, i.e. the last
// version whose arrival time is not after now.
func (s *store) latest(id string, now time.Time) (Event, bool) {
	revs, ok := s.versions[id]
	if !ok {
		return Event{}, false
	}
	for i := len(revs) - 1; i >= 0; i-- {
		if !revs[i].arrivalTime.After(now) {
			return revs[i].event, true
		}
	}
	return Event{}, false
}
