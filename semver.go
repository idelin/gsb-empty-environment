package taskpkg

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. The optional leading "v" used by Go
// modules is accepted. Build metadata is parsed but ignored in comparisons, as
// required by the semantic versioning specification.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease []string // nil means a release version; releases outrank pre-releases.
	raw        string
}

// ParseVersion parses a semantic version such as "v1.2.3" or "2.0.0-rc.1".
func ParseVersion(s string) (Version, error) {
	v := Version{raw: s}
	core := s
	if strings.HasPrefix(core, "v") || strings.HasPrefix(core, "V") {
		core = core[1:]
	}
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	if i := strings.IndexByte(core, '-'); i >= 0 {
		for _, ident := range strings.Split(core[i+1:], ".") {
			if ident == "" {
				return Version{}, fmt.Errorf("taskpkg: empty prerelease identifier in %q", s)
			}
			if !isIdentChars(ident) {
				return Version{}, fmt.Errorf("taskpkg: invalid prerelease identifier %q in %q", ident, s)
			}
			if isDigits(ident) && len(ident) > 1 && ident[0] == '0' {
				return Version{}, fmt.Errorf("taskpkg: numeric prerelease identifier must not have leading zeros: %q", s)
			}
			v.Prerelease = append(v.Prerelease, ident)
		}
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("taskpkg: version %q must have major.minor.patch", s)
	}
	nums := [3]*int{&v.Major, &v.Minor, &v.Patch}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || !isDigits(part) {
			return Version{}, fmt.Errorf("taskpkg: invalid numeric component %q in %q", part, s)
		}
		if len(part) > 1 && part[0] == '0' {
			return Version{}, fmt.Errorf("taskpkg: numeric component must not have leading zeros: %q", s)
		}
		*nums[i] = n
	}
	return v, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isIdentChars(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') && r != '-' {
			return false
		}
	}
	return true
}

// String returns the canonical representation, retaining a leading "v".
func (v Version) String() string {
	var b strings.Builder
	if strings.HasPrefix(v.raw, "v") || strings.HasPrefix(v.raw, "V") {
		b.WriteByte('v')
	}
	fmt.Fprintf(&b, "%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != nil {
		b.WriteByte('-')
		b.WriteString(strings.Join(v.Prerelease, "."))
	}
	return b.String()
}

// Compare returns -1, 0 or 1 as v is older than, equal to or newer than other.
func (v Version) Compare(other Version) int {
	if c := intCmp(v.Major, other.Major); c != 0 {
		return c
	}
	if c := intCmp(v.Minor, other.Minor); c != 0 {
		return c
	}
	if c := intCmp(v.Patch, other.Patch); c != 0 {
		return c
	}
	return comparePrerelease(v.Prerelease, other.Prerelease)
}

// A release (no prerelease) outranks any prerelease of the same core version.
func comparePrerelease(a, b []string) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	return intCmp(len(a), len(b))
}

// Numeric identifiers always have lower precedence than non-numeric identifiers.
func compareIdent(a, b string) int {
	an, bn := isDigits(a), isDigits(b)
	switch {
	case an && bn:
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		return intCmp(x, y)
	case an && !bn:
		return -1
	case !an && bn:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func intCmp(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// InClosedInterval reports whether min <= v <= max. Bounds are inclusive.
func (v Version) InClosedInterval(min, max Version) bool {
	return v.Compare(min) >= 0 && v.Compare(max) <= 0
}
