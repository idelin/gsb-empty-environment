package repl

import "fmt"

// Checker provides the safety evidence. After every simulator event it
// verifies the central invariant:
//
//	The follower's applied state is ALWAYS exactly the result of applying
//	some prefix of the leader's log (entries 1..lastApplied).
//
// This single invariant subsumes:
//   - no mixed reads: the state is never part-old/part-new, because any
//     published snapshot is itself a full prefix image;
//   - no duplicate application: re-applying an entry would require
//     lastApplied to move backwards or a key to diverge from the prefix;
//   - no gaps: lastApplied advances only by 1 (log) or to a snapshot's
//     LastIndex whose image equals the prefix.
type Checker struct {
	Leader     *Leader
	Follower   *Follower
	Violations []string
	Checks     int
}

func NewChecker(l *Leader, f *Follower) *Checker {
	return &Checker{Leader: l, Follower: f}
}

// Check verifies the invariant at the current instant.
func (c *Checker) Check() {
	c.Checks++
	want := c.Leader.StateAt(c.Follower.LastApplied())
	got, la, _ := c.Follower.ReadAll()
	if len(got) != len(want) {
		c.Violations = append(c.Violations,
			fmt.Sprintf("lastApplied=%d: |state|=%d, want %d", la, len(got), len(want)))
		return
	}
	for k, v := range want {
		if got[k] != v {
			c.Violations = append(c.Violations,
				fmt.Sprintf("lastApplied=%d: key %q = %q, want %q", la, k, got[k], v))
			return
		}
	}
}

// OK reports whether the invariant held at every checked instant.
func (c *Checker) OK() bool { return len(c.Violations) == 0 }
