package gsb

// State is the health state of a node record. The merge order is
// Alive < Suspect < Dead: at equal incarnation, the higher state wins.
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

// Record is a single membership entry: (nodeID, incarnation, state).
type Record struct {
	NodeID      string
	Incarnation uint64
	State       State
}

// Compare orders records for merging: higher incarnation wins; ties are
// broken by state rank (Alive < Suspect < Dead). Returns -1, 0 or +1.
func (r Record) Compare(o Record) int {
	if r.Incarnation != o.Incarnation {
		if r.Incarnation < o.Incarnation {
			return -1
		}
		return 1
	}
	switch {
	case r.State < o.State:
		return -1
	case r.State > o.State:
		return 1
	}
	return 0
}

// Merge returns the dominant record. Merge is a join in a finite lattice:
// commutative, associative and idempotent, so applying records is safe
// against stale, duplicated or reordered deliveries.
func Merge(a, b Record) Record {
	if a.Compare(b) >= 0 {
		return a
	}
	return b
}
