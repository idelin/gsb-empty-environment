package rules

import "container/heap"

// Engine is the incremental implementation. It keeps one lazy-deletion
// max-heap of candidates per object so the winner is tracked in O(log n)
// per operation instead of re-sorting all candidates.
type Engine struct {
	objects   map[string]*objectState // objectID -> per-object state
	seen      map[string]Event        // eventID -> canonical (first accepted) event
	retracted map[string]bool         // tombstones, incl. retractions that arrived early
	byEvent   map[string]string       // eventID -> objectID

	// Duplicates counts identical re-deliveries of an already seen event id.
	Duplicates int
	// Conflicts counts deliveries whose event id was already seen with a
	// different payload/priority/seq; the first version always wins.
	Conflicts int
}

// NewEngine creates an empty Engine.
func NewEngine() *Engine {
	return &Engine{
		objects:   make(map[string]*objectState),
		seen:      make(map[string]Event),
		retracted: make(map[string]bool),
		byEvent:   make(map[string]string),
	}
}

// Apply feeds one operation and returns the effective-value deltas it
// caused (empty or exactly one delta).
func (e *Engine) Apply(op Op) []Delta {
	switch op.Kind {
	case OpAdd:
		return e.add(op.Event)
	case OpRetract:
		return e.retract(op.EventID)
	}
	return nil
}

// Effective returns the current winning event for an object.
func (e *Engine) Effective(objectID string) (Event, bool) {
	st, ok := e.objects[objectID]
	if !ok {
		return Event{}, false
	}
	return st.winner()
}

func (e *Engine) add(ev Event) []Delta {
	if prev, ok := e.seen[ev.EventID]; ok {
		if prev == ev {
			e.Duplicates++
		} else {
			// Same event id, different content: conflict. First wins.
			e.Conflicts++
		}
		return nil
	}
	e.seen[ev.EventID] = ev
	e.byEvent[ev.EventID] = ev.ObjectID

	if e.retracted[ev.EventID] {
		// Retraction arrived first: the event is dead on arrival.
		return nil
	}

	st := e.stateFor(ev.ObjectID)
	oldWinner, hadOld := st.winner()
	st.active[ev.EventID] = ev
	heap.Push(&st.heap, ev)
	newWinner, hasNew := st.winner()
	return diffDelta(ev.ObjectID, oldWinner, hadOld, newWinner, hasNew)
}

func (e *Engine) retract(eventID string) []Delta {
	if e.retracted[eventID] {
		// Duplicate retraction: no-op.
		return nil
	}
	e.retracted[eventID] = true

	objectID, ok := e.byEvent[eventID]
	if !ok {
		// Event not seen yet: record a pending retraction.
		return nil
	}
	st := e.objects[objectID]
	if _, active := st.active[eventID]; !active {
		return nil
	}
	oldWinner, hadOld := st.winner()
	delete(st.active, eventID)
	st.retired[eventID] = true
	newWinner, hasNew := st.winner()
	return diffDelta(objectID, oldWinner, hadOld, newWinner, hasNew)
}

func (e *Engine) stateFor(objectID string) *objectState {
	st, ok := e.objects[objectID]
	if !ok {
		st = &objectState{
			active:  make(map[string]Event),
			retired: make(map[string]bool),
		}
		e.objects[objectID] = st
	}
	return st
}

// objectState holds the live candidates of one object plus a max-heap with
// lazy deletion: retracted entries are tombstoned in retired and popped
// only when they surface at the top.
type objectState struct {
	active  map[string]Event
	retired map[string]bool
	heap    candidateHeap
}

func (s *objectState) winner() (Event, bool) {
	for len(s.heap) > 0 {
		top := s.heap[0]
		if s.retired[top.EventID] {
			heap.Pop(&s.heap)
			continue
		}
		if cur, ok := s.active[top.EventID]; ok && cur == top {
			return top, true
		}
		heap.Pop(&s.heap)
	}
	return Event{}, false
}

// diffDelta emits a delta when the effective winner changed.
func diffDelta(objectID string, oldW Event, hadOld bool, newW Event, hasNew bool) []Delta {
	if hadOld && hasNew && oldW.EventID == newW.EventID {
		return nil
	}
	if !hadOld && !hasNew {
		return nil
	}
	d := Delta{ObjectID: objectID, HadOld: hadOld, HasNew: hasNew}
	if hadOld {
		d.OldPayload = oldW.Payload
	}
	if hasNew {
		d.NewPayload = newW.Payload
	}
	return []Delta{d}
}

// candidateHeap is a max-heap ordered by better().
type candidateHeap []Event

func (h candidateHeap) Len() int            { return len(h) }
func (h candidateHeap) Less(i, j int) bool  { return better(h[i], h[j]) }
func (h candidateHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(x interface{}) { *h = append(*h, x.(Event)) }
func (h *candidateHeap) Pop() interface{} {
	old := *h
	n := len(old)
	top := old[n-1]
	*h = old[:n-1]
	return top
}
