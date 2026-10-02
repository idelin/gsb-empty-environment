// Package itc implements Interval Tree Clocks (ITC), a decentralized
// logical clock for dynamic systems.
//
// ITC tracks causality without any central coordinator and without a
// pre-configured list of cluster nodes. The time interval [0, 1] is
// recursively split: a clock may Fork into two clocks that each own a
// disjoint half of the interval, and two clocks owning disjoint
// intervals may Join back into one. Nodes can therefore join and leave
// the system at any time with zero configuration.
//
// Reference: Almeida, Baquero, Fonte, "Interval Tree Clocks: A Logical
// Clock for Dynamic Systems" (2008).
package itc

import (
	"errors"
	"fmt"
)

// ErrOverlappingIntervals is returned by Join when the two clocks own
// overlapping (non-disjoint) id intervals, i.e. they are not the result
// of a legitimate fork/join lineage.
var ErrOverlappingIntervals = errors.New("itc: cannot join clocks with overlapping id intervals")

// ---------------------------------------------------------------------------
// ID component: a binary tree describing which parts of [0, 1] this clock owns.
// ---------------------------------------------------------------------------

type idKind int

const (
	idZero idKind = iota // owns nothing
	idOne                // owns the whole interval
	idNode               // owns left/right sub-intervals
)

// ID is the identity component of a Stamp. It is an immutable value;
// all operations return new trees.
type ID struct {
	kind        idKind
	left, right *ID
}

func zeroID() *ID { return &ID{kind: idZero} }
func oneID() *ID  { return &ID{kind: idOne} }

// nodeID builds (l, r), normalizing (0,0)->0 and (1,1)->1.
func nodeID(l, r *ID) *ID {
	switch {
	case l.kind == idZero && r.kind == idZero:
		return zeroID()
	case l.kind == idOne && r.kind == idOne:
		return oneID()
	default:
		return &ID{kind: idNode, left: l, right: r}
	}
}

func (i *ID) isZero() bool { return i.kind == idZero }

// splitID divides an id into two ids covering disjoint halves.
//
//	split(0)       = (0, 0)
//	split(1)       = ((1,0), (0,1))
//	split((0, i))  = ((0, i1), (0, i2))  where (i1, i2) = split(i)
//	split((i, 0))  = ((i1, 0), (i2, 0))  where (i1, i2) = split(i)
//	split((i, j))  = ((i, 0), (0, j))
func splitID(i *ID) (*ID, *ID) {
	switch i.kind {
	case idZero:
		return zeroID(), zeroID()
	case idOne:
		return nodeID(oneID(), zeroID()), nodeID(zeroID(), oneID())
	default:
		l, r := i.left, i.right
		switch {
		case l.isZero():
			r1, r2 := splitID(r)
			return nodeID(zeroID(), r1), nodeID(zeroID(), r2)
		case r.isZero():
			l1, l2 := splitID(l)
			return nodeID(l1, zeroID()), nodeID(l2, zeroID())
		default:
			return nodeID(l, zeroID()), nodeID(zeroID(), r)
		}
	}
}

// sumID merges two disjoint ids. It fails if the intervals overlap.
func sumID(a, b *ID) (*ID, error) {
	if a.isZero() {
		return b, nil
	}
	if b.isZero() {
		return a, nil
	}
	if a.kind == idOne || b.kind == idOne {
		return nil, ErrOverlappingIntervals
	}
	l, err := sumID(a.left, b.left)
	if err != nil {
		return nil, err
	}
	r, err := sumID(a.right, b.right)
	if err != nil {
		return nil, err
	}
	return nodeID(l, r), nil
}

func (i *ID) equal(o *ID) bool {
	if i.kind != o.kind {
		return false
	}
	if i.kind != idNode {
		return true
	}
	return i.left.equal(o.left) && i.right.equal(o.right)
}

func (i *ID) String() string {
	switch i.kind {
	case idZero:
		return "0"
	case idOne:
		return "1"
	default:
		return fmt.Sprintf("(%s, %s)", i.left, i.right)
	}
}

// ---------------------------------------------------------------------------
// Event component: a binary tree of counters matching the id structure.
// A leaf n means "n events happened everywhere in the owned interval";
// a node (n, l, r) means base n plus l on the left half and r on the right.
// Invariant (normalized form): at least one child has minimum value 0.
// ---------------------------------------------------------------------------

// Event is the event component of a Stamp. It is an immutable value.
type Event struct {
	value       int
	left, right *Event // nil => leaf
}

func leafEvent(n int) *Event { return &Event{value: n} }

func nodeEvent(n int, l, r *Event) *Event { return &Event{value: n, left: l, right: r} }

func (e *Event) isLeaf() bool { return e.left == nil }

func (e *Event) clone() *Event {
	if e.isLeaf() {
		return leafEvent(e.value)
	}
	return nodeEvent(e.value, e.left.clone(), e.right.clone())
}

// lift adds d to the base of the tree (i.e. to every position).
func liftEvent(d int, e *Event) *Event {
	if e.isLeaf() {
		return leafEvent(e.value + d)
	}
	return nodeEvent(e.value+d, e.left, e.right)
}

// sink subtracts d from the base of the tree.
func sinkEvent(d int, e *Event) *Event {
	if e.isLeaf() {
		return leafEvent(e.value - d)
	}
	return nodeEvent(e.value-d, e.left, e.right)
}

// minEvent returns the minimum counter value over the whole interval.
// For normalized trees this is exactly the base value.
func minEvent(e *Event) int { return e.value }

// normEvent restores the normalized form: it pulls the common minimum of
// both children up into the base, and collapses (n, m, m) into leaf n+m.
func normEvent(e *Event) *Event {
	if e.isLeaf() {
		return e
	}
	l, r := e.left, e.right
	m := minEvent(l)
	if minEvent(r) < m {
		m = minEvent(r)
	}
	if m > 0 {
		e = nodeEvent(e.value+m, sinkEvent(m, l), sinkEvent(m, r))
		l, r = e.left, e.right
	}
	if l.isLeaf() && r.isLeaf() && l.value == r.value {
		return leafEvent(e.value + l.value)
	}
	return e
}

// joinEvents computes the pointwise maximum of two event trees.
func joinEvents(a, b *Event) *Event {
	switch {
	case a.isLeaf() && b.isLeaf():
		if a.value >= b.value {
			return leafEvent(a.value)
		}
		return leafEvent(b.value)
	case a.isLeaf():
		return joinEvents(nodeEvent(a.value, leafEvent(0), leafEvent(0)), b)
	case b.isLeaf():
		return joinEvents(a, nodeEvent(b.value, leafEvent(0), leafEvent(0)))
	default:
		if a.value > b.value {
			return joinEvents(b, a)
		}
		d := b.value - a.value
		return normEvent(nodeEvent(a.value,
			joinEvents(a.left, liftEvent(d, b.left)),
			joinEvents(a.right, liftEvent(d, b.right))))
	}
}

// recordEvent increments the event tree at the positions owned by id.
func recordEvent(id *ID, e *Event) *Event {
	switch {
	case id.kind == idOne:
		if e.isLeaf() {
			return leafEvent(e.value + 1)
		}
		return nodeEvent(e.value+1, e.left, e.right)
	case e.isLeaf():
		// Expand the leaf so the id tree has room to descend.
		return recordEvent(id, nodeEvent(e.value, leafEvent(0), leafEvent(0)))
	case id.left.isZero():
		return normEvent(nodeEvent(e.value, e.left, recordEvent(id.right, e.right)))
	default:
		return normEvent(nodeEvent(e.value, recordEvent(id.left, e.left), e.right))
	}
}

// leqEvent reports whether a is less than or equal to b at every position.
func leqEvent(a, b *Event) bool {
	switch {
	case a.isLeaf() && b.isLeaf():
		return a.value <= b.value
	case a.isLeaf():
		// b's minimum value is its base.
		return a.value <= b.value
	case b.isLeaf():
		return leqEvent(a, nodeEvent(b.value, leafEvent(0), leafEvent(0)))
	default:
		if a.value > b.value {
			return false
		}
		d := b.value - a.value
		return leqEvent(a.left, liftEvent(d, b.left)) &&
			leqEvent(a.right, liftEvent(d, b.right))
	}
}
func (e *Event) equal(o *Event) bool {
	if e.isLeaf() != o.isLeaf() {
		return false
	}
	if e.isLeaf() {
		return e.value == o.value
	}
	return e.value == o.value && e.left.equal(o.left) && e.right.equal(o.right)
}

func (e *Event) String() string {
	if e.isLeaf() {
		return fmt.Sprintf("%d", e.value)
	}
	return fmt.Sprintf("(%d, %s, %s)", e.value, e.left, e.right)
}

// ---------------------------------------------------------------------------
// Stamp: id + event, the full logical clock.
// ---------------------------------------------------------------------------

// Order is the causal relationship between two stamps.
type Order int

const (
	// Concurrent means neither stamp happened-before the other.
	Concurrent Order = iota
	// Equal means both stamps describe the same causal past.
	Equal
	// Less means the receiver happened-before the argument.
	Less
	// Greater means the argument happened-before the receiver.
	Greater
)

func (o Order) String() string {
	switch o {
	case Concurrent:
		return "concurrent"
	case Equal:
		return "equal"
	case Less:
		return "less (happened-before)"
	case Greater:
		return "greater (happened-after)"
	default:
		return "unknown"
	}
}

// Stamp is an Interval Tree Clock. It is safe to copy by value, but
// Event() mutates the receiver, so treat a Stamp as owned by one node.
type Stamp struct {
	id    *ID
	event *Event
}

// Seed returns a fresh clock holding the complete time interval [0, 1].
// Exactly one Seed (or a fork/join lineage of one) should exist per
// causal domain; new nodes join the system by Fork, never by Seed.
func Seed() *Stamp {
	return &Stamp{id: oneID(), event: leafEvent(0)}
}

// Fork splits the clock into two clocks, each owning a disjoint half of
// the interval and both inheriting the full causal history. This is how
// a new node joins the system: an existing node forks and hands one of
// the two stamps to the newcomer.
func (s *Stamp) Fork() (*Stamp, *Stamp) {
	i1, i2 := splitID(s.id)
	return &Stamp{id: i1, event: s.event.clone()},
		&Stamp{id: i2, event: s.event.clone()}
}

// Join merges two clocks that own disjoint intervals (typically produced
// by an earlier Fork). The result owns the union of both intervals and
// precisely preserves the causal history of both sides.
func Join(a, b *Stamp) (*Stamp, error) {
	id, err := sumID(a.id, b.id)
	if err != nil {
		return nil, err
	}
	return &Stamp{id: id, event: joinEvents(a.event, b.event)}, nil
}

// MustJoin is like Join but panics on overlapping intervals.
func MustJoin(a, b *Stamp) *Stamp {
	s, err := Join(a, b)
	if err != nil {
		panic(err)
	}
	return s
}

// Event records a local event (e.g. a state change or a message send),
// advancing the clock. It panics on an anonymous (Peek) stamp, which
// owns no interval and therefore cannot record events.
func (s *Stamp) Event() {
	if s.id.isZero() {
		panic("itc: cannot record an event on an anonymous (peek) stamp")
	}
	s.event = recordEvent(s.id, s.event)
}

// Peek returns an anonymous copy of the clock: same causal history but
// owning no interval, so it can be compared but cannot record events.
// Useful for read-only snapshots attached to messages.
func (s *Stamp) Peek() *Stamp {
	return &Stamp{id: zeroID(), event: s.event.clone()}
}

// Clone returns an exact copy of the stamp.
func (s *Stamp) Clone() *Stamp {
	return &Stamp{id: s.id, event: s.event.clone()}
}

// Leq reports whether s happened-before or is equal to o.
func (s *Stamp) Leq(o *Stamp) bool { return leqEvent(s.event, o.event) }

// Compare returns the causal relationship between a and b.
func Compare(a, b *Stamp) Order {
	ab, ba := a.Leq(b), b.Leq(a)
	switch {
	case ab && ba:
		return Equal
	case ab:
		return Less
	case ba:
		return Greater
	default:
		return Concurrent
	}
}

// Equal reports whether both stamps are identical (id and event).
func (s *Stamp) Equal(o *Stamp) bool {
	return s.id.equal(o.id) && s.event.equal(o.event)
}

// ID returns a human-readable form of the id (interval ownership) tree.
func (s *Stamp) ID() string { return s.id.String() }

// EventTree returns a human-readable form of the event tree.
func (s *Stamp) EventTree() string { return s.event.String() }

func (s *Stamp) String() string {
	return fmt.Sprintf("{id: %s, event: %s}", s.id, s.event)
}
