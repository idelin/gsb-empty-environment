// Package modselect implements version selection for a Go module proxy:
// retraction intervals, exclusion lists, and @latest resolution.
package modselect

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Build metadata (including the
// "+incompatible" suffix) is preserved in Raw but ignored for ordering,
// so v2.0.0 and v2.0.0+incompatible compare equal.
type Version struct {
	Raw   string
	Major uint64
	Minor uint64
	Patch uint64
	Pre   string
	Build string
}

// Parse parses a Go-style semantic version such as "v1.2.3",
// "v1.2.3-rc.1" or "v2.0.0+incompatible".
func Parse(v string) (Version, error) {
	if !strings.HasPrefix(v, "v") {
		return Version{}, fmt.Errorf("modselect: version %q missing leading 'v'", v)
	}
	rest := v[1:]
	var pre, build string
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		build = rest[i+1:]
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		pre = rest[i+1:]
		rest = rest[:i]
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("modselect: version %q: want vMAJOR.MINOR.PATCH", v)
	}
	var nums [3]uint64
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("modselect: version %q: invalid number %q", v, p)
		}
		nums[i] = n
	}
	return Version{Raw: v, Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: pre, Build: build}, nil
}

// IsRelease reports whether v has no prerelease suffix.
func (v Version) IsRelease() bool { return v.Pre == "" }

// Compare orders versions by semver precedence. A prerelease sorts below
// its release; build metadata (+incompatible) does not affect the order.
func Compare(a, b Version) int {
	if a.Major != b.Major {
		return cmpUint(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return cmpUint(a.Minor, b.Minor)
	}
	if a.Patch != b.Patch {
		return cmpUint(a.Patch, b.Patch)
	}
	return comparePrerelease(a.Pre, b.Pre)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func comparePrerelease(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1 // release outranks any prerelease
	case b == "":
		return -1
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if c := compareIdent(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return cmpUint(uint64(len(as)), uint64(len(bs)))
}

func compareIdent(a, b string) int {
	an, aerr := strconv.ParseUint(a, 10, 64)
	bn, berr := strconv.ParseUint(b, 10, 64)
	switch {
	case aerr == nil && berr == nil:
		return cmpUint(an, bn)
	case aerr == nil:
		return -1 // numeric identifiers sort below alphanumeric
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}
