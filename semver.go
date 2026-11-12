package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version (semver 2.0.0).
//
// Build metadata is parsed for validation but never participates in
// precedence, so two versions that differ only by build metadata compare
// equal and share the same canonical form.
type Version struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	Prerelease []string
	Build      string
}

// ErrInvalidVersion is returned when a string is not a valid semver
// version. It is also wrapped with the offending value.
var ErrInvalidVersion = errors.New("netsec: invalid semantic version")

// ParseVersion parses a strict semver 2.0.0 version such as
// "1.2.3", "1.0.0-alpha.1" or "2.0.0+sha.abc".
func ParseVersion(s string) (Version, error) {
	raw := s
	build := ""
	if i := strings.IndexByte(s, '+'); i >= 0 {
		build = s[i+1:]
		s = s[:i]
		if build == "" || !isIdentList(build) {
			return Version{}, fmt.Errorf("%w: %q", ErrInvalidVersion, raw)
		}
	}

	var pre string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
		if pre == "" || !isIdentList(pre) {
			return Version{}, fmt.Errorf("%w: %q", ErrInvalidVersion, raw)
		}
	}

	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%w: %q", ErrInvalidVersion, raw)
	}
	nums := make([]uint64, 3)
	for i, part := range parts {
		if !isNumericIdent(part) {
			return Version{}, fmt.Errorf("%w: %q", ErrInvalidVersion, raw)
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("%w: %q", ErrInvalidVersion, raw)
		}
		nums[i] = n
	}

	var prerelease []string
	if pre != "" {
		prerelease = strings.Split(pre, ".")
	}
	return Version{
		Major:      nums[0],
		Minor:      nums[1],
		Patch:      nums[2],
		Prerelease: prerelease,
		Build:      build,
	}, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isNumericIdent(s string) bool {
	return isDigits(s) && (len(s) == 1 || s[0] != '0')
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	allDigits := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
			allDigits = false
		default:
			return false
		}
	}
	// Numeric identifiers, including prerelease ones, must not carry
	// leading zeros per the semver 2.0 spec.
	if allDigits && !isNumericIdent(s) {
		return false
	}
	return true
}

func isIdentList(s string) bool {
	for _, ident := range strings.Split(s, ".") {
		if !isIdent(ident) {
			return false
		}
	}
	return true
}

// Compare returns -1, 0 or 1 as v precedes, equals or follows other.
// Build metadata is ignored by precedence.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]uint64{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
	} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	return comparePrerelease(v.Prerelease, other.Prerelease)
}

func comparePrerelease(a, b []string) int {
	// A version without a prerelease has higher precedence.
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if c := compareIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func compareIdent(a, b string) int {
	an, aNum := numericIdent(a)
	bn, bNum := numericIdent(b)
	switch {
	case aNum && bNum:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	case aNum:
		// Numeric identifiers always have lower precedence than alphanumerics.
		return -1
	case bNum:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func numericIdent(s string) (uint64, bool) {
	if !isDigits(s) {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Canonical returns the version without build metadata, the form used as
// graph node identity. Prerelease ordering is preserved.
func (v Version) Canonical() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d.%d.%d", v.Major, v.Minor, v.Patch)
	if len(v.Prerelease) > 0 {
		b.WriteByte('-')
		b.WriteString(strings.Join(v.Prerelease, "."))
	}
	return b.String()
}

// String renders the full version including any build metadata.
func (v Version) String() string {
	s := v.Canonical()
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}
