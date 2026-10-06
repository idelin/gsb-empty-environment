// Package posetmatch decides whether a label pattern can occur as a
// contiguous substring of some linear extension (topological order) of a
// partially ordered set of events. It never infers order from timestamps:
// matching is decided over the space of linear extensions of the poset.
package posetmatch

import (
	"fmt"
	"sort"
)

// Label is the observable tag attached to an event. Labels need not be
// unique: several events may carry the same label.
type Label string

// EventID uniquely identifies an event inside a poset.
type EventID string

// Event is a labelled point in the partial order. Time is metadata only and
// is never used to decide matches.
type Event struct {
	ID    EventID
	Label Label
	Time  int64
}

// Poset is a finite partially ordered set of events. The zero value is not
// usable; build one with NewPoset.
type Poset struct {
	events map[EventID]Event
	edges  map[EventID]map[EventID]bool // precedence: from -> to
	ids    []EventID                    // insertion order, for determinism
}

// NewPoset returns an empty poset.
func NewPoset() *Poset {
	return &Poset{
		events: make(map[EventID]Event),
		edges:  make(map[EventID]map[EventID]bool),
	}
}

// AddEvent inserts an event. It fails on duplicate IDs.
func (p *Poset) AddEvent(ev Event) error {
	if _, ok := p.events[ev.ID]; ok {
		return fmt.Errorf("posetmatch: duplicate event %q", ev.ID)
	}
	p.events[ev.ID] = ev
	p.edges[ev.ID] = make(map[EventID]bool)
	p.ids = append(p.ids, ev.ID)
	return nil
}

// RemoveEvent deletes an event and all precedence relations touching it.
func (p *Poset) RemoveEvent(id EventID) bool {
	if _, ok := p.events[id]; !ok {
		return false
	}
	delete(p.events, id)
	delete(p.edges, id)
	for _, tos := range p.edges {
		delete(tos, id)
	}
	for i, x := range p.ids {
		if x == id {
			p.ids = append(p.ids[:i], p.ids[i+1:]...)
			break
		}
	}
	return true
}

// Has reports whether the event exists.
func (p *Poset) Has(id EventID) bool {
	_, ok := p.events[id]
	return ok
}

// Event returns the stored event.
func (p *Poset) Event(id EventID) Event { return p.events[id] }

// Events lists events in insertion order.
func (p *Poset) Events() []Event {
	out := make([]Event, 0, len(p.ids))
	for _, id := range p.ids {
		out = append(out, p.events[id])
	}
	return out
}

// Len returns the number of events.
func (p *Poset) Len() int { return len(p.ids) }

// HasEdge reports a direct precedence relation from -> to.
func (p *Poset) HasEdge(from, to EventID) bool { return p.edges[from][to] }

// addEdge inserts a precedence relation without any validation.
func (p *Poset) addEdge(from, to EventID) {
	if p.edges[from] == nil {
		p.edges[from] = make(map[EventID]bool)
	}
	p.edges[from][to] = true
}

// Edges lists direct precedence relations in deterministic order.
func (p *Poset) Edges() [][2]EventID {
	var out [][2]EventID
	for _, from := range p.ids {
		var tos []string
		for to := range p.edges[from] {
			tos = append(tos, string(to))
		}
		sort.Strings(tos)
		for _, to := range tos {
			out = append(out, [2]EventID{from, EventID(to)})
		}
	}
	return out
}

// Clone deep-copies the poset.
func (p *Poset) Clone() *Poset {
	q := NewPoset()
	for _, id := range p.ids {
		q.events[id] = p.events[id]
		q.edges[id] = make(map[EventID]bool, len(p.edges[id]))
		for to := range p.edges[id] {
			q.edges[id][to] = true
		}
		q.ids = append(q.ids, id)
	}
	return q
}

// Path returns one directed path from -> to (both inclusive), or nil if to
// is not reachable from from. from == to yields the trivial path.
func (p *Poset) Path(from, to EventID) []EventID {
	if from == to {
		return []EventID{from}
	}
	prev := map[EventID]EventID{}
	seen := map[EventID]bool{from: true}
	queue := []EventID{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		var nexts []string
		for next := range p.edges[cur] {
			if !seen[next] {
				nexts = append(nexts, string(next))
			}
		}
		sort.Strings(nexts)
		for _, s := range nexts {
			next := EventID(s)
			seen[next] = true
			prev[next] = cur
			if next == to {
				path := []EventID{to}
				for path[0] != from {
					path = append([]EventID{prev[path[0]]}, path...)
				}
				return path
			}
			queue = append(queue, next)
		}
	}
	return nil
}

// Reaches reports whether there is a directed path from -> to.
func (p *Poset) Reaches(from, to EventID) bool { return p.Path(from, to) != nil }

// IsLinearExtension reports whether order is a permutation of all events
// consistent with every precedence relation.
func (p *Poset) IsLinearExtension(order []EventID) bool {
	if len(order) != len(p.ids) {
		return false
	}
	pos := make(map[EventID]int, len(order))
	for i, id := range order {
		if !p.Has(id) {
			return false
		}
		pos[id] = i
	}
	if len(pos) != len(p.ids) {
		return false
	}
	for from, tos := range p.edges {
		for to := range tos {
			if pos[from] > pos[to] {
				return false
			}
		}
	}
	return true
}

// TopoOrder returns one deterministic linear extension (Kahn's algorithm,
// scanning events in insertion order).
func (p *Poset) TopoOrder() []EventID {
	indeg := make(map[EventID]int, len(p.ids))
	for _, id := range p.ids {
		indeg[id] = 0
	}
	for _, tos := range p.edges {
		for to := range tos {
			indeg[to]++
		}
	}
	used := make(map[EventID]bool, len(p.ids))
	order := make([]EventID, 0, len(p.ids))
	for len(order) < len(p.ids) {
		progress := false
		for _, id := range p.ids {
			if used[id] || indeg[id] != 0 {
				continue
			}
			used[id] = true
			order = append(order, id)
			for to := range p.edges[id] {
				indeg[to]--
			}
			progress = true
			break
		}
		if !progress {
			return nil // cycle: no linear extension exists
		}
	}
	return order
}

// LinearExtensions enumerates every linear extension in deterministic order,
// invoking fn with a fresh slice per extension. Enumeration stops early when
// fn returns false or when limit extensions have been produced. It returns
// the number of extensions produced and whether the enumeration ran to
// completion (false means the limit cut it short).
func (p *Poset) LinearExtensions(limit int, fn func(order []EventID) bool) (count int, complete bool) {
	indeg := make(map[EventID]int, len(p.ids))
	for _, id := range p.ids {
		indeg[id] = 0
	}
	for _, tos := range p.edges {
		for to := range tos {
			indeg[to]++
		}
	}
	complete = true
	order := make([]EventID, 0, len(p.ids))
	var rec func() bool
	rec = func() bool {
		if len(order) == len(p.ids) {
			if count >= limit {
				complete = false
				return false
			}
			count++
			cp := make([]EventID, len(order))
			copy(cp, order)
			return fn(cp)
		}
		for _, id := range p.ids {
			if indeg[id] != 0 {
				continue
			}
			indeg[id] = -1 // mark chosen
			for to := range p.edges[id] {
				indeg[to]--
			}
			order = append(order, id)
			cont := rec()
			order = order[:len(order)-1]
			for to := range p.edges[id] {
				indeg[to]++
			}
			indeg[id] = 0
			if !cont {
				return false
			}
		}
		return true
	}
	rec()
	return count, complete
}
