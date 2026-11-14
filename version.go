package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed, comparable version.  It follows Semantic Versioning
// precedence for the common numeric-core form used by npm, Cargo, NuGet,
// PyPI and similar ecosystems.  The parser is deliberately a little more
// permissive than strict SemVer: a leading "v", leading zeroes, and missing
// minor/patch components are accepted and canonicalized.  This is useful when
// consuming SBOM data, where such spellings are common.
type Version struct {
	// Epoch is the optional numeric prefix before a colon, as used by
	// package managers such as Debian and RPM.  It is zero for SemVer.
	Epoch int64

	// Segments holds the numeric core.  SemVer uses three segments; some
	// package managers use a different number.
	Segments []int64

	// Major, Minor and Patch mirror the first three numeric segments.
	Major int64
	Minor int64
	Patch int64

	// Prerelease holds the identifiers after "-".  An empty slice means
	// this is a release version.
	Prerelease []string

	// Build holds the metadata after "+".  Build metadata does not affect
	// precedence, but it is retained in the canonical spelling.
	Build string

	// Original is the spelling supplied to ParseVersion.
	Original string

	// Valid reports whether the value parsed as a version.  Invalid
	// versions are still usable for the loose comparison fallback.
	Valid bool
}

// ParseVersion parses a version string.  It accepts a leading "v" or "V",
// leading zeroes, one to three (or more) numeric segments, a prerelease
// suffix, build metadata and an optional numeric epoch prefix.
func ParseVersion(raw string) (Version, error) {
	v, err := parseVersion(raw)
	if err != nil {
		return Version{Original: raw}, err
	}
	return v, nil
}

// MustParseVersion is like ParseVersion but panics on an invalid version.
func MustParseVersion(raw string) Version {
	v, err := ParseVersion(raw)
	if err != nil {
		panic(err)
	}
	return v
}

func parseVersion(raw string) (Version, error) {
	s := strings.TrimSpace(raw)
	original := raw
	if s == "" {
		return Version{Original: original}, fmt.Errorf("empty version")
	}

	v := Version{Original: original, Valid: true}

	// Optional epoch, for example "1:2.3.4".
	if i := strings.IndexByte(s, ':'); i > 0 {
		allDigits := true
		for _, r := range s[:i] {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			n, err := strconv.ParseInt(s[:i], 10, 64)
			if err != nil {
				return v, fmt.Errorf("invalid version epoch %q", s[:i])
			}
			v.Epoch = n
			s = s[i+1:]
		}
	}

	// A leading v/V is a common tag spelling, not part of the version.
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
		if s == "" {
			return v, fmt.Errorf("empty version after prefix")
		}
	}

	if i := strings.IndexByte(s, '+'); i >= 0 {
		v.Build = s[i+1:]
		s = s[:i]
		if v.Build == "" {
			return v, fmt.Errorf("empty build metadata")
		}
		if !isBuildMetadata(v.Build) {
			return v, fmt.Errorf("invalid build metadata %q", v.Build)
		}
	}

	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre := s[i+1:]
		s = s[:i]
		if pre == "" {
			return v, fmt.Errorf("empty prerelease")
		}
		v.Prerelease = strings.Split(pre, ".")
		for j, id := range v.Prerelease {
			if id == "" || !isPrereleaseIdentifier(id) {
				return v, fmt.Errorf("invalid prerelease identifier %q", id)
			}
			// Canonicalize numeric prerelease identifiers by removing
			// leading zeroes.  This also makes "rc.01" and "rc.1"
			// compare and identify alike.
			if isDigits(id) {
				v.Prerelease[j] = trimLeadingZeroes(id)
			}
		}
	}

	if s == "" {
		return v, fmt.Errorf("empty numeric version")
	}
	parts := strings.Split(s, ".")
	v.Segments = make([]int64, 0, len(parts))
	for _, part := range parts {
		if part == "" || !isDigits(part) {
			return v, fmt.Errorf("invalid numeric version segment %q", part)
		}
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return v, fmt.Errorf("invalid numeric version segment %q", part)
		}
		v.Segments = append(v.Segments, n)
	}
	if len(v.Segments) > 0 {
		v.Major = v.Segments[0]
	}
	if len(v.Segments) > 1 {
		v.Minor = v.Segments[1]
	}
	if len(v.Segments) > 2 {
		v.Patch = v.Segments[2]
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

func trimLeadingZeroes(s string) string {
	i := 0
	for i < len(s)-1 && s[i] == '0' {
		i++
	}
	return s[i:]
}

func isPrereleaseIdentifier(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func isBuildMetadata(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// String returns the canonical version spelling.  Numeric cores shorter than
// three segments are padded with zeroes, matching SemVer precedence.
func (v Version) String() string {
	var b strings.Builder
	if v.Epoch != 0 {
		b.WriteString(strconv.FormatInt(v.Epoch, 10))
		b.WriteByte(':')
	}
	if len(v.Segments) == 0 {
		if v.Major != 0 || v.Minor != 0 || v.Patch != 0 {
			b.WriteString(strconv.FormatInt(v.Major, 10))
			b.WriteByte('.')
			b.WriteString(strconv.FormatInt(v.Minor, 10))
			b.WriteByte('.')
			b.WriteString(strconv.FormatInt(v.Patch, 10))
		} else {
			b.WriteString("0.0.0")
		}
	} else {
		segments := v.Segments
		if len(segments) < 3 {
			segments = append([]int64(nil), segments...)
			for len(segments) < 3 {
				segments = append(segments, 0)
			}
		}
		for i, n := range segments {
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(strconv.FormatInt(n, 10))
		}
	}
	if len(v.Prerelease) > 0 {
		b.WriteByte('-')
		b.WriteString(strings.Join(v.Prerelease, "."))
	}
	if v.Build != "" {
		b.WriteByte('+')
		b.WriteString(v.Build)
	}
	return b.String()
}

// Compare compares v to other according to SemVer precedence.  Build metadata
// is ignored.  Epochs are compared first.
func (v Version) Compare(other Version) int {
	if v.Epoch != other.Epoch {
		if v.Epoch < other.Epoch {
			return -1
		}
		return 1
	}

	n := len(v.Segments)
	if len(other.Segments) > n {
		n = len(other.Segments)
	}
	if len(v.Segments) == 0 && (v.Major != 0 || v.Minor != 0 || v.Patch != 0) {
		n = maxInt(n, 3)
	}
	if len(other.Segments) == 0 && (other.Major != 0 || other.Minor != 0 || other.Patch != 0) {
		n = maxInt(n, 3)
	}
	for i := 0; i < n; i++ {
		a, b := v.segment(i), other.segment(i)
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}

	// A release version has higher precedence than a prerelease of the same
	// numeric core.
	if len(v.Prerelease) == 0 && len(other.Prerelease) > 0 {
		return 1
	}
	if len(v.Prerelease) > 0 && len(other.Prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(v.Prerelease) && i < len(other.Prerelease); i++ {
		if c := comparePrereleaseIdentifier(v.Prerelease[i], other.Prerelease[i]); c != 0 {
			return c
		}
	}
	if len(v.Prerelease) != len(other.Prerelease) {
		if len(v.Prerelease) < len(other.Prerelease) {
			return -1
		}
		return 1
	}
	return 0
}

func (v Version) segment(i int) int64 {
	if i < len(v.Segments) {
		return v.Segments[i]
	}
	switch i {
	case 0:
		return v.Major
	case 1:
		return v.Minor
	case 2:
		return v.Patch
	default:
		return 0
	}
}

// MajorVersion returns the first numeric segment.
func (v Version) MajorVersion() int64 {
	if len(v.Segments) == 0 {
		return v.Major
	}
	return v.Segments[0]
}

// MinorVersion returns the second numeric segment.
func (v Version) MinorVersion() int64 {
	if len(v.Segments) < 2 {
		return v.Minor
	}
	return v.Segments[1]
}

// PatchVersion returns the third numeric segment.
func (v Version) PatchVersion() int64 {
	if len(v.Segments) < 3 {
		return v.Patch
	}
	return v.Segments[2]
}

// Less reports whether v has lower precedence than other.
func (v Version) Less(other Version) bool { return v.Compare(other) < 0 }

// Greater reports whether v has higher precedence than other.
func (v Version) Greater(other Version) bool { return v.Compare(other) > 0 }

// Equal reports whether v and other have the same precedence.
func (v Version) Equal(other Version) bool { return v.Compare(other) == 0 }

func comparePrereleaseIdentifier(a, b string) int {
	an, bn := isDigits(a), isDigits(b)
	switch {
	case an && bn:
		aa, bb := trimLeadingZeroes(a), trimLeadingZeroes(b)
		if len(aa) != len(bb) {
			if len(aa) < len(bb) {
				return -1
			}
			return 1
		}
		if aa < bb {
			return -1
		}
		if aa > bb {
			return 1
		}
		return 0
	case an && !bn:
		return -1
	case !an && bn:
		return 1
	default:
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
}

// CompareVersions compares two version strings.  Invalid version strings use a
// deterministic loose comparison instead of producing an error, which keeps
// matching code simple.  Use CompareSemver when errors matter.
func CompareVersions(a, b string) int {
	va, ea := parseVersion(a)
	vb, eb := parseVersion(b)
	if ea == nil && eb == nil {
		// SemVer precedence deliberately ignores build metadata.
		return va.Compare(vb)
	}
	return compareLoose(a, b)
}

// CompareSemver compares two version strings and returns an error when either
// input is not a valid version.
func CompareSemver(a, b string) (int, error) {
	va, err := ParseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := ParseVersion(b)
	if err != nil {
		return 0, err
	}
	return va.Compare(vb), nil
}

// Compare is an alias for CompareVersions.
func Compare(a, b string) int { return CompareVersions(a, b) }

func compareLoose(a, b string) int {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == b {
		return 0
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ad, bd := isDigitByte(a[i]), isDigitByte(b[j])
		switch {
		case ad && bd:
			ai, aj := i, j
			for ai < len(a) && isDigitByte(a[ai]) {
				ai++
			}
			for aj < len(b) && isDigitByte(b[aj]) {
				aj++
			}
			aa := trimLeadingZeroes(a[i:ai])
			bb := trimLeadingZeroes(b[j:aj])
			if len(aa) != len(bb) {
				if len(aa) < len(bb) {
					return -1
				}
				return 1
			}
			if aa < bb {
				return -1
			}
			if aa > bb {
				return 1
			}
			i, j = ai, aj
		case ad != bd:
			// Numeric tokens sort after alphabetic tokens in the loose
			// fallback.
			if ad {
				return 1
			}
			return -1
		default:
			if a[i] < b[j] {
				return -1
			}
			if a[i] > b[j] {
				return 1
			}
			i++
			j++
		}
	}
	if i < len(a) {
		return 1
	}
	if j < len(b) {
		return -1
	}
	return 0
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }
