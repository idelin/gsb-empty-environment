// Package poset analyzes partially ordered event logs. Given a set of
// labeled events and precedence constraints, it decides whether a label
// pattern can appear as a contiguous substring of some topological order
// (linear extension) of the poset. Because concurrent events may be
// interleaved arbitrarily, the answer cannot be obtained by sorting events
// by time; the space of linear extensions must be searched.
//
// Cases support incremental maintenance: events may be retracted and
// precedence relations added or removed. Every mutation invalidates the
// previously computed matches while the old-version evidence is retained
// in the case history. Adding a precedence relation that would close a
// cycle is rejected atomically, leaving the case untouched.
package poset

import (
	"errors"
	"fmt"
	"strings"
)

// EventID uniquely identifies an event within a Case.
type EventID string

// Event is a single labeled occurrence in a case.
type Event struct {
	ID    EventID
	Label string
}

// Edge is a precedence constraint: Before must precede After in every
// topological order of the case.
type Edge struct {
	Before EventID
	After  EventID
}

var (
	// ErrEventExists is returned when adding an event whose ID is taken.
	ErrEventExists = errors.New("poset: event already exists")
	// ErrEventMissing is returned when referencing an unknown event.
	ErrEventMissing = errors.New("poset: event does not exist")
	// ErrEdgeMissing is returned when removing a precedence that is not set.
	ErrEdgeMissing = errors.New("poset: precedence relation does not exist")
	// ErrEmptyPattern is returned when matching against an empty pattern.
	ErrEmptyPattern = errors.New("poset: pattern must not be empty")
)

// CycleError reports a precedence addition that would close a cycle. The
// addition is rejected atomically: no state of the case is modified.
type CycleError struct {
	// Path is the offending cycle, e.g. [a b c a] for a -> b -> c -> a.
	Path []EventID
}

func (e *CycleError) Error() string {
	parts := make([]string, len(e.Path))
	for i, id := range e.Path {
		parts[i] = string(id)
	}
	return fmt.Sprintf("poset: precedence rejected, would create cycle: %s",
		strings.Join(parts, " -> "))
}

// Options bound the enumeration performed by Match.
type Options struct {
	// MaxExtensions caps the number of linear extensions examined. When
	// the cap is hit the search is truncated and the resulting
	// SearchReport has Closed == false.
	MaxExtensions uint64
	// RetainExtensions caps how many explored linear extensions are kept
	// in SearchReport.Extensions as evidence of the closed search range.
	RetainExtensions uint64
}

// DefaultOptions returns limits suitable for cases of up to 12 events.
func DefaultOptions() Options {
	return Options{MaxExtensions: 2_000_000, RetainExtensions: 20_000}
}

// Case is a set of labeled events plus precedence constraints, with
// versioned match evidence.
type Case struct {
	name    string
	events  map[EventID]Event
	order   []EventID // insertion order, keeps enumeration deterministic
	edges   map[EventID]map[EventID]struct{}
	version uint64
	opts    Options
	history []Snapshot
}

// NewCase creates an empty case.
func NewCase(name string) *Case {
	return &Case{
		name:   name,
		events: make(map[EventID]Event),
		edges:  make(map[EventID]map[EventID]struct{}),
		opts:   DefaultOptions(),
	}
}

// SetOptions replaces the enumeration limits.
func (c *Case) SetOptions(opts Options) { c.opts = opts }

// Version is the current mutation counter of the case.
func (c *Case) Version() uint64 { return c.version }

// Events returns the events in deterministic (insertion) order.
func (c *Case) Events() []Event {
	out := make([]Event, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, c.events[id])
	}
	return out
}

// Edges returns the precedence relations in deterministic order.
func (c *Case) Edges() []Edge {
	var out []Edge
	for _, u := range c.order {
		for _, v := range c.order {
			if _, ok := c.edges[u][v]; ok {
				out = append(out, Edge{Before: u, After: v})
			}
		}
	}
	return out
}

// AddEvent inserts a new labeled event. It invalidates existing matches.
func (c *Case) AddEvent(id EventID, label string) error {
	if _, ok := c.events[id]; ok {
		return fmt.Errorf("%w: %s", ErrEventExists, id)
	}
	c.events[id] = Event{ID: id, Label: label}
	c.order = append(c.order, id)
	c.bump(fmt.Sprintf("event %s added", id))
	return nil
}

// RetractEvent removes an event and all its precedence relations. Existing
// matches are invalidated incrementally; their evidence stays in History.
func (c *Case) RetractEvent(id EventID) error {
	if _, ok := c.events[id]; !ok {
		return fmt.Errorf("%w: %s", ErrEventMissing, id)
	}
	delete(c.events, id)
	delete(c.edges, id)
	for u := range c.edges {
		delete(c.edges[u], id)
	}
	for i, e := range c.order {
		if e == id {
			c.order = append(c.order[:i:i], c.order[i+1:]...)
			break
		}
	}
	c.bump(fmt.Sprintf("event %s retracted", id))
	return nil
}

// AddPrecedence records that before must precede after. If the new relation
// would close a cycle the addition is rejected atomically: the case, its
// version, and the validity of existing snapshots are left untouched.
func (c *Case) AddPrecedence(before, after EventID) error {
	if _, ok := c.events[before]; !ok {
		return fmt.Errorf("%w: %s", ErrEventMissing, before)
	}
	if _, ok := c.events[after]; !ok {
		return fmt.Errorf("%w: %s", ErrEventMissing, after)
	}
	if before == after {
		return &CycleError{Path: []EventID{before, after}}
	}
	if p, ok := c.path(after, before); ok {
		return &CycleError{Path: append([]EventID{before}, p...)}
	}
	if c.edges[before] == nil {
		c.edges[before] = make(map[EventID]struct{})
	}
	if _, dup := c.edges[before][after]; dup {
		return nil // already implied relation is set; no state change
	}
	c.edges[before][after] = struct{}{}
	c.bump(fmt.Sprintf("precedence %s -> %s added", before, after))
	return nil
}

// RemovePrecedence drops an existing precedence relation and invalidates
// existing matches.
func (c *Case) RemovePrecedence(before, after EventID) error {
	outs, ok := c.edges[before]
	if !ok {
		return ErrEdgeMissing
	}
	if _, ok := outs[after]; !ok {
		return ErrEdgeMissing
	}
	delete(outs, after)
	c.bump(fmt.Sprintf("precedence %s -> %s removed", before, after))
	return nil
}

// bump advances the version and incrementally invalidates every still-valid
// snapshot. Invalidated snapshots are retained as old-version evidence.
func (c *Case) bump(reason string) {
	c.version++
	for i := range c.history {
		if c.history[i].Valid {
			c.history[i].Valid = false
			c.history[i].InvalidatedBy = fmt.Sprintf("%s (version %d)", reason, c.version)
		}
	}
}

// path returns a directed path from -> to following precedence edges, in
// deterministic order, or ok == false if none exists.
func (c *Case) path(from, to EventID) ([]EventID, bool) {
	visited := make(map[EventID]bool, len(c.order))
	var trail []EventID
	var dfs func(u EventID) bool
	dfs = func(u EventID) bool {
		trail = append(trail, u)
		if u == to {
			return true
		}
		visited[u] = true
		for _, id := range c.order {
			if _, ok := c.edges[u][id]; !ok || visited[id] {
				continue
			}
			if dfs(id) {
				return true
			}
		}
		trail = trail[:len(trail)-1]
		return false
	}
	if dfs(from) {
		return trail, true
	}
	return nil, false
}

// History returns all snapshots ever computed, newest last, including
// invalidated old-version evidence.
func (c *Case) History() []Snapshot {
	out := make([]Snapshot, len(c.history))
	copy(out, c.history)
	return out
}

// Current returns the newest still-valid snapshot, if any.
func (c *Case) Current() (Snapshot, bool) {
	for i := len(c.history) - 1; i >= 0; i-- {
		if c.history[i].Valid {
			return c.history[i], true
		}
	}
	return Snapshot{}, false
}
