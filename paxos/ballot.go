package paxos

// Ballot is a totally-ordered proposal number: (Num, NodeID).
type Ballot struct {
	Num  uint64
	Node int
}

// Compare returns -1, 0, +1.
func (b Ballot) Compare(o Ballot) int {
	if b.Num != o.Num {
		if b.Num < o.Num {
			return -1
		}
		return 1
	}
	if b.Node != o.Node {
		if b.Node < o.Node {
			return -1
		}
		return 1
	}
	return 0
}

func (b Ballot) Greater(o Ballot) bool { return b.Compare(o) > 0 }
func (b Ballot) Less(o Ballot) bool    { return b.Compare(o) < 0 }

// NoopValue marks a slot filled by Phase-1 hole repair when no acceptor
// in the majority had a value for it.
const NoopValue = "\x00noop"
