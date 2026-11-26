package dnssec

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Name is a domain name in normal (left-to-right) label order. The zero
// value is the root name. Labels are stored without the terminating root
// label.
type Name struct {
	Labels []string
}

// ParseName parses a presentation-format domain name. A trailing dot is
// optional. Escaped dots and the RFC 1035 \DDD decimal escape are accepted.
func ParseName(s string) (Name, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "." {
		return Name{}, nil
	}
	if strings.HasPrefix(s, ".") {
		return Name{}, errors.New("dnssec: domain name has an empty leading label")
	}

	var labels []string
	var label []byte
	flush := func() error {
		if len(label) == 0 {
			return errors.New("dnssec: domain name has an empty label")
		}
		if len(label) > 63 {
			return errors.New("dnssec: domain name label exceeds 63 octets")
		}
		labels = append(labels, string(label))
		label = nil
		return nil
	}

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '.':
			if err := flush(); err != nil {
				return Name{}, err
			}
		case '\\':
			if i+1 >= len(s) {
				return Name{}, errors.New("dnssec: trailing escape in domain name")
			}
			if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
				v, _ := strconv.Atoi(s[i+1 : i+4])
				if v > 255 {
					return Name{}, fmt.Errorf("dnssec: invalid decimal escape %03d", v)
				}
				label = append(label, byte(v))
				i += 3
			} else {
				label = append(label, s[i+1])
				i++
			}
		default:
			label = append(label, s[i])
		}
	}

	if len(label) > 0 {
		if err := flush(); err != nil {
			return Name{}, err
		}
	} else if !strings.HasSuffix(s, ".") || len(s) == 1 {
		return Name{}, errors.New("dnssec: domain name has an empty label")
	}

	n := Name{Labels: labels}
	for i := range n.Labels {
		n.Labels[i] = asciiLower(n.Labels[i])
	}
	if err := validateName(n); err != nil {
		return Name{}, err
	}
	return n, nil
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func asciiLower(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// MustParseName is ParseName for fixed test data or known-valid input.
func MustParseName(s string) Name {
	n, err := ParseName(s)
	if err != nil {
		panic(err)
	}
	return n
}

// String returns the canonical presentation form with a trailing dot.
func (n Name) String() string {
	if len(n.Labels) == 0 {
		return "."
	}
	var b strings.Builder
	for i, l := range n.Labels {
		if i > 0 {
			b.WriteByte('.')
		}
		for j := 0; j < len(l); j++ {
			switch l[j] {
			case '.', '\\':
				b.WriteByte('\\')
				b.WriteByte(l[j])
			default:
				if l[j] < 0x20 || l[j] > 0x7e {
					b.WriteByte('\\')
					b.WriteString(fmt.Sprintf("%03d", l[j]))
				} else {
					b.WriteByte(l[j])
				}
			}
		}
	}
	b.WriteByte('.')
	return b.String()
}

// LabelCount returns the number of labels, excluding the root label.
func (n Name) LabelCount() int { return len(n.Labels) }

// Wire returns the canonical RFC 4034 wire encoding: lower-case labels,
// label length octets, and a terminating zero octet.
func (n Name) Wire() []byte {
	out := make([]byte, 0, 1+len(n.Labels))
	for _, l := range n.Labels {
		out = append(out, byte(len(l)))
		out = append(out, []byte(asciiLower(l))...)
	}
	out = append(out, 0)
	return out
}

// CanonicalNameWire validates n and returns its canonical wire encoding.
func CanonicalNameWire(n Name) ([]byte, error) {
	if err := validateName(n); err != nil {
		return nil, err
	}
	return n.Wire(), nil
}

func validateName(n Name) error {
	if len(n.Labels) > 127 {
		return errors.New("dnssec: domain name has too many labels")
	}
	wireLen := 1
	for _, l := range n.Labels {
		if l == "" {
			return errors.New("dnssec: domain name has an empty label")
		}
		if len(l) > 63 {
			return errors.New("dnssec: domain name label exceeds 63 octets")
		}
		wireLen += 1 + len(l)
	}
	if wireLen > 255 {
		return errors.New("dnssec: domain name exceeds 255 octets")
	}
	return nil
}

// CompareNames implements RFC 4034 section 6.1 canonical name ordering.
// Labels are compared from the rightmost (least significant) label toward
// the left. Comparison is case-insensitive and uses unsigned octet order.
func CompareNames(a, b Name) int {
	ai, bi := len(a.Labels)-1, len(b.Labels)-1
	for ai >= 0 && bi >= 0 {
		cmp := bytes.Compare([]byte(asciiLower(a.Labels[ai])), []byte(asciiLower(b.Labels[bi])))
		if cmp != 0 {
			return cmp
		}
		ai--
		bi--
	}
	switch {
	case ai < 0 && bi < 0:
		return 0
	case ai < 0:
		return -1
	default:
		return 1
	}
}

// CompareNameStrings parses and canonically compares two domain names.
func CompareNameStrings(a, b string) (int, error) {
	an, err := ParseName(a)
	if err != nil {
		return 0, err
	}
	bn, err := ParseName(b)
	if err != nil {
		return 0, err
	}
	return CompareNames(an, bn), nil
}

// CanonicalNameCompare is a convenience alias for CompareNames.
func CanonicalNameCompare(a, b Name) int { return CompareNames(a, b) }

// EqualNames compares names using DNS case-insensitive semantics.
func EqualNames(a, b Name) bool { return CompareNames(a, b) == 0 }

// IsSubdomainOf reports whether n is equal to or below parent. Root is a
// parent of every name, including root itself.
func IsSubdomainOf(n, parent Name) bool {
	if len(n.Labels) < len(parent.Labels) {
		return false
	}
	for i := 1; i <= len(parent.Labels); i++ {
		if asciiLower(n.Labels[len(n.Labels)-i]) != asciiLower(parent.Labels[len(parent.Labels)-i]) {
			return false
		}
	}
	return true
}

// IsProperSubdomainOf reports whether n is below parent.
func IsProperSubdomainOf(n, parent Name) bool {
	return len(n.Labels) > len(parent.Labels) && IsSubdomainOf(n, parent)
}

// Parent returns the name with its leftmost label removed.
func (n Name) Parent() (Name, bool) {
	if len(n.Labels) == 0 {
		return Name{}, false
	}
	return Name{Labels: append([]string(nil), n.Labels[1:]...)}, true
}

// Ancestor returns the suffix of n with count labels. count zero is root.
func (n Name) Ancestor(count int) (Name, error) {
	if count < 0 || count > len(n.Labels) {
		return Name{}, errors.New("dnssec: invalid ancestor label count")
	}
	return Name{Labels: append([]string(nil), n.Labels[len(n.Labels)-count:]...)}, nil
}

// Child returns a copy of n with label prepended as its leftmost label.
func (n Name) Child(label string) Name {
	out := make([]string, 0, len(n.Labels)+1)
	out = append(out, asciiLower(label))
	out = append(out, n.Labels...)
	return Name{Labels: out}
}

// Wildcard returns the wildcard owner name *.n.
func (n Name) Wildcard() Name {
	return Name{Labels: append([]string{"*"}, n.Labels...)}
}

// ParseWireName decodes an uncompressed wire-format name at offset.
func ParseWireName(wire []byte, offset int) (Name, int, error) {
	var labels []string
	start := offset
	for {
		if offset >= len(wire) {
			return Name{}, 0, errors.New("dnssec: truncated wire-format name")
		}
		l := int(wire[offset])
		offset++
		if l == 0 {
			break
		}
		if l&0xc0 != 0 {
			return Name{}, 0, errors.New("dnssec: compressed names are not accepted here")
		}
		if offset+l > len(wire) {
			return Name{}, 0, errors.New("dnssec: truncated wire-format label")
		}
		labels = append(labels, asciiLower(string(wire[offset:offset+l])))
		offset += l
	}
	n := Name{Labels: labels}
	if offset-start > 255 {
		return Name{}, 0, errors.New("dnssec: wire-format name exceeds 255 octets")
	}
	return n, offset, nil
}

// MarshalWireName validates and encodes an uncompressed wire-format name.
func MarshalWireName(n Name) ([]byte, error) { return CanonicalNameWire(n) }

// ValidateName checks DNS label and total wire-length limits.
func ValidateName(n Name) error { return validateName(n) }

// CanonicalCompareNames is an alias for CompareNames.
func CanonicalCompareNames(a, b Name) int { return CompareNames(a, b) }

// CompareCanonicalNames is an alias for CompareNames.
func CompareCanonicalNames(a, b Name) int { return CompareNames(a, b) }
