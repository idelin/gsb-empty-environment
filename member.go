// Package membersim is a discrete-event simulator for a SWIM-style group
// membership protocol. It models crash/restart faults, message loss,
// duplication and reordering, and produces a deterministic, replayable
// event log plus per-node membership views with transition reasons.
package membersim

// State is the membership state of a node as seen by an observer.
type State int

const (
	Alive State = iota
	Suspect
	Dead
)

func (s State) String() string {
	switch s {
	case Alive:
		return "alive"
	case Suspect:
		return "suspect"
	case Dead:
		return "dead"
	}
	return "unknown"
}

// stateRank orders states within one incarnation: dead > suspect > alive.
func stateRank(s State) int {
	switch s {
	case Alive:
		return 0
	case Suspect:
		return 1
	case Dead:
		return 2
	}
	return -1
}

// Member is one entry in a node's membership view.
type Member struct {
	State       State
	Incarnation uint64
}

// Update is a gossiped membership fact about Node.
type Update struct {
	Node        int
	State       State
	Incarnation uint64
}

// mergeMembers folds a remote view of a member into the local one.
// A higher incarnation always wins; within the same incarnation the
// higher ranked state (dead > suspect > alive) wins. Incarnations are
// never invented here: only the subject node itself may raise its own
// incarnation (see node.refute and sim.restartNode).
func mergeMembers(local, remote Member) (merged Member, changed bool) {
	if remote.Incarnation > local.Incarnation {
		return remote, true
	}
	if remote.Incarnation == local.Incarnation && stateRank(remote.State) > stateRank(local.State) {
		return remote, true
	}
	return local, false
}
