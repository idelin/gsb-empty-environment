// Package gossip implements a SWIM-style piggyback gossip disseminator:
// membership change events (join/leave/suspect) are queued with propagation
// counters and piggybacked on regular Ping/Ack packet headers, with a cap
// on the maximum number of dissemination rounds (O(log N)).
package gossip

// State is the membership state of a node.
type State int

const (
	Alive State = iota
	Suspect
	Dead
	Left
)

func (s State) String() string {
	switch s {
	case Alive:
		return "alive"
	case Suspect:
		return "suspect"
	case Dead:
		return "dead"
	case Left:
		return "left"
	}
	return "unknown"
}

// rank orders states for conflict resolution at equal incarnation numbers:
// a "stronger" (worse) state wins over a weaker one.
func (s State) rank() int {
	switch s {
	case Alive:
		return 0
	case Suspect:
		return 1
	case Dead, Left:
		return 2
	}
	return 0
}

// Member is one entry of a node's membership view table.
type Member struct {
	ID          string
	Addr        string
	State       State
	Incarnation uint64
}

// Event is a membership change notification carried in packet headers.
type Event struct {
	NodeID      string
	Addr        string
	State       State
	Incarnation uint64
}

// supersedes reports whether e is strictly newer than other.
func (e Event) supersedes(other Event) bool {
	if e.Incarnation != other.Incarnation {
		return e.Incarnation > other.Incarnation
	}
	return e.State.rank() > other.State.rank()
}
