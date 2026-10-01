// Package gossip implements a SWIM-style piggyback gossip disseminator:
// membership change events (join/leave/suspect) are queued with a
// propagation counter and piggybacked on regular Ping/Ack packets,
// up to a bounded number of dissemination rounds.
package gossip

import "fmt"

// EventType is the kind of membership change.
type EventType int

const (
	Join EventType = iota
	Leave
	Suspect
)

func (t EventType) String() string {
	switch t {
	case Join:
		return "JOIN"
	case Leave:
		return "LEAVE"
	case Suspect:
		return "SUSPECT"
	}
	return "UNKNOWN"
}

// Event is a membership change notification. Incarnation orders events
// about the same node so stale news loses to fresh news.
type Event struct {
	Type        EventType
	Node        string
	Incarnation uint64
}

func (e Event) String() string {
	return fmt.Sprintf("%s(%s,inc=%d)", e.Type, e.Node, e.Incarnation)
}

// State is a node's status in a member view.
type State int

const (
	Alive State = iota
	Suspected
	Left
)

func (s State) String() string {
	switch s {
	case Alive:
		return "alive"
	case Suspected:
		return "suspected"
	case Left:
		return "left"
	}
	return "unknown"
}

// Member is one entry of a node's membership view table.
type Member struct {
	ID          string
	State       State
	Incarnation uint64
}
