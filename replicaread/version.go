package replicaread

import "fmt"

// Version identifies a write. It is totally ordered: first by Term,
// then by Seq within the same term.
type Version struct {
	Term uint64
	Seq  uint64
}

// Compare returns -1, 0 or +1 depending on whether v is less than,
// equal to, or greater than o.
func (v Version) Compare(o Version) int {
	switch {
	case v.Term != o.Term:
		if v.Term < o.Term {
			return -1
		}
		return 1
	case v.Seq != o.Seq:
		if v.Seq < o.Seq {
			return -1
		}
		return 1
	default:
		return 0
	}
}

func (v Version) Less(o Version) bool { return v.Compare(o) < 0 }

func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Term, v.Seq) }
