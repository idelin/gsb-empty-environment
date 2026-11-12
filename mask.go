package netsec

import (
	"fmt"
	"strings"
)

// MaxWidth is the largest value width accepted by the analysis.  Values are
// represented as uint64 masks; larger widths can be analyzed by splitting them
// into words at the host boundary.
const MaxWidth = 64

// Mask is a bit mask over a value of at most MaxWidth bits.
type Mask uint64

// Bit returns a mask containing one bit.
func Bit(position int) Mask {
	if position < 0 || position >= MaxWidth {
		return 0
	}
	return Mask(1) << uint(position)
}

// Bits returns a mask containing the listed bit positions.  Positions outside
// [0, MaxWidth) are ignored.
func Bits(positions ...int) Mask {
	var m Mask
	for _, position := range positions {
		m |= Bit(position)
	}
	return m
}

// Has reports whether position is present in the mask.
func (m Mask) Has(position int) bool {
	return position >= 0 && position < MaxWidth && m&Bit(position) != 0
}

// With returns the union of m and the supplied masks.
func (m Mask) With(others ...Mask) Mask {
	for _, other := range others {
		m |= other
	}
	return m
}

// Without returns the bits of m that are not in other.
func (m Mask) Without(other Mask) Mask {
	return m &^ other
}

// Positions returns the set positions in ascending order.
func (m Mask) Positions() []int {
	var positions []int
	for position := 0; position < MaxWidth; position++ {
		if m.Has(position) {
			positions = append(positions, position)
		}
	}
	return positions
}

// Count returns the number of set bits.
func (m Mask) Count() int {
	count := 0
	for x := uint64(m); x != 0; x &= x - 1 {
		count++
	}
	return count
}

// String renders the mask as hexadecimal.
func (m Mask) String() string {
	return fmt.Sprintf("%#x", uint64(m))
}

func widthMask(width int) Mask {
	if width >= MaxWidth {
		return ^Mask(0)
	}
	return Mask(1)<<uint(width) - 1
}

func checkWidth(what string, width int) error {
	if width <= 0 || width > MaxWidth {
		return fmt.Errorf("%s width must be in [1,%d], got %d", what, MaxWidth, width)
	}
	return nil
}

func maskString(m Mask) string {
	if m == 0 {
		return "0x0"
	}
	var parts []string
	for _, position := range m.Positions() {
		parts = append(parts, fmt.Sprintf("%d", position))
	}
	return fmt.Sprintf("%#x(bits %s)", uint64(m), strings.Join(parts, ","))
}

func maskAt(masks []Mask, index int) Mask {
	if index < 0 || index >= len(masks) {
		return 0
	}
	return masks[index]
}
