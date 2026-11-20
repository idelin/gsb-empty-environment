package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// nugetVersion is a parsed NuGet semantic version: major.minor.patch.revision
// plus an optional dash-separated prerelease label.
type nugetVersion struct {
	major    uint64
	minor    uint64
	patch    uint64
	revision uint64
	release  string
}

func parseNugetVersion(s string) (nugetVersion, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nugetVersion{}, fmt.Errorf("empty version")
	}
	var v nugetVersion
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.release = s[i+1:]
		s = s[:i]
		if v.release == "" {
			return nugetVersion{}, fmt.Errorf("empty prerelease label")
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return nugetVersion{}, fmt.Errorf("too many version components")
	}
	nums := make([]uint64, 4)
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nugetVersion{}, fmt.Errorf("invalid version component %q", p)
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch, v.revision = nums[0], nums[1], nums[2], nums[3]
	return v, nil
}

// compareNuget returns -1, 0 or 1. A version without a prerelease label is
// greater than one with a label; labels compare lexicographically.
func compareNuget(a, b nugetVersion) int {
	for _, pair := range [][2]uint64{
		{a.major, b.major},
		{a.minor, b.minor},
		{a.patch, b.patch},
		{a.revision, b.revision},
	} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.release == b.release:
		return 0
	case a.release == "":
		return 1
	case b.release == "":
		return -1
	case a.release < b.release:
		return -1
	default:
		return 1
	}
}

// rangeBound is one endpoint of a NuGet version range. A zero value with
// open==true means an unbounded side.
type rangeBound struct {
	version nugetVersion
	open    bool
}

type versionRange struct {
	lower          rangeBound
	upper          rangeBound
	lowerInclusive bool
	upperInclusive bool
}

// parseVersionRange parses the NuGet PackageReference version range syntax,
// including floating wildcards such as 1.0.*, 1.* and *.
func parseVersionRange(s string) (versionRange, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return versionRange{}, fmt.Errorf("empty version range")
	}

	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "(") {
		lowerInclusive := s[0] == '['
		if !strings.HasSuffix(s, "]") && !strings.HasSuffix(s, ")") {
			return versionRange{}, fmt.Errorf("malformed range %q", s)
		}
		upperInclusive := s[len(s)-1] == ']'
		inner := s[1 : len(s)-1]
		if inner == "" {
			return versionRange{}, fmt.Errorf("empty range body %q", s)
		}
		parts := strings.SplitN(inner, ",", 2)
		vr := versionRange{lowerInclusive: lowerInclusive, upperInclusive: upperInclusive}
		low := strings.TrimSpace(parts[0])
		if low == "" {
			vr.lower.open = true
		} else if strings.Contains(low, "*") {
			return versionRange{}, fmt.Errorf("floating wildcard not allowed inside explicit range %q", s)
		} else {
			v, err := parseNugetVersion(low)
			if err != nil {
				return versionRange{}, err
			}
			vr.lower.version = v
		}
		if len(parts) == 1 {
			if !lowerInclusive || !upperInclusive {
				return versionRange{}, fmt.Errorf("single-version range must use brackets %q", s)
			}
			vr.upper.version = vr.lower.version
		} else {
			up := strings.TrimSpace(parts[1])
			if up == "" {
				vr.upper.open = true
			} else if strings.Contains(up, "*") {
				return versionRange{}, fmt.Errorf("floating wildcard not allowed inside explicit range %q", s)
			} else {
				v, err := parseNugetVersion(up)
				if err != nil {
					return versionRange{}, err
				}
				vr.upper.version = v
			}
		}
		return vr, nil
	}

	// Bare version or floating wildcard: lower bound inclusive, no upper bound.
	if strings.Contains(s, "*") {
		base, err := floatingLowerBound(s)
		if err != nil {
			return versionRange{}, err
		}
		return versionRange{
			lower:          rangeBound{version: base},
			upper:          rangeBound{open: true},
			lowerInclusive: true,
		}, nil
	}
	v, err := parseNugetVersion(s)
	if err != nil {
		return versionRange{}, err
	}
	return versionRange{
		lower:          rangeBound{version: v},
		upper:          rangeBound{open: true},
		lowerInclusive: true,
	}, nil
}

// floatingLowerBound turns a wildcard version such as 1.2.* into the minimum
// concrete version it can resolve to (1.2.0). A bare "*" is unbounded.
func floatingLowerBound(s string) (nugetVersion, error) {
	parts := strings.Split(s, ".")
	nums := make([]uint64, 0, 4)
	sawStar := false
	for _, p := range parts {
		if p == "*" {
			sawStar = true
			continue
		}
		if sawStar {
			return nugetVersion{}, fmt.Errorf("version component after wildcard in %q", s)
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nugetVersion{}, fmt.Errorf("invalid wildcard version %q", s)
		}
		nums = append(nums, n)
	}
	var v nugetVersion
	for i := range nums {
		switch i {
		case 0:
			v.major = nums[i]
		case 1:
			v.minor = nums[i]
		case 2:
			v.patch = nums[i]
		case 3:
			v.revision = nums[i]
		}
	}
	return v, nil
}

// contains reports whether v satisfies the range.
func (r versionRange) contains(v nugetVersion) bool {
	if !r.lower.open {
		cmp := compareNuget(v, r.lower.version)
		if r.lowerInclusive {
			if cmp < 0 {
				return false
			}
		} else if cmp <= 0 {
			return false
		}
	}
	if !r.upper.open {
		cmp := compareNuget(v, r.upper.version)
		if r.upperInclusive {
			if cmp > 0 {
				return false
			}
		} else if cmp >= 0 {
			return false
		}
	}
	return true
}

// belowLowerBound reports whether v is strictly below the range's lower bound.
// An unbounded lower side never downgrades.
func (r versionRange) belowLowerBound(v nugetVersion) bool {
	if r.lower.open {
		return false
	}
	return compareNuget(v, r.lower.version) < 0
}

func lowerCase(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
