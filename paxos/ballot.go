package paxos

import "fmt"

// Ballot is a totally ordered proposal number. The (Num, Node) pair
// guarantees global uniqueness and a total order across nodes, which is
// what the conflict arbitrator uses to decide which proposer wins.
type Ballot struct {
	Num  int
	Node string
}

// Compare returns -1, 0 or +1 depending on whether b is less than, equal
// to, or greater than o.
func (b Ballot) Compare(o Ballot) int {
	switch {
	case b.Num < o.Num:
		return -1
	case b.Num > o.Num:
		return 1
	case b.Node < o.Node:
		return -1
	case b.Node > o.Node:
		return 1
	default:
		return 0
	}
}

func (b Ballot) String() string {
	return fmt.Sprintf("(%d,%s)", b.Num, b.Node)
}
