package paxos

import "fmt"

// Ballot is a totally ordered (round, nodeID) pair.
type Ballot struct {
	Round int
	Node  int
}

// Compare returns -1, 0 or +1 as b is less than, equal to, or greater than o.
func (b Ballot) Compare(o Ballot) int {
	if b.Round != o.Round {
		if b.Round < o.Round {
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

func (b Ballot) IsZero() bool { return b.Round == 0 && b.Node == 0 }

func (b Ballot) String() string { return fmt.Sprintf("(%d,%d)", b.Round, b.Node) }
