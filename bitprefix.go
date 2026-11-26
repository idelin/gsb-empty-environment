package kt

import (
	"errors"
	"sort"
)

// MaxLabelBits is the fixed maximum length (in bits) of a key-transparency
// label. All labels are fixed-length bit strings of at most 256 bits.
const MaxLabelBits = 256

// bytesForBits reports how many bytes are needed to hold n bits.
func bytesForBits(n int) int { return (n + 7) / 8 }

// BitPrefix is a fixed-length bit string with length in [0, MaxLabelBits].
// The zero value is the empty prefix (length 0), which matches every label.
//
// Bits are packed big-endian inside Bytes: the first bit of the prefix is the
// most significant bit of Bytes[0]. Unused low bits of the final byte are
// always zero. BitPrefix values are immutable; mutating helpers return new
// values.
type BitPrefix struct {
	bits  int
	bytes []byte
}

// NewBitPrefix constructs a BitPrefix from packed big-endian bytes. nBits is
// the number of significant bits; trailing unused bits must be zero.
func NewBitPrefix(packed []byte, nBits int) (BitPrefix, error) {
	if nBits < 0 || nBits > MaxLabelBits {
		return BitPrefix{}, ErrLabelTooLong
	}
	if len(packed) != bytesForBits(nBits) {
		return BitPrefix{}, ErrPrefixEncoding
	}
	cp := append([]byte(nil), packed...)
	if nBits%8 != 0 && len(cp) > 0 {
		if cp[len(cp)-1]&(byte(0xFF)>>(nBits%8)) != 0 {
			return BitPrefix{}, ErrPrefixEncoding
		}
	}
	return BitPrefix{bits: nBits, bytes: cp}, nil
}

// NewBitPrefixFromBytes treats the full byte slice as a label of 8*len(b)
// bits (b must be no longer than 32 bytes).
func NewBitPrefixFromBytes(b []byte) (BitPrefix, error) {
	return NewBitPrefix(b, len(b)*8)
}

// EmptyPrefix returns the zero-length prefix.
func EmptyPrefix() BitPrefix { return BitPrefix{bits: 0, bytes: []byte{}} }

// BitAt returns the bit at position i (0-based from the most significant bit).
func (p BitPrefix) BitAt(i int) int {
	if i < 0 || i >= p.bits {
		panic("kt: bit index out of range")
	}
	return int((p.bytes[i>>3] >> (7 - uint(i&7))) & 1)
}

// Len returns the prefix length in bits.
func (p BitPrefix) Len() int { return p.bits }

// Bytes returns a copy of the packed big-endian bytes.
func (p BitPrefix) Bytes() []byte { return append([]byte(nil), p.bytes...) }

// Equal reports whether two prefixes are bit-for-bit identical.
func (p BitPrefix) Equal(o BitPrefix) bool {
	if p.bits != o.bits {
		return false
	}
	for i := range p.bytes {
		if p.bytes[i] != o.bytes[i] {
			return false
		}
	}
	return true
}

// IsPrefixOf reports whether p is a (non-strict) bit prefix of o.
func (p BitPrefix) IsPrefixOf(o BitPrefix) bool {
	if p.bits > o.bits {
		return false
	}
	full := p.bits / 8
	for i := 0; i < full; i++ {
		if p.bytes[i] != o.bytes[i] {
			return false
		}
	}
	for i := full * 8; i < p.bits; i++ {
		if p.BitAt(i) != o.BitAt(i) {
			return false
		}
	}
	return true
}

// HasPrefix reports whether o is a (non-strict) prefix of p.
func (p BitPrefix) HasPrefix(o BitPrefix) bool { return o.IsPrefixOf(p) }

// AppendBit returns a new prefix one bit longer. bit must be 0 or 1.
func (p BitPrefix) AppendBit(bit int) (BitPrefix, error) {
	if bit != 0 && bit != 1 {
		return BitPrefix{}, ErrPrefixEncoding
	}
	if p.bits >= MaxLabelBits {
		return BitPrefix{}, ErrLabelTooLong
	}
	out := BitPrefix{bits: p.bits + 1, bytes: append([]byte(nil), p.bytes...)}
	if p.bits%8 == 0 {
		out.bytes = append(out.bytes, byte(bit<<7))
	} else {
		out.bytes[len(out.bytes)-1] |= byte(bit << (7 - uint(p.bits%8)))
	}
	return out, nil
}

// Parent returns the prefix with the final bit removed. The empty prefix has
// no parent.
func (p BitPrefix) Parent() (BitPrefix, error) {
	if p.bits == 0 {
		return BitPrefix{}, ErrEmptyPrefixParent
	}
	nb := p.bits - 1
	out := BitPrefix{bits: nb, bytes: append([]byte(nil), p.bytes[:bytesForBits(nb)]...)}
	if nb%8 != 0 {
		out.bytes[len(out.bytes)-1] &= byte(0xFF) << (8 - uint(nb%8))
	}
	return out, nil
}

// Sibling returns the sibling prefix: the parent prefix extended with the
// opposite of this prefix's final bit.
func (p BitPrefix) Sibling() (BitPrefix, error) {
	if p.bits == 0 {
		return BitPrefix{}, ErrEmptyPrefixParent
	}
	parent, err := p.Parent()
	if err != nil {
		return BitPrefix{}, err
	}
	return parent.AppendBit(1 - p.BitAt(p.bits-1))
}

// CommonPrefixLen returns the length of the longest common bit prefix.
func (p BitPrefix) CommonPrefixLen(o BitPrefix) int {
	n := p.bits
	if o.bits < n {
		n = o.bits
	}
	for i := 0; i < n; i++ {
		if p.BitAt(i) != o.BitAt(i) {
			return i
		}
	}
	return n
}

// Compare orders prefixes lexicographically by (bits, length), giving a total
// order useful for deterministic sorting.
func (p BitPrefix) Compare(o BitPrefix) int {
	n := p.bits
	if o.bits < n {
		n = o.bits
	}
	for i := 0; i < n; i++ {
		if a, b := p.BitAt(i), o.BitAt(i); a != b {
			return a - b
		}
	}
	switch {
	case p.bits < o.bits:
		return -1
	case p.bits > o.bits:
		return 1
	default:
		return 0
	}
}

// String renders the prefix as a 0/1 bit string (the empty prefix is "").
func (p BitPrefix) String() string {
	out := make([]byte, p.bits)
	for i := 0; i < p.bits; i++ {
		out[i] = byte('0' + p.BitAt(i))
	}
	return string(out)
}

// MarshalBinary / UnmarshalBinary use a deterministic on-wire encoding:
//
//	uint16 bit length || packed big-endian bytes.
func (p BitPrefix) MarshalBinary() ([]byte, error) {
	out := make([]byte, 2+len(p.bytes))
	out[0] = byte(p.bits >> 8)
	out[1] = byte(p.bits)
	copy(out[2:], p.bytes)
	return out, nil
}

// UnmarshalBinary parses the encoding produced by MarshalBinary.
func (p *BitPrefix) UnmarshalBinary(data []byte) error {
	if len(data) < 2 {
		return ErrPrefixEncoding
	}
	n := int(data[0])<<8 | int(data[1])
	if n > MaxLabelBits || len(data) != 2+bytesForBits(n) {
		return ErrPrefixEncoding
	}
	got, err := NewBitPrefix(data[2:], n)
	if err != nil {
		return err
	}
	*p = got
	return nil
}

// SortPrefixes sorts a slice in-place in deterministic total order.
func SortPrefixes(ps []BitPrefix) {
	sort.Slice(ps, func(i, j int) bool { return ps[i].Compare(ps[j]) < 0 })
}

// Disjoint reports whether all prefixes in the set are pairwise incomparable
// (i.e. none is a prefix of another).
func Disjoint(ps []BitPrefix) bool {
	for i := range ps {
		for j := i + 1; j < len(ps); j++ {
			if ps[i].IsPrefixOf(ps[j]) || ps[j].IsPrefixOf(ps[i]) {
				return false
			}
		}
	}
	return true
}

var (
	// ErrLabelTooLong indicates a label exceeds 256 bits.
	ErrLabelTooLong = errors.New("kt: label exceeds 256 bits")
	// ErrPrefixEncoding indicates malformed packed prefix bytes.
	ErrPrefixEncoding = errors.New("kt: malformed bit prefix encoding")
	// ErrEmptyPrefixParent indicates Parent/Sibling on the empty prefix.
	ErrEmptyPrefixParent = errors.New("kt: empty prefix has no parent/sibling")
)
