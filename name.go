package netsec

import (
	"errors"
	"strings"
)

// maxLabelCount is the maximum number of labels in a domain name, excluding
// the root label (RFC 1035, section 3.1).
const maxLabelCount = 127

// maxNameOctets is the maximum wire length of a domain name including the
// terminating zero-length root label.
const maxNameOctets = 255

// Name is a parsed, presentation-independent domain name stored as its
// lower-cased labels without a root label and without a trailing dot.
//
// The empty Name (len(Labels) == 0) represents the root. A leading wildcard
// label is stored like any other label; wildcard semantics are handled by the
// label-counting helpers below.
type Name struct {
	Labels []string
}

// MustParseName parses s or panics. It is intended for package-internal tests
// and constant inputs. Production callers should use ParseName.
func MustParseName(s string) Name {
	n, err := ParseName(s)
	if err != nil {
		panic("netsec: MustParseName: " + err.Error())
	}
	return n
}

// ParseName parses a presentation-format domain name such as "host.example.com."
// or "host.example.com". Escapes (\\ddd and \\.) and the empty root ("." or "")
// are supported. The returned labels are lower-cased and never include a root
// label or a trailing dot.
func ParseName(s string) (Name, error) {
	if s == "" || s == "." {
		return Name{}, nil
	}

	var labels []string
	var cur strings.Builder
	flush := func() error {
		if cur.Len() == 0 {
			return errors.New("netsec: empty label in name")
		}
		if cur.Len() > 63 {
			return errors.New("netsec: label exceeds 63 octets")
		}
		labels = append(labels, strings.ToLower(cur.String()))
		cur.Reset()
		return nil
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			i++
			if i >= len(s) {
				return Name{}, errors.New("netsec: dangling escape")
			}
			if s[i] >= '0' && s[i] <= '9' {
				val := 0
				for d := 0; d < 3; d++ {
					if i >= len(s) || s[i] < '0' || s[i] > '9' {
						return Name{}, errors.New("netsec: malformed \\ddd escape")
					}
					val = val*10 + int(s[i]-'0')
					if d < 2 {
						i++
					}
				}
				if val > 255 {
					return Name{}, errors.New("netsec: \\ddd escape out of range")
				}
				cur.WriteByte(byte(val))
			} else {
				cur.WriteByte(s[i])
			}
		case c == '.':
			if err := flush(); err != nil {
				return Name{}, err
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		if err := flush(); err != nil {
			return Name{}, err
		}
	}

	if len(labels) > maxLabelCount {
		return Name{}, errors.New("netsec: name has too many labels")
	}
	return Name{Labels: labels}, nil
}

// ParseWireFormatName parses a single, uncompressed wire-format domain name.
// Pointer compression is rejected because RRSIG owner names and RDATA name
// fields must stand alone during validation.
func ParseWireFormatName(wire []byte) (Name, error) {
	var labels []string
	pos := 0
	for {
		if pos >= len(wire) {
			return Name{}, errors.New("netsec: truncated wire name")
		}
		length := int(wire[pos])
		pos++
		if length == 0 {
			break
		}
		if length&0xC0 != 0 {
			return Name{}, errors.New("netsec: compressed name is not permitted here")
		}
		if pos+length > len(wire) {
			return Name{}, errors.New("netsec: truncated label")
		}
		labels = append(labels, strings.ToLower(string(wire[pos:pos+length])))
		pos += length
		if len(labels) > maxLabelCount {
			return Name{}, errors.New("netsec: name has too many labels")
		}
	}
	return Name{Labels: labels}, nil
}

// IsRoot reports whether the name is the root (zero labels).
func (n Name) IsRoot() bool { return len(n.Labels) == 0 }

// LabelCount returns the number of labels excluding the root label.
func (n Name) LabelCount() int { return len(n.Labels) }

// IsWildcard reports whether the name begins with the literal "*" label.
func (n Name) IsWildcard() bool {
	return len(n.Labels) > 0 && n.Labels[0] == "*"
}

// EffectiveLabelCount returns the label count used for RRSIG "labels"
// comparison. Per the verification rule set, the root label is not counted and
// a leading literal wildcard ("*") label is not counted either.
func (n Name) EffectiveLabelCount() int {
	if n.IsWildcard() {
		return len(n.Labels) - 1
	}
	return len(n.Labels)
}

// Parent returns the name with its left-most label removed. The parent of the
// root is the root.
func (n Name) Parent() Name {
	if len(n.Labels) == 0 {
		return Name{}
	}
	return Name{Labels: append([]string(nil), n.Labels[1:]...)}
}

// Equal reports whether two names are identical label by label.
func (n Name) Equal(o Name) bool {
	if len(n.Labels) != len(o.Labels) {
		return false
	}
	for i := range n.Labels {
		if n.Labels[i] != o.Labels[i] {
			return false
		}
	}
	return true
}

// IsSubDomain reports whether n is equal to parent or enclosed by parent.
func (n Name) IsSubDomain(parent Name) bool {
	if len(n.Labels) < len(parent.Labels) {
		return false
	}
	offset := len(n.Labels) - len(parent.Labels)
	for i := range parent.Labels {
		if n.Labels[offset+i] != parent.Labels[i] {
			return false
		}
	}
	return true
}

// WireEncode renders the name as an uncompressed wire-format octet string,
// including the terminating zero-length root label.
func (n Name) WireEncode() ([]byte, error) {
	out := make([]byte, 0, n.wireLen())
	for _, label := range n.Labels {
		if len(label) == 0 || len(label) > 63 {
			return nil, errors.New("netsec: invalid label length")
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	out = append(out, 0)
	if len(out) > maxNameOctets {
		return nil, errors.New("netsec: wire name exceeds 255 octets")
	}
	return out, nil
}

func (n Name) wireLen() int {
	total := 1
	for _, label := range n.Labels {
		total += 1 + len(label)
	}
	return total
}

// String renders the canonical presentation form with a trailing dot.
func (n Name) String() string {
	if len(n.Labels) == 0 {
		return "."
	}
	var b strings.Builder
	for _, label := range n.Labels {
		for i := 0; i < len(label); i++ {
			c := label[i]
			if c == '.' || c == '\\' || c < 0x21 || c > 0x7E {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
		b.WriteByte('.')
	}
	return b.String()
}

// ClosestEncloser returns the closest enclosing name of n whose label count
// equals effectiveCount. It is used to recover the closest encloser that a
// wildcard was expanded from. The root is returned when effectiveCount == 0.
func (n Name) ClosestEncloser(effectiveCount int) (Name, error) {
	if effectiveCount < 0 || effectiveCount > len(n.Labels) {
		return Name{}, errors.New("netsec: effective label count out of range")
	}
	if effectiveCount == len(n.Labels) {
		return Name{Labels: append([]string(nil), n.Labels...)}, nil
	}
	return Name{Labels: append([]string(nil), n.Labels[len(n.Labels)-effectiveCount:]...)}, nil
}

// PrependWildcard returns "*." + n in label form, i.e. a wildcard name whose
// next-closer side is n.
func (n Name) PrependWildcard() Name {
	labels := make([]string, 0, len(n.Labels)+1)
	labels = append(labels, "*")
	labels = append(labels, n.Labels...)
	return Name{Labels: labels}
}
