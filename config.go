package main

import (
	"fmt"
	"sort"
)

// Config is the cluster membership configuration.
// A stable config has Joint == false and only Old is meaningful.
// A joint config (Raft-style joint consensus) has Joint == true and
// requires a majority of BOTH Old and New for every decision.
type Config struct {
	Joint bool
	Old   []int
	New   []int
}

// Stable builds a single-set configuration.
func Stable(members ...int) Config {
	return Config{Old: sortedUnique(members)}
}

// JointCfg builds the joint configuration of an old and a new member set.
func JointCfg(old, newMembers []int) Config {
	return Config{Joint: true, Old: sortedUnique(old), New: sortedUnique(newMembers)}
}

func sortedUnique(ids []int) []int {
	cp := append([]int(nil), ids...)
	sort.Ints(cp)
	out := cp[:0]
	for i, id := range cp {
		if i == 0 || id != cp[i-1] {
			out = append(out, id)
		}
	}
	return out
}

func contains(set []int, id int) bool {
	for _, m := range set {
		if m == id {
			return true
		}
	}
	return false
}

func equalSets(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func majorityOf(set []int, votes map[int]bool) bool {
	if len(set) == 0 {
		return false
	}
	count := 0
	for _, id := range set {
		if votes[id] {
			count++
		}
	}
	return count > len(set)/2
}

// Quorum reports whether votes satisfy the decision condition of c:
// a majority of Old, and additionally a majority of New while joint.
// This is the single place where "joint majority" is enforced for
// elections and for commits.
func (c Config) Quorum(votes map[int]bool) bool {
	if !majorityOf(c.Old, votes) {
		return false
	}
	if c.Joint && !majorityOf(c.New, votes) {
		return false
	}
	return true
}

// Voters is the set of nodes that participate in the configuration.
func (c Config) Voters() []int {
	out := append([]int(nil), c.Old...)
	if c.Joint {
		for _, id := range c.New {
			if !contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// canTransitionTo enforces the only legal configuration chain:
//
//	Stable(A) -> Joint(A,B) -> Stable(B)
//
// In particular Stable(A) -> Stable(B) with A != B (a direct switch
// between possibly disjoint majorities) is rejected.
func (c Config) canTransitionTo(next Config) bool {
	if len(c.Old) == 0 {
		return true // uninitialized node accepts any first config
	}
	switch {
	case !c.Joint && !next.Joint:
		return equalSets(c.Old, next.Old)
	case !c.Joint && next.Joint:
		return equalSets(c.Old, next.Old)
	case c.Joint && !next.Joint:
		return equalSets(c.New, next.Old)
	default:
		return equalSets(c.Old, next.Old) && equalSets(c.New, next.New)
	}
}

func (c Config) String() string {
	if c.Joint {
		return fmt.Sprintf("joint(old=%v,new=%v)", c.Old, c.New)
	}
	return fmt.Sprintf("stable%v", c.Old)
}
