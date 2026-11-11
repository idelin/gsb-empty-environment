package netsec

import "fmt"

// MaxTags is the hard limit on the number of distinct community tags.
// Tags are represented as bits, so the finite domain of tag sets has
// exactly 2^MaxTags = 65536 members.
const MaxTags = 16

// Tag identifies one community tag in the bounded universe [0, MaxTags).
type Tag uint8

// TagSet is a bit set of community tags over the 16-tag universe.
// The zero value is the empty set.
type TagSet uint16

// Contains reports whether t is present in the set.
func (s TagSet) Contains(t Tag) bool {
	return s&bit(t) != 0
}

// With returns a copy of the set with t added.
func (s TagSet) With(t Tag) TagSet {
	return s | bit(t)
}

// Without returns a copy of the set with t removed.
func (s TagSet) Without(t Tag) TagSet {
	return s &^ bit(t)
}

// Union returns the union of two tag sets.
func (s TagSet) Union(other TagSet) TagSet {
	return s | other
}

// Intersect returns the intersection of two tag sets.
func (s TagSet) Intersect(other TagSet) TagSet {
	return s & other
}

// Empty reports whether the set has no members.
func (s TagSet) Empty() bool { return s == 0 }

// List returns the sorted list of tags contained in the set.
func (s TagSet) List() []Tag {
	out := make([]Tag, 0, popcount(uint16(s)))
	for i := 0; i < MaxTags; i++ {
		t := Tag(i)
		if s.Contains(t) {
			out = append(out, t)
		}
	}
	return out
}

// Count returns the number of tags in the set.
func (s TagSet) Count() int { return popcount(uint16(s)) }

// String renders the set as e.g. "tags{0,3}".
func (s TagSet) String() string {
	return fmt.Sprintf("tags%v", s.List())
}

func bit(t Tag) TagSet {
	return TagSet(1) << t
}

func popcount(x uint16) int {
	x = x - ((x >> 1) & 0x5555)
	x = (x & 0x3333) + ((x >> 2) & 0x3333)
	x = (x + (x >> 4)) & 0x0f0f
	return int((x * 0x0101) >> 8)
}

// validTag reports whether t fits inside the bounded universe.
func validTag(t Tag) bool { return int(t) < MaxTags }
