package posetmatch

import (
	"fmt"
	"strings"
)

// CycleError is returned when a precedence relation would close a cycle.
// The mutation is rejected atomically: no edge is added, the version does
// not advance, and all cached evidence keeps its validity.
type CycleError struct {
	Before, After EventID
	Path          []EventID // existing path from After back to Before
}

func (e *CycleError) Error() string {
	var b strings.Builder
	for i, id := range e.Path {
		if i > 0 {
			b.WriteString(" -> ")
		}
		b.WriteString(string(id))
	}
	return fmt.Sprintf("posetmatch: precedence %s -> %s would close cycle via %s",
		e.Before, e.After, b.String())
}

// Evidence is one immutable record in a case's audit log. It captures the
// match outcome computed at Version, and is never deleted: when a later
// mutation refutes it, Valid flips to false and InvalidatedAt records the
// version at which that happened.
type Evidence struct {
	Version       int
	Pattern       []Label
	Found         bool
	Witness       []EventID // full linear extension, when Found
	Start, End    int
	Report        *ExhaustionReport // closed search space, when !Found
	Valid         bool
	InvalidatedAt int
	Note          string

	currentOrder  []EventID // witness restricted to still-existing events
	segmentEvents map[EventID]bool
}

// Segment returns the event IDs that realised the pattern, when Found.
func (e *Evidence) Segment() []EventID {
	if !e.Found {
		return nil
	}
	return e.Witness[e.Start:e.End]
}

// Case is one analysable scenario: a poset plus a versioned evidence log.
// Mutations advance the version and incrementally invalidate exactly the
// evidence whose conclusion can no longer be guaranteed; refuted records
// stay in the log as old-version evidence.
type Case struct {
	name    string
	poset   *Poset
	version int
	log     []*Evidence
	latest  map[string]*Evidence
}

// NewCase starts an empty case at version 0.
func NewCase(name string) *Case {
	return &Case{
		name:   name,
		poset:  NewPoset(),
		latest: make(map[string]*Evidence),
	}
}

// Name returns the case name.
func (c *Case) Name() string { return c.name }

// Version returns the current mutation version.
func (c *Case) Version() int { return c.version }

// HasEdge reports a direct precedence relation.
func (c *Case) HasEdge(from, to EventID) bool { return c.poset.HasEdge(from, to) }

// Has reports whether an event exists.
func (c *Case) Has(id EventID) bool { return c.poset.Has(id) }

// Events lists current events in insertion order.
func (c *Case) Events() []Event { return c.poset.Events() }

// Evidence returns the full audit log, oldest first, including refuted
// records.
func (c *Case) Evidence() []*Evidence { return append([]*Evidence(nil), c.log...) }

// ValidEvidence returns only the records whose conclusion still holds at the
// current version.
func (c *Case) ValidEvidence() []*Evidence {
	var out []*Evidence
	for _, ev := range c.log {
		if ev.Valid {
			out = append(out, ev)
		}
	}
	return out
}

func patternKey(pattern []Label) string {
	var b strings.Builder
	for _, l := range pattern {
		b.WriteString(string(l))
		b.WriteByte(0x1f)
	}
	return b.String()
}

func (c *Case) invalidate(ev *Evidence, note string) {
	ev.Valid = false
	ev.InvalidatedAt = c.version
	ev.Note = note
	if c.latest[patternKey(ev.Pattern)] == ev {
		delete(c.latest, patternKey(ev.Pattern))
	}
}

// Match answers the pattern query for the case. Results are cached per
// pattern and reused across versions until a mutation incrementally
// invalidates them.
func (c *Case) Match(pattern []Label) *Evidence {
	key := patternKey(pattern)
	if ev, ok := c.latest[key]; ok && ev.Valid {
		return ev
	}
	out := c.poset.FindPattern(pattern)
	ev := &Evidence{
		Version: c.version,
		Pattern: append([]Label(nil), pattern...),
		Found:   out.Found,
		Valid:   true,
		Report:  out.Report,
	}
	if out.Found {
		ev.Witness = out.Match.Witness
		ev.Start = out.Match.Start
		ev.End = out.Match.End
		ev.currentOrder = append([]EventID(nil), out.Match.Witness...)
		ev.segmentEvents = make(map[EventID]bool, len(pattern))
		for _, id := range out.Match.Segment() {
			ev.segmentEvents[id] = true
		}
	}
	c.log = append(c.log, ev)
	c.latest[key] = ev
	return ev
}

// AddEvent appends an event. Positive evidence survives: the isolated new
// event can always be appended to the old witness. Negative evidence is
// conservatively invalidated because the new event may enable matches.
func (c *Case) AddEvent(id EventID, label Label, ts int64) error {
	if c.poset.Has(id) {
		return fmt.Errorf("posetmatch: case %q already has event %q", c.name, id)
	}
	if err := c.poset.AddEvent(Event{ID: id, Label: label, Time: ts}); err != nil {
		return err
	}
	c.version++
	for _, ev := range c.log {
		if !ev.Valid {
			continue
		}
		if ev.Found {
			ev.currentOrder = append(ev.currentOrder, id)
		} else {
			c.invalidate(ev, "new event may enable previously impossible matches")
		}
	}
	return nil
}

// RetractEvent removes an event and its precedence relations. Positive
// evidence is invalidated exactly when the retracted event belonged to the
// matched segment; otherwise the witness restricted to the remaining events
// still realises the pattern. Negative evidence is conservatively
// invalidated because retraction relaxes constraints.
func (c *Case) RetractEvent(id EventID) error {
	if !c.poset.Has(id) {
		return fmt.Errorf("posetmatch: case %q has no event %q", c.name, id)
	}
	c.poset.RemoveEvent(id)
	c.version++
	for _, ev := range c.log {
		if !ev.Valid {
			continue
		}
		if !ev.Found {
			c.invalidate(ev, "retraction relaxes constraints; absence no longer proven")
			continue
		}
		if ev.segmentEvents[id] {
			c.invalidate(ev, "retracted event was part of the matched segment")
			continue
		}
		next := ev.currentOrder[:0]
		for _, x := range ev.currentOrder {
			if x != id {
				next = append(next, x)
			}
		}
		ev.currentOrder = next
	}
	return nil
}

// AddPrecedence adds one relation before -> after. If the relation would
// close a cycle it is rejected atomically: nothing changes, including the
// version. Adding a relation only shrinks the space of linear extensions,
// so negative evidence always survives; positive evidence is invalidated
// exactly when its witness violates the new relation.
func (c *Case) AddPrecedence(before, after EventID) error {
	if err := c.checkEndpoints(before, after); err != nil {
		return err
	}
	if c.poset.HasEdge(before, after) {
		return nil // idempotent: relation already present
	}
	if path := c.poset.Path(after, before); path != nil {
		return &CycleError{Before: before, After: after, Path: path}
	}
	c.poset.addEdge(before, after)
	c.version++
	c.invalidateViolators(before, after)
	return nil
}

// AddPrecedences applies a batch of relations atomically: either every
// relation is added (one version bump) or, if any of them — alone or in
// combination — would close a cycle, none is and the case is untouched.
func (c *Case) AddPrecedences(edges [][2]EventID) error {
	sim := c.poset.Clone()
	var fresh [][2]EventID
	for _, e := range edges {
		before, after := e[0], e[1]
		if err := c.checkEndpoints(before, after); err != nil {
			return err
		}
		if sim.HasEdge(before, after) {
			continue
		}
		if path := sim.Path(after, before); path != nil {
			return &CycleError{Before: before, After: after, Path: path}
		}
		sim.addEdge(before, after)
		fresh = append(fresh, e)
	}
	if len(fresh) == 0 {
		return nil
	}
	for _, e := range fresh {
		c.poset.addEdge(e[0], e[1])
	}
	c.version++
	for _, e := range fresh {
		c.invalidateViolators(e[0], e[1])
	}
	return nil
}

func (c *Case) checkEndpoints(before, after EventID) error {
	if before == after {
		return fmt.Errorf("posetmatch: self precedence on %q is a cycle", before)
	}
	if !c.poset.Has(before) {
		return fmt.Errorf("posetmatch: case %q has no event %q", c.name, before)
	}
	if !c.poset.Has(after) {
		return fmt.Errorf("posetmatch: case %q has no event %q", c.name, after)
	}
	return nil
}

// invalidateViolators drops positive evidence whose surviving witness order
// places after before before, which the new relation forbids.
func (c *Case) invalidateViolators(before, after EventID) {
	for _, ev := range c.log {
		if !ev.Valid || !ev.Found {
			continue
		}
		posBefore, posAfter := -1, -1
		for i, id := range ev.currentOrder {
			switch id {
			case before:
				posBefore = i
			case after:
				posAfter = i
			}
		}
		if posBefore < 0 || posAfter < 0 || posAfter < posBefore {
			c.invalidate(ev, fmt.Sprintf("witness violates new precedence %s -> %s", before, after))
		}
	}
}
