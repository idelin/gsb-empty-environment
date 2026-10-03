// Package paxos implements a Multi-Paxos majority consensus log engine
// with Prepare/Promise and Accept/Accepted phases, leader leases that
// allow skipping Phase 1 for consecutive log entries, hole repair on
// leader change, and an offline integrity inspector with formal
// invariant assertions.
package paxos

import "fmt"

// Ballot is a totally ordered proposal number: (Term, Node).
type Ballot struct {
	Term int
	Node int
}

// Compare returns -1, 0 or +1 as b is less than, equal to, or greater
// than o in the lexicographic (Term, Node) order.
func (b Ballot) Compare(o Ballot) int {
	if b.Term != o.Term {
		if b.Term < o.Term {
			return -1
		}
		return 1
	}
	switch {
	case b.Node < o.Node:
		return -1
	case b.Node > o.Node:
		return 1
	}
	return 0
}

// IsZero reports whether b is the zero ballot.
func (b Ballot) IsZero() bool { return b.Term == 0 && b.Node == 0 }

func (b Ballot) String() string { return fmt.Sprintf("(%d,%d)", b.Term, b.Node) }

// LogEntry is a single log record. Prev references the parent slot and
// forms the integrity graph walked by the offline inspector.
type LogEntry struct {
	Term  int
	Value string
	Prev  int
}

// Equal reports value-equality of two entries.
func (e LogEntry) Equal(o LogEntry) bool {
	return e.Term == o.Term && e.Value == o.Value && e.Prev == o.Prev
}

func (e LogEntry) String() string {
	return fmt.Sprintf("{term=%d value=%q prev=%d}", e.Term, e.Value, e.Prev)
}

// Slot is the per-slot acceptor state.
type Slot struct {
	Promised    Ballot
	Accepted    Ballot
	HasAccepted bool
	Entry       LogEntry
	Committed   bool
}

// Promise is the Phase-1b reply. Accepted carries the acceptor state of
// the relevant slots so a proposer can learn previously accepted values.
type Promise struct {
	OK       bool
	Term     int
	MaxSlot  int
	Accepted map[int]Slot
}
