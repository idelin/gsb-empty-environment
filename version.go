package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// SemVer is a parsed semantic version (https://semver.org/spec/v2.0.0.html).
//
// Only the comparison-relevant fields are retained. A leading "v" is accepted
// on input but the canonical String form omits it, so versions compared inside
// the snapshot never depend on input spelling.
type SemVer struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	Prerelease string // dot-joined dot-separated identifiers, without the leading "-"
}

// ParseSemVer parses versions such as "1.2.3", "v1.2.3" and "1.2.3-rc.1".
func ParseSemVer(raw string) (SemVer, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return SemVer{}, fmt.Errorf("netsec: empty version string")
	}
	s = strings.TrimPrefix(s, "v")

	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return SemVer{}, fmt.Errorf("netsec: version %q must have major.minor.patch", raw)
	}
	nums := make([]uint64, 3)
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return SemVer{}, fmt.Errorf("netsec: invalid numeric component %q in version %q", p, raw)
		}
		nums[i] = n
	}

	if hasPre {
		if pre == "" {
			return SemVer{}, fmt.Errorf("netsec: empty prerelease in version %q", raw)
		}
		for _, ident := range strings.Split(pre, ".") {
			if ident == "" {
				return SemVer{}, fmt.Errorf("netsec: empty prerelease identifier in version %q", raw)
			}
			if !allValidIdentChars(ident) {
				return SemVer{}, fmt.Errorf("netsec: invalid prerelease identifier %q in version %q", ident, raw)
			}
			if isDigits(ident) {
				if len(ident) > 1 && ident[0] == '0' {
					return SemVer{}, fmt.Errorf("netsec: numeric prerelease identifier %q must not have leading zeroes", ident)
				}
			}
		}
	}

	return SemVer{Major: nums[0], Minor: nums[1], Patch: nums[2], Prerelease: pre}, nil
}

func allValidIdentChars(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '-' {
			continue
		}
		return false
	}
	return true
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Compare returns -1, 0 or 1 as a sorts lower than, equal to, or higher than b.
// Per semver, a version without a prerelease is greater than the same core
// version with a prerelease.
func (a SemVer) Compare(b SemVer) int {
	if a.Major != b.Major {
		return sign(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return sign(a.Minor, b.Minor)
	}
	if a.Patch != b.Patch {
		return sign(a.Patch, b.Patch)
	}
	switch {
	case a.Prerelease == b.Prerelease:
		return 0
	case a.Prerelease == "":
		return 1
	case b.Prerelease == "":
		return -1
	default:
		return comparePrerelease(a.Prerelease, b.Prerelease)
	}
}

func sign(x, y uint64) int {
	if x < y {
		return -1
	}
	return 1
}

func comparePrerelease(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		xNum, yNum := isDigits(x), isDigits(y)
		switch {
		case xNum && !yNum:
			return -1
		case !xNum && yNum:
			return 1
		case xNum && yNum:
			xv, _ := strconv.ParseUint(x, 10, 64)
			yv, _ := strconv.ParseUint(y, 10, 64)
			if xv != yv {
				return sign(xv, yv)
			}
		default:
			if x != y {
				if x < y {
					return -1
				}
				return 1
			}
		}
	}
	switch {
	case len(as) == len(bs):
		return 0
	case len(as) < len(bs):
		return -1
	default:
		return 1
	}
}

// String returns the canonical representation without a leading "v".
func (v SemVer) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		s += "-" + v.Prerelease
	}
	return s
}

// VersionRange is one inclusive or exclusive bound of a version interval.
//
// The range is zero-based: HasMin/HasMax determine whether a bound exists, so
// a half-open or unbounded interval is expressed simply by clearing the flag.
// Empty intervals (a range whose lower bound is not below its upper bound) are
// rejected by Compile.
type VersionRange struct {
	Min       SemVer
	HasMin    bool
	MinClosed bool // true: ">=", false: ">"
	Max       SemVer
	HasMax    bool
	MaxClosed bool // true: "<=", false: "<"
}

// ParseVersionRange parses a whitespace-separated sequence of comparisons,
// e.g. ">=1.0.0 <2.0.0", ">1.0.0", "<=3.1.2" or ">=1.0.0-rc.1 <2.0.0".
// At most one lower and one upper bound are allowed; wildcards are rejected so
// that every approved interval has explicit, auditable boundaries.
func ParseVersionRange(expr string) (VersionRange, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) == 0 {
		return VersionRange{}, fmt.Errorf("netsec: empty version range")
	}
	var r VersionRange
	for _, f := range fields {
		if len(f) < 2 {
			return VersionRange{}, fmt.Errorf("netsec: malformed range term %q", f)
		}
		var op, ver string
		switch {
		case strings.HasPrefix(f, ">="):
			op, ver = ">=", f[2:]
		case strings.HasPrefix(f, "<="):
			op, ver = "<=", f[2:]
		case strings.HasPrefix(f, ">"):
			op, ver = ">", f[1:]
		case strings.HasPrefix(f, "<"):
			op, ver = "<", f[1:]
		default:
			return VersionRange{}, fmt.Errorf("netsec: unsupported range term %q (use >=, >, <= or <)", f)
		}
		v, err := ParseSemVer(ver)
		if err != nil {
			return VersionRange{}, err
		}
		switch op {
		case ">=", ">":
			if r.HasMin {
				return VersionRange{}, fmt.Errorf("netsec: range %q specifies more than one lower bound", expr)
			}
			r.Min, r.HasMin, r.MinClosed = v, true, op == ">="
		case "<=", "<":
			if r.HasMax {
				return VersionRange{}, fmt.Errorf("netsec: range %q specifies more than one upper bound", expr)
			}
			r.Max, r.HasMax, r.MaxClosed = v, true, op == "<="
		}
	}
	if r.empty() {
		return VersionRange{}, fmt.Errorf("netsec: version range %q is empty", expr)
	}
	return r, nil
}

// Contains reports whether v falls inside the interval.
func (r VersionRange) Contains(v SemVer) bool {
	if r.HasMin {
		cmp := v.Compare(r.Min)
		if r.MinClosed {
			if cmp < 0 {
				return false
			}
		} else if cmp <= 0 {
			return false
		}
	}
	if r.HasMax {
		cmp := v.Compare(r.Max)
		if r.MaxClosed {
			if cmp > 0 {
				return false
			}
		} else if cmp >= 0 {
			return false
		}
	}
	return true
}

func (r VersionRange) empty() bool {
	if !r.HasMin || !r.HasMax {
		return false
	}
	cmp := r.Min.Compare(r.Max)
	if cmp > 0 {
		return true
	}
	if cmp == 0 && (!r.MinClosed || !r.MaxClosed) {
		return true
	}
	return false
}

// String renders the range back into the same grammar accepted by
// ParseVersionRange. An unbounded range is rendered as "*".
func (r VersionRange) String() string {
	var terms []string
	if r.HasMin {
		if r.MinClosed {
			terms = append(terms, ">="+r.Min.String())
		} else {
			terms = append(terms, ">"+r.Min.String())
		}
	}
	if r.HasMax {
		if r.MaxClosed {
			terms = append(terms, "<="+r.Max.String())
		} else {
			terms = append(terms, "<"+r.Max.String())
		}
	}
	if len(terms) == 0 {
		return "*"
	}
	return strings.Join(terms, " ")
}
