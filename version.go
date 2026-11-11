package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a three-segment non-negative integer version with an optional
// dot-separated pre-release identifier list, per the in-task rules:
//
//	MAJOR.MINOR.PATCH[-ident.ident...]
//
// A version with no pre-release identifiers is a normal release and orders
// higher than any pre-release of the same core.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 []string // nil or empty means a normal release
}

// IsRelease reports whether v is a normal release (no pre-release identifiers).
func (v Version) IsRelease() bool { return len(v.Pre) == 0 }

// String returns the canonical textual form of v.
func (v Version) String() string {
	var b strings.Builder
	b.WriteString(strconv.FormatUint(v.Major, 10))
	b.WriteByte('.')
	b.WriteString(strconv.FormatUint(v.Minor, 10))
	b.WriteByte('.')
	b.WriteString(strconv.FormatUint(v.Patch, 10))
	if len(v.Pre) > 0 {
		b.WriteByte('-')
		b.WriteString(strings.Join(v.Pre, "."))
	}
	return b.String()
}

// Parse parses a version string of the form MAJOR.MINOR.PATCH with an
// optional "-ident.ident..." pre-release suffix. Numeric pre-release
// identifiers are normalized by stripping leading zeros so that equality
// and ordering are well defined.
func Parse(s string) (Version, error) {
	core, prePart := s, ""
	hasPre := false
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core, prePart, hasPre = s[:i], s[i+1:], true
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("netsec: version %q: core must have exactly 3 numeric segments", s)
	}
	var nums [3]uint64
	for i, p := range parts {
		if !isNumeric(p) {
			return Version{}, fmt.Errorf("netsec: version %q: segment %q is not a non-negative integer", s, p)
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("netsec: version %q: segment %q out of range", s, p)
		}
		nums[i] = n
	}
	var pre []string
	if hasPre {
		ids := strings.Split(prePart, ".")
		pre = make([]string, 0, len(ids))
		for _, id := range ids {
			nid, err := normalizeIdent(id)
			if err != nil {
				return Version{}, fmt.Errorf("netsec: version %q: %v", s, err)
			}
			pre = append(pre, nid)
		}
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: pre}, nil
}

// MustParse is like Parse but panics on error. Intended for constants and tests.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// NewVersion builds a Version from parts, validating and normalizing the
// pre-release identifiers.
func NewVersion(major, minor, patch uint64, pre ...string) (Version, error) {
	ids := make([]string, 0, len(pre))
	for _, id := range pre {
		nid, err := normalizeIdent(id)
		if err != nil {
			return Version{}, err
		}
		ids = append(ids, nid)
	}
	return Version{Major: major, Minor: minor, Patch: patch, Pre: ids}, nil
}

// Compare orders two versions: core segments numerically, then pre-release
// identifiers element-wise. Numeric identifiers compare as integers, other
// identifiers compare byte-wise, and numeric identifiers sort before
// non-numeric ones. A normal release sorts above every pre-release of the
// same core. It returns -1, 0 or +1.
func Compare(a, b Version) int {
	if c := cmpUint64(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmpUint64(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmpUint64(a.Patch, b.Patch); c != 0 {
		return c
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1 // normal release > pre-release
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := compareIdent(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(a.Pre), len(b.Pre))
}

// Equal reports whether a and b denote the same version.
func (a Version) Equal(b Version) bool { return Compare(a, b) == 0 }

// adjacent reports whether no version sorts strictly between a and b.
// It requires a < b. Because pre-release identifiers are arbitrary strings,
// versions are dense almost everywhere; the only gaps with no version in
// between are:
//   - (X, X.0) where X.0 appends the identifier "0" to X's pre-release list;
//   - (M.m.p, M.m.(p+1)-0) across a patch boundary with a release below.
func adjacent(a, b Version) bool {
	if Compare(a, b) >= 0 {
		return false
	}
	if a.Major == b.Major && a.Minor == b.Minor && a.Patch == b.Patch {
		if len(a.Pre) == 0 || len(b.Pre) != len(a.Pre)+1 {
			return false
		}
		for i := range a.Pre {
			if a.Pre[i] != b.Pre[i] {
				return false
			}
		}
		return b.Pre[len(a.Pre)] == "0"
	}
	return a.Major == b.Major && a.Minor == b.Minor && b.Patch == a.Patch+1 &&
		len(a.Pre) == 0 && len(b.Pre) == 1 && b.Pre[0] == "0"
}

func compareIdent(a, b string) int {
	an, bn := isNumeric(a), isNumeric(b)
	switch {
	case an && bn:
		// Identifiers are normalized (no leading zeros), so integer order
		// is length order then lexicographic order.
		if c := cmpInt(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case an:
		return -1 // numeric identifiers sort before alphanumeric ones
	case bn:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func isNumeric(s string) bool {
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

func normalizeIdent(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("empty pre-release identifier")
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
			return "", fmt.Errorf("pre-release identifier %q contains invalid byte %#U", id, c)
		}
	}
	if isNumeric(id) {
		id = strings.TrimLeft(id, "0")
		if id == "" {
			id = "0"
		}
	}
	return id, nil
}

func cmpUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
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
