package replay

import "sync"

// Journal is the durable record shared across executor restarts:
// the event log plus the set of confirmed effect IDs.
type Journal struct {
	mu        sync.Mutex
	events    []Event
	eventIDs  map[string]bool
	confirmed map[string]bool
}

func NewJournal() *Journal {
	return &Journal{
		eventIDs:  make(map[string]bool),
		confirmed: make(map[string]bool),
	}
}

// AppendEvent records an event, reporting false if the event ID was
// already journaled (duplicate delivery).
func (j *Journal) AppendEvent(ev Event) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.eventIDs[ev.ID] {
		return false
	}
	j.eventIDs[ev.ID] = true
	j.events = append(j.events, ev)
	return true
}

func (j *Journal) Events() []Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Event, len(j.events))
	copy(out, j.events)
	return out
}

func (j *Journal) IsConfirmed(effectID string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.confirmed[effectID]
}

func (j *Journal) Confirm(effectID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.confirmed[effectID] = true
}

func (j *Journal) ConfirmedCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.confirmed)
}
