package netsec

import "fmt"

// prereleaseID is one dot-separated identifier of a semver prerelease.
type prereleaseID struct {
	numeric bool
	num     uint64
	text    string
}

// Version is a parsed semantic version (https://semver.org).
//
// Build metadata following '+' is parsed for validation but is not part of
// Compare or String, because semver states that build metadata is ignored when
// determining version precedence. Two versions that differ only in build
// metadata therefore compare equal and canonicalize to the same string.
type Version struct {
	major     uint64
	minor     uint64
	patch     uint64
	pre       []prereleaseID
	preRaw    string
	hasPre    bool
	build     string
	canonical string
}

// ParseVersion parses a strict semantic version, optionally prefixed with 'v'.
// Build metadata is accepted but ignored for precedence purposes.
func ParseVersion(s string) (Version, error) {
	orig := s
	if s == "" {
		return Version{}, fmt.Errorf("netsec: empty version string")
	}
	if s[0] == 'v' || s[0] == 'V' {
		s = s[1:]
	}
	if s == "" {
		return Version{}, fmt.Errorf("netsec: invalid version %q", orig)
	}

	core, pre, build, hasPre, err := splitVersion(s)
	if err != nil {
		return Version{}, fmt.Errorf("netsec: invalid version %q: %w", orig, err)
	}
	major, minor, patch, err := parseCore(core)
	if err != nil {
		return Version{}, fmt.Errorf("netsec: invalid version %q: %w", orig, err)
	}

	ids, preRaw, err := parsePrerelease(pre, hasPre)
	if err != nil {
		return Version{}, fmt.Errorf("netsec: invalid version %q: %w", orig, err)
	}
	if build != "" {
		if err := validateIdentifiers(build); err != nil {
			return Version{}, fmt.Errorf("netsec: invalid version %q: invalid build metadata: %w", orig, err)
		}
	}

	canonical := fmt.Sprintf("%d.%d.%d", major, minor, patch)
	if hasPre {
		canonical += "-" + preRaw
	}
	return Version{
		major:     major,
		minor:     minor,
		patch:     patch,
		pre:       ids,
		preRaw:    preRaw,
		hasPre:    hasPre,
		build:     build,
		canonical: canonical,
	}, nil
}

// String returns the canonical version without the leading 'v' and without
// build metadata.
func (v Version) String() string {
	return v.canonical
}

// IsPrerelease reports whether the version carries a prerelease tag.
func (v Version) IsPrerelease() bool {
	return v.hasPre
}

// Compare returns -1, 0, or 1 as a sorts before, equal to, or after b
// according to semver precedence. Build metadata is ignored.
func (a Version) Compare(b Version) int {
	if c := cmpUint(a.major, b.major); c != 0 {
		return c
	}
	if c := cmpUint(a.minor, b.minor); c != 0 {
		return c
	}
	if c := cmpUint(a.patch, b.patch); c != 0 {
		return c
	}
	return comparePrerelease(a.pre, b.pre, a.hasPre, b.hasPre)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// comparePrerelease implements semver clause 11: a version without a
// prerelease has higher precedence than a version with one; otherwise
// prerelease identifiers are compared left to right, numeric identifiers
// numerically, alphanumeric identifiers lexically (ASCII), and a shorter set
// of identifiers sorts before a longer one when it is a prefix.
func comparePrerelease(a, b []prereleaseID, aHasPre, bHasPre bool) int {
	switch {
	case !aHasPre && !bHasPre:
		return 0
	case !aHasPre:
		return 1
	case !bHasPre:
		return -1
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		x, y := a[i], b[i]
		switch {
		case x.numeric && !y.numeric:
			return -1
		case !x.numeric && y.numeric:
			return 1
		case x.numeric && y.numeric:
			if c := cmpUint(x.num, y.num); c != 0 {
				return c
			}
		default:
			if x.text < y.text {
				return -1
			}
			if x.text > y.text {
				return 1
			}
		}
	}
	return cmpUint(uint64(len(a)), uint64(len(b)))
}

func splitVersion(s string) (core, pre, build string, hasPre bool, err error) {
	plus := indexByte(s, '+')
	if plus >= 0 {
		build = s[plus+1:]
		s = s[:plus]
		if build == "" {
			return "", "", "", false, fmt.Errorf("empty build metadata")
		}
	}
	if dash := indexByte(s, '-'); dash >= 0 {
		pre = s[dash+1:]
		core = s[:dash]
		hasPre = true
		if pre == "" {
			return "", "", "", false, fmt.Errorf("empty prerelease")
		}
	} else {
		core = s
	}
	return core, pre, build, hasPre, nil
}

func parseCore(core string) (uint64, uint64, uint64, error) {
	d1 := indexByte(core, '.')
	if d1 <= 0 {
		return 0, 0, 0, fmt.Errorf("expected major.minor.patch")
	}
	rest := core[d1+1:]
	d2 := indexByte(rest, '.')
	if d2 <= 0 {
		return 0, 0, 0, fmt.Errorf("expected major.minor.patch")
	}
	major, err := parseNumericID(core[:d1])
	if err != nil {
		return 0, 0, 0, err
	}
	minor, err := parseNumericID(rest[:d2])
	if err != nil {
		return 0, 0, 0, err
	}
	patch, err := parseNumericID(rest[d2+1:])
	if err != nil {
		return 0, 0, 0, err
	}
	return major, minor, patch, nil
}

func parseNumericID(s string) (uint64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty numeric identifier")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("non-numeric identifier %q", s)
		}
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("leading zero in numeric identifier %q", s)
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		if n > (^uint64(0)-uint64(s[i]-'0'))/10 {
			return 0, fmt.Errorf("numeric identifier %q overflows", s)
		}
		n = n*10 + uint64(s[i]-'0')
	}
	return n, nil
}

func parsePrerelease(pre string, hasPre bool) ([]prereleaseID, string, error) {
	if !hasPre {
		return nil, "", nil
	}
	parts := splitDots(pre)
	ids := make([]prereleaseID, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, "", fmt.Errorf("empty prerelease identifier")
		}
		numeric := true
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				numeric = false
				break
			}
		}
		if numeric {
			if len(part) > 1 && part[0] == '0' {
				return nil, "", fmt.Errorf("leading zero in prerelease identifier %q", part)
			}
			n, err := parseNumericID(part)
			if err != nil {
				return nil, "", err
			}
			ids = append(ids, prereleaseID{numeric: true, num: n})
			continue
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if !(isAlnum(c) || c == '-') {
				return nil, "", fmt.Errorf("illegal character in prerelease identifier %q", part)
			}
		}
		ids = append(ids, prereleaseID{numeric: false, text: part})
	}
	return ids, pre, nil
}

func validateIdentifiers(s string) error {
	for _, part := range splitDots(s) {
		if part == "" {
			return fmt.Errorf("empty identifier")
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if !(isAlnum(c) || c == '-') {
				return fmt.Errorf("illegal character in identifier %q", part)
			}
		}
	}
	return nil
}

func splitDots(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func isAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
