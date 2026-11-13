package netsec

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// PathSegment is one BIP-32 child-number in a derivation path. The hardened
// flag is kept separate from Index so that both m/0 and m/0' can be represented
// without using the high bit as an in-band marker.
type PathSegment struct {
	Index    uint32 `json:"index"`
	Hardened bool   `json:"hardened,omitempty"`
}

// Aliases used by different importers for one derivation path component.
type (
	PathFragment  = PathSegment
	PathComponent = PathSegment
	PathPart      = PathSegment
	Segment       = PathSegment
	ChildNumber   = PathSegment
)

// Path is an absolute wallet derivation path. The zero value is the root path,
// which is rendered as "m".
type Path []PathSegment

// DerivationPath is an alias kept for callers that prefer the longer name.
type DerivationPath = Path

// ParsePath accepts paths such as m, m/0, m/0h/1, and 0'/1. A leading "m" is
// optional. Hardened segments may use ', h, H, or the Unicode prime character.
func ParsePath(value string) (Path, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "m" || value == "M" {
		return Path{}, nil
	}

	raw := value
	if strings.HasPrefix(raw, "m/") || strings.HasPrefix(raw, "M/") {
		raw = raw[2:]
	} else if strings.HasPrefix(raw, "m") || strings.HasPrefix(raw, "M") {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPath, value)
	}
	if raw == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPath, value)
	}

	parts := strings.Split(raw, "/")
	path := make(Path, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("%w: empty child in %q", ErrInvalidPath, value)
		}

		hardened := false
		last, _ := utf8.DecodeLastRuneInString(part)
		switch last {
		case '\'', 'h', 'H', '\u2019':
			hardened = true
			part = part[:len(part)-1]
		}
		if part == "" {
			return nil, fmt.Errorf("%w: missing child number in %q", ErrInvalidPath, value)
		}

		index, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%w: child %q is not a uint32", ErrInvalidPath, part)
		}
		if index >= uint64(1)<<31 {
			if hardened {
				return nil, fmt.Errorf("%w: hardened index %q is too large", ErrInvalidPath, part)
			}
			index -= uint64(1) << 31
			hardened = true
		}
		path = append(path, PathSegment{Index: uint32(index), Hardened: hardened})
	}
	return path, nil
}

// MustParsePath is ParsePath for fixed paths in tests or configuration. It
// panics if value is malformed.
func MustParsePath(value string) Path {
	path, err := ParsePath(value)
	if err != nil {
		panic(err)
	}
	return path
}

// ParseDerivationPath is an alias for ParsePath.
func ParseDerivationPath(value string) (Path, error) {
	return ParsePath(value)
}

// NewPath is an alias for ParsePath.
func NewPath(value string) (Path, error) {
	return ParsePath(value)
}

// Child appends one child segment and returns a new path.
func (path Path) Child(index uint32, hardened bool) Path {
	next := make(Path, len(path)+1)
	copy(next, path)
	next[len(path)] = PathSegment{Index: index, Hardened: hardened}
	return next
}

// Append is the PathSegment form of Child.
func (path Path) Append(segment PathSegment) Path {
	return path.Child(segment.Index, segment.Hardened)
}

// Len returns the number of child segments.
func (path Path) Len() int {
	return len(path)
}

// Segments returns a copy of the path components.
func (path Path) Segments() []PathSegment {
	return clonePath(path)
}

func normalizeSegments(path Path) Path {
	normalized := clonePath(path)
	for i, segment := range normalized {
		if segment.Index >= uint32(1)<<31 {
			segment.Index -= uint32(1) << 31
			segment.Hardened = true
			normalized[i] = segment
		}
	}
	return normalized
}

// Parent returns the path without its last segment.
func (path Path) Parent() (Path, bool) {
	if len(path) == 0 {
		return Path{}, false
	}
	parent := make(Path, len(path)-1)
	copy(parent, path[:len(path)-1])
	return parent, true
}

// Last reports the final path segment.
func (path Path) Last() (PathSegment, bool) {
	if len(path) == 0 {
		return PathSegment{}, false
	}
	return path[len(path)-1], true
}

// IsRoot reports whether this is the wallet root path.
func (path Path) IsRoot() bool {
	return len(path) == 0
}

// HasPrefix reports whether prefix is a whole-segment prefix of path.
func (path Path) HasPrefix(prefix Path) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

// Equal compares two paths segment by segment.
func (path Path) Equal(other Path) bool {
	if len(path) != len(other) {
		return false
	}
	for i := range path {
		if path[i] != other[i] {
			return false
		}
	}
	return true
}

// String renders the canonical absolute form, including the m prefix.
func (path Path) String() string {
	if len(path) == 0 {
		return "m"
	}
	var b strings.Builder
	b.WriteString("m")
	for _, segment := range path {
		b.WriteByte('/')
		b.WriteString(strconv.FormatUint(uint64(segment.Index), 10))
		if segment.Hardened {
			b.WriteByte('\'')
		}
	}
	return b.String()
}

// ComparePaths orders paths lexicographically by child number and hardened
// marker, with a parent before all of its children.
func ComparePaths(a, b Path) int {
	common := len(a)
	if len(b) < common {
		common = len(b)
	}
	for i := 0; i < common; i++ {
		if a[i].Index < b[i].Index {
			return -1
		}
		if a[i].Index > b[i].Index {
			return 1
		}
		if a[i].Hardened == b[i].Hardened {
			continue
		}
		if !a[i].Hardened {
			return -1
		}
		return 1
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

// MarshalText makes Path usable in encoding.TextMarshaler based formats.
func (path Path) MarshalText() ([]byte, error) {
	return []byte(path.String()), nil
}

// UnmarshalText parses the canonical or shorthand path forms accepted by
// ParsePath.
func (path *Path) UnmarshalText(text []byte) error {
	parsed, err := ParsePath(string(text))
	if err != nil {
		return err
	}
	*path = parsed
	return nil
}

// MarshalJSON emits a path as its canonical string.
func (path Path) MarshalJSON() ([]byte, error) {
	return json.Marshal(path.String())
}

// UnmarshalJSON accepts either a path string or an array of PathSegment
// objects.
func (path *Path) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		parsed, err := ParsePath(text)
		if err != nil {
			return err
		}
		*path = parsed
		return nil
	}
	var segments []PathSegment
	if err := json.Unmarshal(data, &segments); err != nil {
		return fmt.Errorf("%w: expected path string or segment array", ErrInvalidPath)
	}
	*path = Path(segments)
	return nil
}
