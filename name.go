package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// Name is a canonical, absolute DNS name.  Internally it is stored as a
// length-prefixed wire name containing lower-case (ASCII) label bytes.  This
// makes Name comparable and safe to use as a map key while preserving the
// octets needed by DNSSEC canonical ordering.
type Name struct {
	wire string
}

// ParseName parses a presentation-format DNS name and returns its canonical
// form.  A trailing dot is optional.  The names "." and an empty Name both
// represent the root.
func ParseName(s string) (Name, error) {
	if s == "" {
		return Name{}, errors.New("netsec: empty domain name")
	}
	if s == "." {
		return Name{}, nil
	}

	labels := make([]string, 0, 4)
	label := make([]byte, 0, 16)
	wireLen := 1 // terminating root label

	for i := 0; i < len(s); {
		switch s[i] {
		case '.':
			if len(label) == 0 {
				return Name{}, fmt.Errorf("netsec: empty label in %q", s)
			}
			if len(label) > 63 {
				return Name{}, fmt.Errorf("netsec: label too long in %q", s)
			}
			wireLen += 1 + len(label)
			if wireLen > 255 {
				return Name{}, fmt.Errorf("netsec: name too long in %q", s)
			}
			labels = append(labels, string(label))
			label = label[:0]
			i++
		case '\\':
			if i+1 >= len(s) {
				return Name{}, fmt.Errorf("netsec: trailing escape in %q", s)
			}
			if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
				v := int(s[i+1]-'0')*100 + int(s[i+2]-'0')*10 + int(s[i+3]-'0')
				if v > 255 {
					return Name{}, fmt.Errorf("netsec: invalid decimal escape in %q", s)
				}
				label = append(label, asciiLower(byte(v)))
				i += 4
				continue
			}
			label = append(label, asciiLower(s[i+1]))
			i += 2
		default:
			label = append(label, asciiLower(s[i]))
			i++
		}
	}

	if len(label) > 0 {
		if len(label) > 63 {
			return Name{}, fmt.Errorf("netsec: label too long in %q", s)
		}
		wireLen += 1 + len(label)
		if wireLen > 255 {
			return Name{}, fmt.Errorf("netsec: name too long in %q", s)
		}
		labels = append(labels, string(label))
	} else if !strings.HasSuffix(s, ".") {
		return Name{}, fmt.Errorf("netsec: empty domain name")
	}

	if len(labels) == 0 {
		return Name{}, nil
	}

	var wire bytes.Buffer
	wire.Grow(wireLen)
	for _, l := range labels {
		wire.WriteByte(byte(len(l)))
		wire.WriteString(l)
	}
	wire.WriteByte(0)
	return Name{wire: wire.String()}, nil
}

// MustParseName is ParseName, except that it panics on an invalid name.
func MustParseName(s string) Name {
	n, err := ParseName(s)
	if err != nil {
		panic(err)
	}
	return n
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func asciiLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func (n Name) canonicalWire() string {
	if n.wire == "" {
		return "\x00"
	}
	return n.wire
}

func (n Name) labels() []string {
	wire := n.canonicalWire()
	labels := make([]string, 0, 4)
	for i := 0; i < len(wire) && wire[i] != 0; {
		l := int(wire[i])
		i++
		labels = append(labels, wire[i:i+l])
		i += l
	}
	return labels
}

// String returns the canonical presentation form, always with a trailing dot.
func (n Name) String() string {
	labels := n.labels()
	if len(labels) == 0 {
		return "."
	}
	var b strings.Builder
	for _, label := range labels {
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case c == '.' || c == '\\':
				b.WriteByte('\\')
				b.WriteByte(c)
			case c < 0x21 || c > 0x7e:
				fmt.Fprintf(&b, "\\%03d", c)
			default:
				b.WriteByte(c)
			}
		}
		b.WriteByte('.')
	}
	return b.String()
}

// Labels returns a copy of the left-to-right (leaf-to-root) labels.
func (n Name) Labels() []string {
	labels := n.labels()
	out := make([]string, len(labels))
	copy(out, labels)
	return out
}

// Compare returns -1, 0, or 1 according to DNSSEC canonical name ordering
// (RFC 4034 section 6.1): labels are compared from the root side toward the
// leaf, byte by byte, and an ancestor sorts before its descendants.
func (n Name) Compare(other Name) int {
	a := n.labels()
	b := other.labels()
	i, j := len(a)-1, len(b)-1
	for i >= 0 && j >= 0 {
		if c := bytes.Compare([]byte(a[i]), []byte(b[j])); c != 0 {
			return c
		}
		i--
		j--
	}
	switch {
	case i < 0 && j < 0:
		return 0
	case i < 0:
		return -1
	default:
		return 1
	}
}

// Parent returns the immediate parent.  The root is its own parent and the
// second result is false.
func (n Name) Parent() (Name, bool) {
	labels := n.labels()
	if len(labels) == 0 {
		return Name{}, false
	}
	if len(labels) == 1 {
		return Name{}, true
	}
	var wire bytes.Buffer
	for _, label := range labels[1:] {
		wire.WriteByte(byte(len(label)))
		wire.WriteString(label)
	}
	wire.WriteByte(0)
	return Name{wire: wire.String()}, true
}

// IsSubdomainOf reports whether n is equal to or below ancestor.
func (n Name) IsSubdomainOf(ancestor Name) bool {
	a := n.labels()
	b := ancestor.labels()
	if len(a) < len(b) {
		return false
	}
	for i := 1; i <= len(b); i++ {
		if a[len(a)-i] != b[len(b)-i] {
			return false
		}
	}
	return true
}

// IsAncestorOf reports whether n is a strict ancestor of descendant.
func (n Name) IsAncestorOf(descendant Name) bool {
	return len(n.labels()) < len(descendant.labels()) && descendant.IsSubdomainOf(n)
}

func nameAt(parent Name, label string) Name {
	var wire bytes.Buffer
	wire.WriteByte(byte(len(label)))
	wire.WriteString(label)
	wire.WriteString(parent.canonicalWire())
	return Name{wire: wire.String()}
}
