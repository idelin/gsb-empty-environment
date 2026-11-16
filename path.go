package netsec

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const hardenedOffset uint32 = 1 << 31

// Fragment is one component of an absolute BIP-32 derivation path.
type Fragment struct {
	Index    uint32
	Hardened bool
}

// String renders the fragment in the conventional m/0' notation.
func (f Fragment) String() string {
	if f.Hardened {
		return strconv.FormatUint(uint64(f.Index), 10) + "'"
	}
	return strconv.FormatUint(uint64(f.Index), 10)
}

// Compare orders fragments by index and then by the hardened bit.
func (f Fragment) Compare(other Fragment) int {
	switch {
	case f.Index < other.Index:
		return -1
	case f.Index > other.Index:
		return 1
	case f.Hardened == other.Hardened:
		return 0
	case !f.Hardened:
		return -1
	default:
		return 1
	}
}

// Path is an absolute wallet derivation path. The empty path is the master
// node, rendered as "m".
type Path []Fragment

// ParsePath parses paths such as "m/44'/0'/0'/0/7". The leading "m/" is
// optional. Hardened fragments may use ', h, H, or the typographic
// apostrophe. A raw BIP-32 index with the high bit set is also accepted.
func ParsePath(text string) (Path, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty derivation path")
	}

	if text == "m" || text == "M" {
		return Path{}, nil
	}
	if strings.HasPrefix(text, "m/") || strings.HasPrefix(text, "M/") {
		text = text[2:]
	}
	if text == "" || strings.HasPrefix(text, "/") || strings.HasSuffix(text, "/") {
		return nil, fmt.Errorf("invalid derivation path %q", text)
	}

	parts := strings.Split(text, "/")
	path := make(Path, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("empty path fragment")
		}

		hardened := false
		if strings.HasSuffix(part, "\u2019") {
			hardened = true
			part = strings.TrimSuffix(part, "\u2019")
		} else {
			switch part[len(part)-1] {
			case '\'', 'h', 'H':
				hardened = true
				part = part[:len(part)-1]
			}
		}
		if part == "" {
			return nil, fmt.Errorf("missing path fragment index")
		}

		index, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid path fragment %q: %w", part, err)
		}
		if index&uint64(hardenedOffset) != 0 {
			if hardened {
				return nil, fmt.Errorf("path fragment %q has both an explicit high bit and a hardened marker", part)
			}
			hardened = true
			index &^= uint64(hardenedOffset)
		}
		if index > uint64(^uint32(0)>>1) {
			return nil, fmt.Errorf("path fragment index %d is out of range", index)
		}

		path = append(path, Fragment{Index: uint32(index), Hardened: hardened})
	}
	return path, nil
}

// MustParsePath is ParsePath for static paths.
func MustParsePath(text string) Path {
	path, err := ParsePath(text)
	if err != nil {
		panic(err)
	}
	return path
}

// String renders the path in m/0' notation.
func (p Path) String() string {
	if len(p) == 0 {
		return "m"
	}
	var b strings.Builder
	b.WriteString("m/")
	for i, fragment := range p {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(fragment.String())
	}
	return b.String()
}

// Clone returns an independent copy of the path.
func (p Path) Clone() Path {
	if p == nil {
		return nil
	}
	clone := make(Path, len(p))
	copy(clone, p)
	return clone
}

// Equal reports whether two paths identify the same node.
func (p Path) Equal(other Path) bool {
	if len(p) != len(other) {
		return false
	}
	for i := range p {
		if p[i] != other[i] {
			return false
		}
	}
	return true
}

// HasPrefix reports whether prefix is a prefix of p, including equality.
func (p Path) HasPrefix(prefix Path) bool {
	if len(prefix) > len(p) {
		return false
	}
	for i := range prefix {
		if p[i] != prefix[i] {
			return false
		}
	}
	return true
}

// IsAncestorOf reports whether p is a strict ancestor of other.
func (p Path) IsAncestorOf(other Path) bool {
	return len(p) < len(other) && other.HasPrefix(p)
}

// Parent returns the immediate parent of p. The master path has no parent.
func (p Path) Parent() (Path, bool) {
	if len(p) == 0 {
		return nil, false
	}
	return p[:len(p)-1].Clone(), true
}

// Last returns the final fragment of p. The master path has none.
func (p Path) Last() (Fragment, bool) {
	if len(p) == 0 {
		return Fragment{}, false
	}
	return p[len(p)-1], true
}

// Compare orders paths lexicographically by fragments.
func (p Path) Compare(other Path) int {
	limit := len(p)
	if len(other) < limit {
		limit = len(other)
	}
	for i := 0; i < limit; i++ {
		if cmp := p[i].Compare(other[i]); cmp != 0 {
			return cmp
		}
	}
	switch {
	case len(p) < len(other):
		return -1
	case len(p) > len(other):
		return 1
	default:
		return 0
	}
}

// MarshalText implements encoding.TextMarshaler.
func (p Path) MarshalText() ([]byte, error) {
	return []byte(p.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (p *Path) UnmarshalText(text []byte) error {
	parsed, err := ParsePath(string(text))
	if err != nil {
		return err
	}
	*p = parsed
	return nil
}

// MarshalJSON encodes a path as its string form.
func (p Path) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

// UnmarshalJSON decodes a path from its string form.
func (p *Path) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("derivation path must be a string: %w", err)
	}
	return p.UnmarshalText([]byte(text))
}
