package netsec

import (
	"bytes"
	"fmt"
	"strings"
)

// Name is an absolute DNS name in canonical, uncompressed wire form. All ASCII
// letters are lower case, as required by RFC 4034 section 6.2. The zero value
// is invalid; use ParseName to construct a Name.
type Name struct {
	wire string
}

// ParseName parses a presentation-format domain name into canonical wire form.
// A trailing dot is accepted but not required; every resulting Name is
// absolute. Standard \DDD and \X escapes are supported.
func ParseName(s string) (Name, error) {
	if s == "" {
		return Name{}, fmt.Errorf("%w: empty domain name", ErrMalformed)
	}

	var labels [][]byte
	cur := make([]byte, 0, 63)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return Name{}, fmt.Errorf("%w: trailing backslash", ErrMalformed)
			}
			if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
				v := int(s[i+1]-'0')*100 + int(s[i+2]-'0')*10 + int(s[i+3]-'0')
				if v > 255 {
					return Name{}, fmt.Errorf("%w: escape out of range", ErrMalformed)
				}
				cur = append(cur, byte(v))
				i += 3
			} else {
				cur = append(cur, s[i+1])
				i++
			}
		case '.':
			if len(cur) == 0 {
				if i == len(s)-1 {
					continue
				}
				return Name{}, fmt.Errorf("%w: empty label", ErrMalformed)
			}
			if len(cur) > 63 {
				return Name{}, fmt.Errorf("%w: label longer than 63 octets", ErrMalformed)
			}
			labels = append(labels, canonicalLabel(cur))
			cur = cur[:0]
		default:
			cur = append(cur, s[i])
		}
	}
	if len(cur) > 0 {
		if len(cur) > 63 {
			return Name{}, fmt.Errorf("%w: label longer than 63 octets", ErrMalformed)
		}
		labels = append(labels, canonicalLabel(cur))
	}

	var b strings.Builder
	for _, label := range labels {
		if len(label) == 0 {
			return Name{}, fmt.Errorf("%w: empty label", ErrMalformed)
		}
		b.WriteByte(byte(len(label)))
		b.Write(label)
	}
	b.WriteByte(0)
	if b.Len() > 255 {
		return Name{}, fmt.Errorf("%w: domain name longer than 255 octets", ErrMalformed)
	}
	return Name{wire: b.String()}, nil
}

// MustParseName is ParseName for trusted literals. It panics on invalid input.
func MustParseName(s string) Name {
	n, err := ParseName(s)
	if err != nil {
		panic(err)
	}
	return n
}

func canonicalLabel(in []byte) []byte {
	out := make([]byte, len(in))
	for i, c := range in {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// IsZero reports whether n is the zero (invalid) Name.
func (n Name) IsZero() bool { return n.wire == "" }

// Wire returns a copy of the canonical wire representation.
func (n Name) Wire() []byte {
	if n.IsZero() {
		return nil
	}
	return []byte(n.wire)
}

// String returns the canonical presentation form, always ending in a dot.
func (n Name) String() string {
	if n.IsZero() {
		return "<invalid>"
	}
	if n.wire == "\x00" {
		return "."
	}
	var b strings.Builder
	for pos := 0; pos < len(n.wire)-1; {
		l := int(n.wire[pos])
		if l == 0 || pos+1+l > len(n.wire)-1 {
			return "<invalid>"
		}
		label := []byte(n.wire[pos+1 : pos+1+l])
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		for _, c := range label {
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
		pos += 1 + l
	}
	b.WriteByte('.')
	return b.String()
}

// Equal reports canonical wire equality.
func (n Name) Equal(o Name) bool { return n.wire == o.wire }

// LabelCount returns the number of labels; the root name has zero.
func (n Name) LabelCount() int {
	if n.IsZero() {
		return 0
	}
	count := 0
	for pos := 0; pos < len(n.wire); {
		l := int(n.wire[pos])
		if l == 0 {
			break
		}
		count++
		pos += 1 + l
	}
	return count
}

func (n Name) labels() [][]byte {
	if n.IsZero() {
		return nil
	}
	var out [][]byte
	for pos := 0; pos < len(n.wire); {
		l := int(n.wire[pos])
		if l == 0 {
			break
		}
		out = append(out, []byte(n.wire[pos+1:pos+1+l]))
		pos += 1 + l
	}
	return out
}

func nameFromLabels(labels [][]byte) Name {
	var b strings.Builder
	for _, label := range labels {
		b.WriteByte(byte(len(label)))
		b.Write(label)
	}
	b.WriteByte(0)
	return Name{wire: b.String()}
}

// Parent returns the name with its leftmost label removed. For the root name,
// ok is false.
func (n Name) Parent() (Name, bool) {
	if n.IsZero() || n.wire == "\x00" {
		return Name{}, false
	}
	first := int(n.wire[0])
	return Name{wire: n.wire[1+first:]}, true
}

// AncestorAtDepth returns the suffix of n with the requested number of
// labels. Depth zero is the root name.
func (n Name) AncestorAtDepth(depth int) (Name, bool) {
	if n.IsZero() || depth < 0 {
		return Name{}, false
	}
	labels := n.labels()
	if depth > len(labels) {
		return Name{}, false
	}
	return nameFromLabels(labels[len(labels)-depth:]), true
}

// AppendLabel prepends one raw presentation label. The label must not contain
// a dot and is canonicalized to lower case.
func (n Name) AppendLabel(label string) (Name, error) {
	if n.IsZero() {
		return Name{}, fmt.Errorf("%w: invalid base name", ErrMalformed)
	}
	if label == "" || len(label) > 63 {
		return Name{}, fmt.Errorf("%w: bad label length", ErrMalformed)
	}
	raw := canonicalLabel([]byte(label))
	for _, c := range raw {
		if c == '.' {
			return Name{}, fmt.Errorf("%w: dot in single label", ErrMalformed)
		}
	}
	if 1+len(raw)+len(n.wire) > 255 {
		return Name{}, fmt.Errorf("%w: domain name longer than 255 octets", ErrMalformed)
	}
	return Name{wire: string([]byte{byte(len(raw))}) + string(raw) + n.wire}, nil
}

// IsSubdomainOf reports whether n equals or is below apex.
func (n Name) IsSubdomainOf(apex Name) bool {
	if n.IsZero() || apex.IsZero() {
		return false
	}
	na, aa := n.labels(), apex.labels()
	if len(na) < len(aa) {
		return false
	}
	offset := len(na) - len(aa)
	for i := range aa {
		if !bytes.Equal(na[offset+i], aa[i]) {
			return false
		}
	}
	return true
}

// IsProperSubdomainOf reports whether n is strictly below apex.
func (n Name) IsProperSubdomainOf(apex Name) bool {
	return n.IsSubdomainOf(apex) && !n.Equal(apex)
}

// CompareNames implements the DNSSEC canonical name order from RFC 4034
// section 6.1: compare labels from right to left, with the shorter name first
// when one is a prefix of the other.
func CompareNames(a, b Name) int {
	la, lb := a.labels(), b.labels()
	i, j := len(la)-1, len(lb)-1
	for i >= 0 && j >= 0 {
		if c := bytes.Compare(la[i], lb[j]); c != 0 {
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

// NSECCoveredBy reports whether target lies strictly inside the NSEC interval
// owner -> next. Equality is never coverage. The interval wraps at the zone
// apex in the usual manner.
func NSECCoveredBy(owner, next, target Name) bool {
	if owner.IsZero() || next.IsZero() || target.IsZero() {
		return false
	}
	if owner.Equal(target) || next.Equal(target) {
		return false
	}
	oc := CompareNames(owner, next)
	tcO := CompareNames(target, owner)
	tcN := CompareNames(target, next)
	if oc < 0 {
		return tcO > 0 && tcN < 0
	}
	if oc > 0 {
		return tcO > 0 || tcN < 0
	}
	return false
}

// WildcardName returns "*." followed by the supplied name.
func WildcardName(apex Name) (Name, error) {
	return apex.AppendLabel("*")
}
