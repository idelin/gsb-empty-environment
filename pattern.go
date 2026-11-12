package netsec

import (
	"bytes"
	"fmt"
	"math/bits"
)

// A Pattern describes a set of values of the same width as len(Value).
// One bits in Mask are fixed to the corresponding bits in Value; zero bits
// are don't-care. Consequently, a zero mask describes the whole field.
type Pattern struct {
	Value []byte
	Mask  []byte
}

// MatchKind is the P4Runtime match kind of a field.
type MatchKind uint8

const (
	// MatchExact matches one concrete value.
	MatchExact MatchKind = iota
	// MatchLPM is a longest-prefix match. Mask must be a contiguous
	// most-significant-bit prefix.
	MatchLPM
	// MatchTernary is a ternary match with an arbitrary mask.
	MatchTernary
	// MatchAny matches the complete field. Value supplies the field width.
	MatchAny
)

func (k MatchKind) String() string {
	switch k {
	case MatchExact:
		return "exact"
	case MatchLPM:
		return "lpm"
	case MatchTernary:
		return "ternary"
	case MatchAny:
		return "any"
	default:
		return fmt.Sprintf("MatchKind(%d)", uint8(k))
	}
}

// FieldMatch is one match constraint of a table entry.
//
// For MatchExact, Mask may be nil. For MatchLPM and MatchTernary, Mask is
// required and must have the same width as Value. For MatchAny, Value
// supplies the width and Mask may be nil.
type FieldMatch struct {
	Field string
	Kind  MatchKind
	Value []byte
	Mask  []byte
}

// NewExactMatch creates an exact match.
func NewExactMatch(field string, value []byte) FieldMatch {
	return FieldMatch{Field: field, Kind: MatchExact, Value: cloneBytes(value)}
}

// NewPrefixMatch creates an LPM match for a big-endian field.
func NewPrefixMatch(field string, value []byte, prefixBits int) (FieldMatch, error) {
	if len(value) == 0 {
		return FieldMatch{}, fmt.Errorf("%w: empty match value", ErrInvalidMatch)
	}
	if prefixBits < 0 || prefixBits > len(value)*8 {
		return FieldMatch{}, fmt.Errorf(
			"%w: prefix length %d outside 0..%d", ErrInvalidMatch, prefixBits, len(value)*8,
		)
	}
	mask := prefixMask(len(value), prefixBits)
	value = cloneBytes(value)
	andInto(value, mask)
	return FieldMatch{Field: field, Kind: MatchLPM, Value: value, Mask: mask}, nil
}

// NewTernaryMatch creates a ternary match. The match is validated when it is
// used by a Controller.
func NewTernaryMatch(field string, value, mask []byte) FieldMatch {
	return FieldMatch{
		Field: field,
		Kind:  MatchTernary,
		Value: cloneBytes(value),
		Mask:  cloneBytes(mask),
	}
}

// NewAnyMatch creates a wildcard match of widthBytes bytes.
func NewAnyMatch(field string, widthBytes int) FieldMatch {
	value := make([]byte, widthBytes)
	mask := make([]byte, widthBytes)
	return FieldMatch{Field: field, Kind: MatchAny, Value: value, Mask: mask}
}

// NewExactPattern creates an authorization pattern containing one value.
func NewExactPattern(value []byte) Pattern {
	mask := make([]byte, len(value))
	for i := range mask {
		mask[i] = 0xff
	}
	return Pattern{Value: cloneBytes(value), Mask: mask}
}

// NewPrefixPattern creates an authorization pattern for a big-endian prefix.
func NewPrefixPattern(value []byte, prefixBits int) (Pattern, error) {
	if len(value) == 0 {
		return Pattern{}, fmt.Errorf("%w: empty pattern value", ErrInvalidTenant)
	}
	if prefixBits < 0 || prefixBits > len(value)*8 {
		return Pattern{}, fmt.Errorf(
			"%w: prefix length %d outside 0..%d", ErrInvalidTenant, prefixBits, len(value)*8,
		)
	}
	mask := prefixMask(len(value), prefixBits)
	value = cloneBytes(value)
	andInto(value, mask)
	return Pattern{Value: value, Mask: mask}, nil
}

// NewTernaryPattern creates an authorization pattern with an arbitrary mask.
// The pattern is validated and canonicalized when the Controller is built.
func NewTernaryPattern(value, mask []byte) Pattern {
	return Pattern{Value: cloneBytes(value), Mask: cloneBytes(mask)}
}

// NewWildcardPattern creates a pattern covering a whole field.
func NewWildcardPattern(widthBytes int) Pattern {
	return Pattern{Value: make([]byte, widthBytes), Mask: make([]byte, widthBytes)}
}

const maxPatternBytes = 32

// bitCube is the canonical internal representation of a Pattern. A one bit
// in mask is fixed; value bits outside the mask are always zero.
type bitCube struct {
	value []byte
	mask  []byte
}

func newBitCube(value, mask []byte) (bitCube, error) {
	if len(value) == 0 {
		return bitCube{}, fmt.Errorf("%w: empty value", ErrInvalidMatch)
	}
	if len(value) != len(mask) {
		return bitCube{}, fmt.Errorf(
			"%w: value width %d does not match mask width %d",
			ErrInvalidMatch, len(value), len(mask),
		)
	}
	if len(value) > maxPatternBytes {
		return bitCube{}, fmt.Errorf(
			"%w: field width %d exceeds %d bytes",
			ErrInvalidMatch, len(value), maxPatternBytes,
		)
	}
	c := bitCube{value: cloneBytes(value), mask: cloneBytes(mask)}
	andInto(c.value, c.mask)
	return c, nil
}

func patternCube(p Pattern) (bitCube, error) {
	return newBitCube(p.Value, p.Mask)
}

func (c bitCube) clone() bitCube {
	return bitCube{value: cloneBytes(c.value), mask: cloneBytes(c.mask)}
}

// cubeSubset reports whether every value selected by c is also selected by a.
func cubeSubset(c, a bitCube) bool {
	if len(c.value) != len(a.value) {
		return false
	}
	for i := range c.mask {
		// Every bit fixed by a must also be fixed by c, and the two
		// cubes must agree on it.
		if a.mask[i]&^c.mask[i] != 0 {
			return false
		}
		if (c.value[i]^a.value[i])&a.mask[i] != 0 {
			return false
		}
	}
	return true
}

func cubeIntersect(a, b bitCube) (bitCube, bool) {
	if len(a.value) != len(b.value) {
		return bitCube{}, false
	}
	common := make([]byte, len(a.mask))
	for i := range common {
		common[i] = a.mask[i] & b.mask[i]
		if (a.value[i]^b.value[i])&common[i] != 0 {
			return bitCube{}, false
		}
	}
	mask := make([]byte, len(a.mask))
	value := make([]byte, len(a.value))
	for i := range mask {
		mask[i] = a.mask[i] | b.mask[i]
		value[i] = (a.value[i] & a.mask[i]) | (b.value[i] & b.mask[i])
	}
	return bitCube{value: value, mask: mask}, true
}

// cubeSubtract returns a disjoint cube decomposition of c minus a.
func cubeSubtract(c, a bitCube) []bitCube {
	if _, ok := cubeIntersect(c, a); !ok {
		return []bitCube{c}
	}
	if cubeSubset(c, a) {
		return nil
	}

	var out []bitCube
	for bit := 0; bit < len(c.mask)*8; bit++ {
		// The value is outside a only on bits that a fixes and c leaves
		// free. For each such bit, produce the branch where they differ.
		if !bitAt(a.mask, bit) || bitAt(c.mask, bit) {
			continue
		}
		d := c.clone()
		setBit(d.mask, bit, true)
		setBit(d.value, bit, !bitAt(a.value, bit))
		out = append(out, d)
	}
	return out
}

// cubeCovered reports whether c is covered by the union of allowed. Splitting
// stops early when the union covers the cube. The remainder limit makes a
// pathological authorization policy fail closed rather than consume unbounded
// memory.
func cubeCovered(c bitCube, allowed []bitCube) bool {
	const maxRemainders = 1 << 18

	remainders := []bitCube{c}
	for _, a := range allowed {
		next := make([]bitCube, 0, len(remainders))
		seen := make(map[string]struct{}, len(remainders)*2)
		for _, r := range remainders {
			for _, piece := range cubeSubtract(r, a) {
				k := string(piece.mask) + "\x00" + string(piece.value)
				if _, ok := seen[k]; ok {
					continue
				}
				seen[k] = struct{}{}
				next = append(next, piece)
			}
		}
		if len(next) > maxRemainders {
			return false
		}
		if len(next) == 0 {
			return true
		}
		remainders = next
	}
	return len(remainders) == 0
}

func prefixMask(widthBytes, prefixBits int) []byte {
	mask := make([]byte, widthBytes)
	full := prefixBits / 8
	rem := prefixBits % 8
	for i := 0; i < full; i++ {
		mask[i] = 0xff
	}
	if rem != 0 && full < widthBytes {
		mask[full] = byte(0xff << (8 - rem))
	}
	return mask
}

// prefixLength returns the number of leading one bits if mask is a valid
// contiguous prefix mask.
func prefixLength(mask []byte) (int, bool) {
	n := 0
	zeroSeen := false
	for _, b := range mask {
		for bit := 7; bit >= 0; bit-- {
			if b&(1<<bit) != 0 {
				if zeroSeen {
					return 0, false
				}
				n++
			} else {
				zeroSeen = true
			}
		}
	}
	return n, true
}

func bitAt(b []byte, bit int) bool {
	return b[bit/8]&(1<<(7-uint(bit%8))) != 0
}

func setBit(b []byte, bit int, value bool) {
	mask := byte(1 << (7 - uint(bit%8)))
	if value {
		b[bit/8] |= mask
	} else {
		b[bit/8] &^= mask
	}
}

func andInto(dst, mask []byte) {
	for i := range dst {
		dst[i] &= mask[i]
	}
}

func allOnes(b []byte) bool {
	for _, x := range b {
		if x != 0xff {
			return false
		}
	}
	return true
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return bytes.Clone(b)
}

func onesCount(b []byte) int {
	n := 0
	for _, x := range b {
		n += bits.OnesCount8(x)
	}
	return n
}
