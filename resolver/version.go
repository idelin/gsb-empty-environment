package resolver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a semantic version. Versions are totally ordered.
type Version struct {
	Major, Minor, Patch int
}

// ParseVersion parses a "MAJOR.MINOR.PATCH" string.
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q: want MAJOR.MINOR.PATCH", s)
	}
	var nums [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("invalid version %q", s)
		}
		nums[i] = n
	}
	return Version{nums[0], nums[1], nums[2]}, nil
}

// Compare returns -1, 0 or +1 as v is less than, equal to, or greater than o.
func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return cmpInt(v.Major, o.Major)
	case v.Minor != o.Minor:
		return cmpInt(v.Minor, o.Minor)
	default:
		return cmpInt(v.Patch, o.Patch)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Range is a closed (inclusive) version interval [Lo, Hi].
type Range struct {
	Lo, Hi Version
}

// maxVersion is the inclusive upper bound of AnyRange.
var maxVersion = Version{1<<30 - 1, 1<<30 - 1, 1<<30 - 1}

// AnyRange matches every version.
func AnyRange() Range { return Range{Version{0, 0, 0}, maxVersion} }

// Contains reports whether v lies in r (inclusive on both ends).
func (r Range) Contains(v Version) bool {
	return r.Lo.Compare(v) <= 0 && v.Compare(r.Hi) <= 0
}

// Intersect returns the intersection of two ranges, or false if it is empty.
func (r Range) Intersect(o Range) (Range, bool) {
	lo, hi := r.Lo, r.Hi
	if o.Lo.Compare(lo) > 0 {
		lo = o.Lo
	}
	if o.Hi.Compare(hi) < 0 {
		hi = o.Hi
	}
	if lo.Compare(hi) > 0 {
		return Range{}, false
	}
	return Range{lo, hi}, true
}

// Intersects reports whether the two ranges overlap.
func (r Range) Intersects(o Range) bool {
	_, ok := r.Intersect(o)
	return ok
}

func (r Range) String() string {
	return "[" + r.Lo.String() + ", " + r.Hi.String() + "]"
}
