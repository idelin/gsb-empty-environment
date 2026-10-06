package rules

import "sort"

// Model is the reference implementation used to cross-check Engine. It
// stores every active candidate and re-sorts the full candidate set on
// every query, trading speed for obvious correctness.
type Model struct {
	active    map[string]map[string]Event // objectID -> eventID -> event
	seen      map[string]Event
	retracted map[string]bool

	Duplicates int
	Conflicts  int
}

// NewModel creates an empty Model.
func NewModel() *Model {
	return &Model{
		active:    make(map[string]map[string]Event),
		seen:      make(map[string]Event),
		retracted: make(map[string]bool),
	}
}

// Apply feeds one operation and returns the effective-value deltas it
// caused (empty or exactly one delta).
func (m *Model) Apply(op Op) []Delta {
	switch op.Kind {
	case OpAdd:
		return m.add(op.Event)
	case OpRetract:
		return m.retract(op.EventID)
	}
	return nil
}

// Effective returns the current winning event for an object by sorting
// the full candidate set.
func (m *Model) Effective(objectID string) (Event, bool) {
	cands := m.sorted(objectID)
	if len(cands) == 0 {
		return Event{}, false
	}
	return cands[0], true
}

func (m *Model) sorted(objectID string) []Event {
	set := m.active[objectID]
	cands := make([]Event, 0, len(set))
	for _, ev := range set {
		cands = append(cands, ev)
	}
	sort.Slice(cands, func(i, j int) bool { return better(cands[i], cands[j]) })
	return cands
}

func (m *Model) add(ev Event) []Delta {
	if prev, ok := m.seen[ev.EventID]; ok {
		if prev == ev {
			m.Duplicates++
		} else {
			m.Conflicts++
		}
		return nil
	}
	m.seen[ev.EventID] = ev
	if m.retracted[ev.EventID] {
		return nil
	}

	oldWinner, hadOld := m.Effective(ev.ObjectID)
	set := m.active[ev.ObjectID]
	if set == nil {
		set = make(map[string]Event)
		m.active[ev.ObjectID] = set
	}
	set[ev.EventID] = ev
	newWinner, hasNew := m.Effective(ev.ObjectID)
	return diffDelta(ev.ObjectID, oldWinner, hadOld, newWinner, hasNew)
}

func (m *Model) retract(eventID string) []Delta {
	if m.retracted[eventID] {
		return nil
	}
	m.retracted[eventID] = true

	ev, ok := m.seen[eventID]
	if !ok {
		// Pending retraction: the event has not arrived yet.
		return nil
	}
	set := m.active[ev.ObjectID]
	if _, live := set[eventID]; !live {
		return nil
	}
	oldWinner, hadOld := m.Effective(ev.ObjectID)
	delete(set, eventID)
	newWinner, hasNew := m.Effective(ev.ObjectID)
	return diffDelta(ev.ObjectID, oldWinner, hadOld, newWinner, hasNew)
}
