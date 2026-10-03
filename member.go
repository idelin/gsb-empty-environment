package gsb

// State is the membership state of a cluster member.
type State uint8

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

// Member is one entry in a node's membership view.
type Member struct {
	ID          string
	State       State
	Incarnation uint64
}

// overrides reports whether m should replace o during reconciliation:
// higher incarnation wins; on a tie the "more severe" state wins
// (dead > suspect > alive), matching SWIM-style refutation rules.
func (m Member) overrides(o Member) bool {
	if m.Incarnation != o.Incarnation {
		return m.Incarnation > o.Incarnation
	}
	return m.State > o.State
}
