package jointpaxos

import "fmt"

// Config is the membership of one view. A plain config decides with a
// majority of Old. A joint config (the reconfiguration stage) decides
// only with a majority of Old AND a majority of New.
type Config struct {
	Old   []string
	New   []string // only meaningful when Joint is true
	Joint bool
}

// Members returns every node allowed to vote in this config.
func (c Config) Members() []string {
	if !c.Joint {
		return append([]string(nil), c.Old...)
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range c.Old {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, m := range c.New {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func majorityOf(set []string, votes map[string]bool) bool {
	n := 0
	for _, m := range set {
		if votes[m] {
			n++
		}
	}
	return n > len(set)/2
}

// Quorum reports whether votes satisfy this config's decision rule.
// For a joint config that is a majority of the old set AND a majority
// of the new set; a majority of only one side is never enough.
func (c Config) Quorum(votes map[string]bool) bool {
	if !c.Joint {
		return majorityOf(c.Old, votes)
	}
	return majorityOf(c.Old, votes) && majorityOf(c.New, votes)
}

func (c Config) String() string {
	if c.Joint {
		return fmt.Sprintf("joint(old=%v,new=%v)", c.Old, c.New)
	}
	return fmt.Sprintf("set%v", c.Old)
}

func intersects(a, b []string) bool {
	s := map[string]bool{}
	for _, x := range a {
		s[x] = true
	}
	for _, y := range b {
		if s[y] {
			return true
		}
	}
	return false
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	s := map[string]bool{}
	for _, x := range a {
		s[x] = true
	}
	for _, y := range b {
		if !s[y] {
			return false
		}
	}
	return true
}

// validTransition guards the config chain. A direct switch between two
// disjoint majorities is rejected: their quorums need not intersect, so
// the two views could commit different values. Disjoint reconfiguration
// is only legal through the joint-majority stage:
//
//	plain(Cold) -> joint(Cold, Cnew) -> plain(Cnew)
//
// A plain -> plain switch is tolerated only when the sets intersect
// (quorum intersection is then guaranteed directly).
func validTransition(cur, next Config) bool {
	if cur.Joint {
		// The joint stage may only finalize to its own new set.
		return !next.Joint && sameSet(next.Old, cur.New)
	}
	if next.Joint {
		// Entering the joint stage: the old side must be the current set.
		return sameSet(next.Old, cur.Old)
	}
	return intersects(cur.Old, next.Old)
}
