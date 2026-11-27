package dnssec

import (
	"errors"
	"strings"
)

// Name is a DNS domain name represented as a sequence of labels in wire
// presentation order (leftmost label first). The root is the empty slice.
// Label contents preserve original case; all comparisons performed by this
// package are case-insensitive as required by DNS naming rules.
type Name struct {
	labels [][]byte
}

var (
	ErrNameTooLong   = errors.New("dnssec: domain name exceeds 255 octets")
	ErrLabelTooLong  = errors.New("dnssec: label exceeds 63 octets")
	ErrInvalidName   = errors.New("dnssec: invalid domain name")
	ErrPointerCycle  = errors.New("dnssec: compression pointer cycle")
	ErrTruncatedName = errors.New("dnssec: truncated domain name")
)

// ParseName parses a presentation-form name (for example "www.Example.COM.").
// A trailing dot is optional; names without it are treated as absolute.
func ParseName(s string) (Name, error) {
	if s == "" || s == "." {
		return Name{}, nil
	}
	parts := strings.Split(strings.TrimSuffix(s, "."), ".")
	labels := make([][]byte, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			return Name{}, ErrInvalidName
		}
		if len(p) > 63 {
			return Name{}, ErrLabelTooLong
		}
		labels = append(labels, []byte(p))
	}
	n := Name{labels: labels}
	if len(n.Wire()) > 255 {
		return Name{}, ErrNameTooLong
	}
	return n, nil
}

// MustParseName is like ParseName but panics on error. It is intended for
// test fixtures and compile-time constants.
func MustParseName(s string) Name {
	n, err := ParseName(s)
	if err != nil {
		panic(err)
	}
	return n
}

// NameFromLabels builds a Name from labels in presentation order.
func NameFromLabels(labels ...string) Name {
	out := make([][]byte, len(labels))
	for i, l := range labels {
		out[i] = []byte(l)
	}
	return Name{labels: out}
}

// Labels returns the labels in presentation order. The returned slice must
// not be modified.
func (n Name) Labels() [][]byte { return n.labels }

// LabelCount returns the number of labels; the root returns 0.
func (n Name) LabelCount() int { return len(n.labels) }

// IsRoot reports whether the name is the root (".").
func (n Name) IsRoot() bool { return len(n.labels) == 0 }

// Wire returns the uncompressed wire encoding including the terminating
// zero-length label for the root.
func (n Name) Wire() []byte {
	total := 1
	for _, l := range n.labels {
		total += 1 + len(l)
	}
	buf := make([]byte, 0, total)
	for _, l := range n.labels {
		buf = append(buf, byte(len(l)))
		buf = append(buf, l...)
	}
	buf = append(buf, 0)
	return buf
}

// LowerWire returns the wire encoding with every ASCII label lowercased, as
// required for canonical ordering and digest computations (RFC 4034 6.2).
func (n Name) LowerWire() []byte {
	w := n.Wire()
	for i := range w {
		if w[i] >= 'A' && w[i] <= 'Z' {
			w[i] += 'a' - 'A'
		}
	}
	return w
}

// String renders the name in presentation form with a trailing dot.
func (n Name) String() string {
	if len(n.labels) == 0 {
		return "."
	}
	var b strings.Builder
	for i, l := range n.labels {
		if i > 0 {
			b.WriteByte('.')
		}
		b.Write(l)
	}
	b.WriteByte('.')
	return b.String()
}

// Equal reports whether two names are identical modulo ASCII case.
func (n Name) Equal(o Name) bool {
	if len(n.labels) != len(o.labels) {
		return false
	}
	for i := range n.labels {
		if !equalFold(n.labels[i], o.labels[i]) {
			return false
		}
	}
	return true
}

// IsSubdomain reports whether n is equal to or a subdomain of parent.
func (n Name) IsSubdomain(parent Name) bool {
	if len(n.labels) < len(parent.labels) {
		return false
	}
	offset := len(n.labels) - len(parent.labels)
	for i := range parent.labels {
		if !equalFold(n.labels[offset+i], parent.labels[i]) {
			return false
		}
	}
	return true
}

// Parent returns the containing name. The parent of root is root.
func (n Name) Parent() Name {
	if len(n.labels) <= 1 {
		return Name{}
	}
	cp := make([][]byte, len(n.labels)-1)
	copy(cp, n.labels[1:])
	return Name{labels: cp}
}

// PrefixLabel returns a copy with label prepended as the new leftmost label.
func (n Name) PrefixLabel(label string) (Name, error) {
	if len(label) > 63 {
		return Name{}, ErrLabelTooLong
	}
	out := make([][]byte, 0, len(n.labels)+1)
	out = append(out, []byte(label))
	out = append(out, n.labels...)
	res := Name{labels: out}
	if len(res.Wire()) > 255 {
		return Name{}, ErrNameTooLong
	}
	return res, nil
}

// TrimToLabels returns a name consisting of the rightmost count labels.
func (n Name) TrimToLabels(count int) (Name, error) {
	if count < 0 || count > len(n.labels) {
		return Name{}, ErrInvalidName
	}
	if count == 0 {
		return Name{}, nil
	}
	out := make([][]byte, count)
	copy(out, n.labels[len(n.labels)-count:])
	return Name{labels: out}, nil
}

// CanonicalCompare implements the canonical name ordering of RFC 4034
// section 6.1: compare from the root (rightmost label) towards the left,
// label by label, case-insensitively, treating the root as the smallest
// label so that shorter names sort first within the same suffix.
// It returns -1, 0 or 1.
func CanonicalCompare(a, b Name) int {
	ai, bi := len(a.labels)-1, len(b.labels)-1
	for ai >= 0 && bi >= 0 {
		if c := compareFold(a.labels[ai], b.labels[bi]); c != 0 {
			return c
		}
		ai--
		bi--
	}
	// Both exhausted: equal. Otherwise the name that reached root first
	// (fewer labels) sorts earlier.
	switch {
	case ai < 0 && bi < 0:
		return 0
	case ai < 0:
		return -1
	default:
		return 1
	}
}

func equalFold(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if lowerByte(a[i]) != lowerByte(b[i]) {
			return false
		}
	}
	return true
}

func compareFold(a, b []byte) int {
	la, lb := len(a), len(b)
	n := la
	if lb < n {
		n = lb
	}
	for i := 0; i < n; i++ {
		x, y := lowerByte(a[i]), lowerByte(b[i])
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	switch {
	case la < lb:
		return -1
	case la > lb:
		return 1
	default:
		return 0
	}
}

func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// readName parses an (optionally compressed) wire name starting at off.
// It returns the name and the offset immediately following the encoded name
// in the local sequence (i.e. after a pointer, the offset past the pointer).
func readName(msg []byte, off int) (Name, int, error) {
	var labels [][]byte
	cursor := off
	jumped := false
	nextOff := 0
	seen := map[int]bool{}
	for {
		if cursor >= len(msg) {
			return Name{}, 0, ErrTruncatedName
		}
		l := int(msg[cursor])
		switch {
		case l == 0:
			cursor++
			if !jumped {
				nextOff = cursor
			}
			return Name{labels: labels}, nextOff, nil
		case l&0xC0 == 0xC0:
			if cursor+1 >= len(msg) {
				return Name{}, 0, ErrTruncatedName
			}
			ptr := (l&0x3F)<<8 | int(msg[cursor+1])
			if !jumped {
				nextOff = cursor + 2
			}
			if seen[ptr] || ptr >= off {
				return Name{}, 0, ErrPointerCycle
			}
			seen[ptr] = true
			cursor = ptr
			jumped = true
		case l&0xC0 != 0:
			return Name{}, 0, ErrInvalidName
		default:
			cursor++
			if cursor+l > len(msg) {
				return Name{}, 0, ErrTruncatedName
			}
			label := make([]byte, l)
			copy(label, msg[cursor:cursor+l])
			labels = append(labels, label)
			cursor += l
		}
	}
}
