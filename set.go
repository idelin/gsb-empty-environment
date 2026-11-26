package joinenum

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"
)

// RelationSet is a compact set of base-relation identifiers.  Queries are
// limited to at most 62 relations because the set is represented by a uint64.
type RelationSet uint64

// EmptySet is the empty relation set.
const EmptySet RelationSet = 0

// Of returns the set containing exactly the given relations.
func Of(relations ...int) RelationSet {
	var s RelationSet
	for _, r := range relations {
		s = s.Add(r)
	}
	return s
}

// Add returns the set with relation r included.
func (s RelationSet) Add(r int) RelationSet {
	if r < 0 || r >= 63 {
		panic(fmt.Sprintf("joinenum: relation index %d out of range", r))
	}
	return s | (RelationSet(1) << uint(r))
}

// Remove returns the set with relation r removed.
func (s RelationSet) Remove(r int) RelationSet {
	if r < 0 || r >= 63 {
		return s
	}
	return s &^ (RelationSet(1) << uint(r))
}

// Contains reports whether r belongs to s.
func (s RelationSet) Contains(r int) bool {
	if r < 0 || r >= 63 {
		return false
	}
	return s&(RelationSet(1)<<uint(r)) != 0
}

// Union returns s ∪ t.
func (s RelationSet) Union(t RelationSet) RelationSet { return s | t }

// Intersect returns s ∩ t.
func (s RelationSet) Intersect(t RelationSet) RelationSet { return s & t }

// Difference returns s \ t.
func (s RelationSet) Difference(t RelationSet) RelationSet { return s &^ t }

// Subset reports whether s ⊆ t.
func (s RelationSet) Subset(t RelationSet) bool { return s&t == s }

// Equal reports whether the two sets contain the same relations.
func (s RelationSet) Equal(t RelationSet) bool { return s == t }

// IsEmpty reports whether s has no members.
func (s RelationSet) IsEmpty() bool { return s == 0 }

// Len returns the cardinality of s.
func (s RelationSet) Len() int { return bits.OnesCount64(uint64(s)) }

// Elements returns the members of s in ascending order.
func (s RelationSet) Elements() []int {
	out := make([]int, 0, s.Len())
	for v := uint64(s); v != 0; v &= v - 1 {
		out = append(out, bits.TrailingZeros64(v))
	}
	return out
}

// Subsets calls fn for every subset of s, including the empty set and s.
func (s RelationSet) Subsets(fn func(RelationSet)) {
	for sub := s; ; sub = (sub - 1) & s {
		fn(sub)
		if sub == 0 {
			return
		}
	}
}

// ProperSubsets calls fn for every non-empty proper subset of s.
func (s RelationSet) ProperSubsets(fn func(RelationSet)) {
	for sub := (s - 1) & s; sub != 0; sub = (sub - 1) & s {
		fn(sub)
	}
}

func (s RelationSet) String(names []string) string {
	elems := s.Elements()
	parts := make([]string, 0, len(elems))
	for _, r := range elems {
		if r < len(names) {
			parts = append(parts, names[r])
		} else {
			parts = append(parts, fmt.Sprintf("#%d", r))
		}
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ",") + "}"
}
