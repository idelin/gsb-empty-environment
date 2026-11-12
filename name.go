// Package netsec provides a DNS name object that keeps the original
// label bytes for forensic display separate from a canonical
// comparison key.
package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// Wire-format limits, checked against decoded label bytes.
const (
	MaxLabelLength = 63
	MaxNameLength  = 255
)

// Name is a parsed DNS name. It retains the original bytes of every
// label exactly as decoded, plus a canonical key for comparison and
// cache indexing. Case folding applies to ASCII letters only.
type Name struct {
	labels [][]byte
	key    string
}

// Parse decodes a presentation-form name. A backslash followed by a
// non-digit quotes that byte literally; a backslash followed by three
// decimal digits encodes an arbitrary byte value. Unescaped dots
// separate labels. A single trailing dot marks the root and is
// accepted; "" and "." denote the root name itself. Length limits are
// enforced on the decoded bytes, not on the escaped input.
func Parse(input string) (*Name, error) {
	if input == "" || input == "." {
		return &Name{key: buildKey(nil)}, nil
	}
	var labels [][]byte
	var cur []byte
	flush := func() error {
		if len(cur) == 0 {
			return errors.New("netsec: empty label")
		}
		if len(cur) > MaxLabelLength {
			return fmt.Errorf("netsec: label of %d bytes exceeds %d", len(cur), MaxLabelLength)
		}
		label := make([]byte, len(cur))
		copy(label, cur)
		labels = append(labels, label)
		cur = nil
		return nil
	}
	for i := 0; i < len(input); {
		switch c := input[i]; {
		case c == '.':
			if err := flush(); err != nil {
				return nil, err
			}
			i++
		case c == '\\':
			if i+1 >= len(input) {
				return nil, errors.New("netsec: trailing backslash")
			}
			if d := input[i+1]; d >= '0' && d <= '9' {
				if i+3 >= len(input) {
					return nil, errors.New("netsec: truncated decimal escape")
				}
				d1, d2, d3 := input[i+1], input[i+2], input[i+3]
				if !isDigit(d1) || !isDigit(d2) || !isDigit(d3) {
					return nil, errors.New("netsec: malformed decimal escape")
				}
				v := int(d1-'0')*100 + int(d2-'0')*10 + int(d3-'0')
				if v > 255 {
					return nil, fmt.Errorf("netsec: decimal escape \\%d out of range", v)
				}
				cur = append(cur, byte(v))
				i += 4
			} else {
				cur = append(cur, d)
				i += 2
			}
		default:
			cur = append(cur, c)
			i++
		}
	}
	if len(cur) > 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return newName(labels)
}

// FromLabels builds a Name from raw label bytes, copying them.
func FromLabels(labels ...[]byte) (*Name, error) {
	cp := make([][]byte, len(labels))
	for i, l := range labels {
		if len(l) == 0 {
			return nil, errors.New("netsec: empty label")
		}
		if len(l) > MaxLabelLength {
			return nil, fmt.Errorf("netsec: label of %d bytes exceeds %d", len(l), MaxLabelLength)
		}
		cp[i] = append([]byte(nil), l...)
	}
	return newName(cp)
}

func newName(labels [][]byte) (*Name, error) {
	wire := 1 // root label
	for _, l := range labels {
		wire += 1 + len(l)
	}
	if wire > MaxNameLength {
		return nil, fmt.Errorf("netsec: name of %d wire bytes exceeds %d", wire, MaxNameLength)
	}
	return &Name{labels: labels, key: buildKey(labels)}, nil
}

// Labels returns a copy of the original, unmodified label bytes.
func (n *Name) Labels() [][]byte {
	out := make([][]byte, len(n.labels))
	for i, l := range n.labels {
		out[i] = append([]byte(nil), l...)
	}
	return out
}

// Key is the canonical comparison and cache key. Labels are folded
// (ASCII only) and length-prefixed, so distinct label sequences can
// never collide regardless of embedded dot or zero bytes.
func (n *Name) Key() string { return n.key }

// Equal reports whether two names share the same canonical key.
func (n *Name) Equal(other *Name) bool { return n.key == other.key }

// String renders the escaped display form. Parsing it yields the
// identical label bytes. Printable ASCII is emitted literally except
// '.' and '\\', which are quoted so a label separator can never leak
// into a label; every other byte uses a \DDD decimal escape.
func (n *Name) String() string {
	if len(n.labels) == 0 {
		return "."
	}
	var sb strings.Builder
	for i, l := range n.labels {
		if i > 0 {
			sb.WriteByte('.')
		}
		writeEscaped(&sb, l)
	}
	return sb.String()
}

func writeEscaped(sb *strings.Builder, label []byte) {
	for _, c := range label {
		switch {
		case c == '.' || c == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case c >= 0x21 && c <= 0x7e:
			sb.WriteByte(c)
		default:
			sb.WriteByte('\\')
			sb.WriteByte('0' + c/100)
			sb.WriteByte('0' + c/10%10)
			sb.WriteByte('0' + c%10)
		}
	}
}

func buildKey(labels [][]byte) string {
	var sb strings.Builder
	for _, l := range labels {
		sb.WriteByte(byte(len(l)))
		for _, c := range l {
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
