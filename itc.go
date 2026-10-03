// Package itc implements Interval Tree Clocks (ITC), a decentralized
// logical clock for dynamic systems.
//
// ITC tracks causality without any centralized coordinator and without a
// preconfigured list of cluster nodes. Nodes may join and leave at any
// time: the time interval [0, 1] is recursively split (Fork) and merged
// (Join) so that every live node always owns a disjoint slice of the
// interval, while the event tree records the causal history.
//
// Reference: Paulo Sergio Almeida, Carlos Baquero, Victor Fonte,
// "Interval Tree Clocks: A Logical Clock for Dynamic Systems", SRDS 2008.
package itc

import "fmt"

// ---------------------------------------------------------------------------
// ID: a binary tree describing which slices of the [0, 1] interval a node
// owns. A leaf 1 means "owns everything", a leaf 0 "owns nothing", and an
// inner node (l, r) splits ownership between the two halves.
// ---------------------------------------------------------------------------

// ID is the identity component of a Stamp. It is immutable; all operations
// return new values.
type ID struct {
	leaf  bool
	value int // 0 or 1, valid only when leaf is true
	left  *ID
	right *ID
}

func zeroID() *ID { return &ID{leaf: true, value: 0} }
func oneID() *ID  { return &ID{leaf: true, value: 1} }

func (i *ID) isZero() bool { return i.leaf && i.value == 0 }
func (i *ID) isOne() bool  { return i.leaf && i.value == 1 }

// Clone returns a deep copy of the ID.
func (i *ID) Clone() *ID {
	if i == nil {
		return nil
	}
	c := *i
	if !i.leaf {
		c.left = i.left.Clone()
		c.right = i.right.Clone()
	}
	return &c
}

// normID collapses (0, 0) -> 0 and (1, 1) -> 1.
func normID(i *ID) *ID {
	if !i.leaf {
		if i.left.isZero() && i.right.isZero() {
			return zeroID()
		}
		if i.left.isOne() && i.right.isOne() {
			return oneID()
		}
	}
	return i
}

// forkID splits an ID into two IDs whose ownership sums to the original.
func forkID(i *ID) (*ID, *ID) {
	switch {
	case i.isZero():
		return i.Clone(), i.Clone()
	case i.isOne():
		return &ID{left: oneID(), right: zeroID()},
			&ID{left: zeroID(), right: oneID()}
	case i.left.isZero():
		l, r := forkID(i.right)
		return &ID{left: zeroID(), right: l},
			&ID{left: zeroID(), right: r}
	case i.right.isZero():
		l, r := forkID(i.left)
		return &ID{left: l, right: zeroID()},
			&ID{left: r, right: zeroID()}
	default:
		return &ID{left: i.left.Clone(), right: zeroID()},
			&ID{left: zeroID(), right: i.right.Clone()}
	}
}

// sumID merges two disjoint IDs back together. It panics if the IDs
// overlap, which can only happen when stamps were not properly forked.
func sumID(a, b *ID) *ID {
	switch {
	case a.isZero():
		return b.Clone()
	case b.isZero():
		return a.Clone()
	case a.leaf || b.leaf:
		panic("itc: cannot join overlapping identities")
	default:
		return normID(&ID{left: sumID(a.left, b.left), right: sumID(a.right, b.right)})
	}
}

// String renders the ID as 0, 1 or (l, r), e.g. "((1, 0), 0)".
func (i *ID) String() string {
	if i.leaf {
		return fmt.Sprintf("%d", i.value)
	}
	return fmt.Sprintf("(%s, %s)", i.left, i.right)
}

// ---------------------------------------------------------------------------
// Event: a tree of counters. A leaf n is a plain counter; an inner node
// (n, l, r) has base counter n with l and r relative to it, so the absolute
// counter along a path is the sum of the bases on that path.
// ---------------------------------------------------------------------------

// Event is the causal-history component of a Stamp. It is immutable.
type Event struct {
	leaf  bool
	value int
	left  *Event
	right *Event
}

func leafEvent(v int) *Event { return &Event{leaf: true, value: v} }

// Clone returns a deep copy of the Event.
func (e *Event) Clone() *Event {
	if e == nil {
		return nil
	}
	c := *e
	if !e.leaf {
		c.left = e.left.Clone()
		c.right = e.right.Clone()
	}
	return &c
}

// normEvent collapses (n, m, m) -> n+m when both children are equal leaves.
func normEvent(e *Event) *Event {
	if !e.leaf && e.left.leaf && e.right.leaf && e.left.value == e.right.value {
		return leafEvent(e.value + e.left.value)
	}
	return e
}

// maxEvent returns the largest absolute counter contained in the tree.
func maxEvent(e *Event) int {
	if e.leaf {
		return e.value
	}
	m := maxEvent(e.left)
	if r := maxEvent(e.right); r > m {
		m = r
	}
	return e.value + m
}

// costEvent is the sum of all counters; used to pick the cheaper grow.
func costEvent(e *Event) int {
	if e.leaf {
		return e.value
	}
	return e.value + costEvent(e.left) + costEvent(e.right)
}

// fill inflates the counters owned by id as much as possible without
// introducing new tree structure.
func fill(id *ID, e *Event) *Event {
	switch {
	case id.isZero():
		return e.Clone()
	case id.isOne():
		return leafEvent(maxEvent(e))
	case e.leaf:
		return e.Clone()
	case id.left.isOne():
		return normEvent(&Event{
			value: e.value,
			left:  leafEvent(maxEvent(e.left)),
			right: fill(id.right, e.right),
		})
	case id.right.isOne():
		return normEvent(&Event{
			value: e.value,
			left:  fill(id.left, e.left),
			right: leafEvent(maxEvent(e.right)),
		})
	default:
		return normEvent(&Event{
			value: e.value,
			left:  fill(id.left, e.left),
			right: fill(id.right, e.right),
		})
	}
}

// grow increments the counters owned by id, extending the tree where
// needed. It returns the new event tree and its cost.
func grow(id *ID, e *Event) (*Event, int) {
	switch {
	case id.isOne():
		if e.leaf {
			return leafEvent(e.value + 1), e.value + 1
		}
		m := maxEvent(e)
		return leafEvent(m + 1), m + 1
	case id.isZero():
		// No ownership in this region; unreachable for well-formed stamps.
		return e.Clone(), costEvent(e)
	case e.leaf:
		// Lift the leaf to a node so the ID tree can be descended.
		return grow(id, &Event{value: e.value, left: leafEvent(0), right: leafEvent(0)})
	case id.left.isZero():
		r, c := grow(id.right, e.right)
		return normEvent(&Event{value: e.value, left: e.left.Clone(), right: r}),
			e.value + costEvent(e.left) + c
	case id.right.isZero():
		l, c := grow(id.left, e.left)
		return normEvent(&Event{value: e.value, left: l, right: e.right.Clone()}),
			e.value + c + costEvent(e.right)
	default:
		l, cl := grow(id.left, e.left)
		r, cr := grow(id.right, e.right)
		if cl <= cr {
			return normEvent(&Event{value: e.value, left: l, right: e.right.Clone()}),
				e.value + cl + costEvent(e.right)
		}
		return normEvent(&Event{value: e.value, left: e.left.Clone(), right: r}),
			e.value + costEvent(e.left) + cr
	}
}

// event records one local event: fill if that already advances the clock,
// otherwise grow.
func event(id *ID, e *Event) *Event {
	f := fill(id, e)
	if leqEvent(e, f) && !equalEvent(f, e) {
		return f
	}
	g, _ := grow(id, e)
	return g
}

// liftLeaf views a leaf counter n as the node (n, 0, 0).
func liftLeaf(e *Event) *Event {
	if !e.leaf {
		return e
	}
	return &Event{value: e.value, left: leafEvent(0), right: leafEvent(0)}
}

// liftEvent adds d to every absolute counter in e. For an inner node it
// suffices to raise the base, since children are relative to it.
func liftEvent(e *Event, d int) *Event {
	if d == 0 {
		return e
	}
	if e.leaf {
		return leafEvent(e.value + d)
	}
	return &Event{value: e.value + d, left: e.left, right: e.right}
}

// leqEvent reports whether every counter in a is <= the corresponding
// counter in b (the causal order on event trees). Children of the side
// with the smaller base are compared against the other side's children
// lifted by the base difference, because counters are relative to their
// base.
func leqEvent(a, b *Event) bool {
	if a.leaf && b.leaf {
		return a.value <= b.value
	}
	an := liftLeaf(a)
	bn := liftLeaf(b)
	if an.value > bn.value {
		return false
	}
	d := bn.value - an.value
	return leqEvent(an.left, liftEvent(bn.left, d)) &&
		leqEvent(an.right, liftEvent(bn.right, d))
}

func equalEvent(a, b *Event) bool {
	if a.leaf != b.leaf || a.value != b.value {
		return false
	}
	if a.leaf {
		return true
	}
	return equalEvent(a.left, b.left) && equalEvent(a.right, b.right)
}

// sinkEvent subtracts d from every absolute counter in e, clamping at 0.
// It is used to rebase a subtree onto a higher base counter during join.
func sinkEvent(e *Event, d int) *Event {
	if d <= 0 {
		return e.Clone()
	}
	if e.leaf {
		if e.value <= d {
			return leafEvent(0)
		}
		return leafEvent(e.value - d)
	}
	if e.value >= d {
		return &Event{value: e.value - d, left: e.left.Clone(), right: e.right.Clone()}
	}
	rem := d - e.value
	return &Event{value: 0, left: sinkEvent(e.left, rem), right: sinkEvent(e.right, rem)}
}

// joinEvent computes the pointwise maximum of two event trees, preserving
// the causal history of both.
func joinEvent(a, b *Event) *Event {
	if a.leaf && b.leaf {
		if a.value > b.value {
			return leafEvent(a.value)
		}
		return leafEvent(b.value)
	}
	an := liftLeaf(a)
	bn := liftLeaf(b)
	switch {
	case an.value < bn.value:
		d := bn.value - an.value
		return normEvent(&Event{
			value: bn.value,
			left:  joinEvent(sinkEvent(an.left, d), bn.left),
			right: joinEvent(sinkEvent(an.right, d), bn.right),
		})
	case bn.value < an.value:
		d := an.value - bn.value
		return normEvent(&Event{
			value: an.value,
			left:  joinEvent(an.left, sinkEvent(bn.left, d)),
			right: joinEvent(an.right, sinkEvent(bn.right, d)),
		})
	default:
		return normEvent(&Event{
			value: an.value,
			left:  joinEvent(an.left, bn.left),
			right: joinEvent(an.right, bn.right),
		})
	}
}

// String renders the Event as n or (n, l, r), e.g. "(1, 2, 0)".
func (e *Event) String() string {
	if e.leaf {
		return fmt.Sprintf("%d", e.value)
	}
	return fmt.Sprintf("(%d, %s, %s)", e.value, e.left, e.right)
}

// ---------------------------------------------------------------------------
// Stamp: an (ID, Event) pair identifying a replica and its causal history.
// ---------------------------------------------------------------------------

// Stamp is an Interval Tree Clock. All operations are pure: they never
// mutate the receiver and return new stamps, so a Stamp is safe to copy
// and share.
type Stamp struct {
	id    *ID
	event *Event
}

// Seed creates the initial clock: a single node owning the whole time
// interval [0, 1] with no events recorded yet.
func Seed() *Stamp {
	return &Stamp{id: oneID(), event: leafEvent(0)}
}

// ID returns the identity component of the stamp.
func (s *Stamp) ID() *ID { return s.id }

// EventTree returns the event (causal history) component of the stamp.
func (s *Stamp) EventTree() *Event { return s.event }

// Clone returns a deep copy of the stamp.
func (s *Stamp) Clone() *Stamp {
	return &Stamp{id: s.id.Clone(), event: s.event.Clone()}
}

// Fork splits the stamp into two stamps, each owning half of the time
// interval and sharing the same causal history. This is how a new node
// joins the system: any existing node forks and hands one stamp to the
// newcomer. No global coordination is required.
func (s *Stamp) Fork() (*Stamp, *Stamp) {
	l, r := forkID(s.id)
	return &Stamp{id: l, event: s.event.Clone()},
		&Stamp{id: r, event: s.event.Clone()}
}

// Event records one local event (the ITC "event" operation), advancing
// the causal history of this stamp.
func (s *Stamp) Event() *Stamp {
	return &Stamp{id: s.id.Clone(), event: event(s.id, s.event)}
}

// Join merges two stamps whose identities are disjoint (typically forked
// from a common ancestor). The result owns the union of both intervals
// and an event tree that preserves the causal history of both, so every
// event recorded on either side causally precedes the joined stamp.
func Join(a, b *Stamp) *Stamp {
	return &Stamp{id: sumID(a.id, b.id), event: joinEvent(a.event, b.event)}
}

// Leq reports whether s causally precedes or equals o, i.e. every event
// recorded in s is reflected in o.
func (s *Stamp) Leq(o *Stamp) bool { return leqEvent(s.event, o.event) }

// Equal reports whether s and o have identical causal histories.
func (s *Stamp) Equal(o *Stamp) bool { return equalEvent(s.event, o.event) }

// Concurrent reports whether neither stamp causally precedes the other.
func (s *Stamp) Concurrent(o *Stamp) bool { return !s.Leq(o) && !o.Leq(s) }

// String renders the stamp as "{id; event}", e.g. "{(1, 0); (1, 2, 0)}".
func (s *Stamp) String() string {
	return fmt.Sprintf("{%s; %s}", s.id, s.event)
}
