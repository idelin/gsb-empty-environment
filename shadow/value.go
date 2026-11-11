package shadow

// Width is the bit width of a value or instruction.
type Width int

const (
	// Width1 is used for the internal carry bit.
	Width1  Width = 1
	Width8  Width = 8
	Width16 Width = 16
	Width32 Width = 32
)

// Value stores a concrete bit vector and the sources of every result bit.
type Value struct {
	width Width
	bits  []bitTag
}

type bitTag struct {
	value    uint8
	id       uint64
	constant bool
	sources  SourceSet
}

// Width returns the value width.
func (value Value) Width() Width {
	return value.width
}

// Bit returns the concrete value of one bit.
func (value Value) Bit(index int) (uint8, error) {
	if index < 0 || index >= len(value.bits) {
		return 0, &BitIndexError{Index: index, Width: value.width}
	}
	return value.bits[index].value, nil
}

// Uint64 returns the concrete little-endian bit vector as an unsigned integer.
func (value Value) Uint64() uint64 {
	var result uint64
	for index := len(value.bits) - 1; index >= 0; index-- {
		result = (result << 1) | uint64(value.bits[index].value)
	}
	return result
}

// Sources returns a copy of the source set for one result bit.
func (value Value) Sources(index int) (SourceSet, error) {
	if index < 0 || index >= len(value.bits) {
		return nil, &BitIndexError{Index: index, Width: value.width}
	}
	return cloneSources(value.bits[index].sources), nil
}

// BitSource pairs a result bit with its source set.
type BitSource struct {
	Bit     uint8
	Sources SourceSet
}

// BitSources returns all result bits and their source sets.
func (value Value) BitSources() []BitSource {
	result := make([]BitSource, len(value.bits))
	for index, bit := range value.bits {
		result[index] = BitSource{
			Bit:     bit.value,
			Sources: cloneSources(bit.sources),
		}
	}
	return result
}

func validWidth(width Width) bool {
	return width == Width1 || width == Width8 || width == Width16 || width == Width32
}

func checkWidth(width Width) error {
	if !validWidth(width) {
		return &WidthError{Width: width}
	}
	return nil
}

func sameWidth(a, b Value) error {
	if err := a.validate(); err != nil {
		return err
	}
	if err := b.validate(); err != nil {
		return err
	}
	if a.width != b.width {
		return &WidthMismatchError{Left: a.width, Right: b.width}
	}
	return nil
}

func (value Value) validate() error {
	if err := checkWidth(value.width); err != nil {
		return err
	}
	if len(value.bits) != int(value.width) {
		return &InvalidValueError{Width: value.width, Bits: len(value.bits)}
	}
	return nil
}

// InvalidValueError indicates a Value was not created by the shadow machine.
type InvalidValueError struct {
	Width Width
	Bits  int
}

func (err *InvalidValueError) Error() string {
	return "shadow: invalid value: width " + itoa(int(err.Width)) + " has " + itoa(err.Bits) + " bits"
}

// WidthError indicates an unsupported value width.
type WidthError struct {
	Width Width
}

func (err *WidthError) Error() string {
	return "shadow: unsupported width " + itoa(int(err.Width))
}

// WidthMismatchError indicates that two operands have different widths.
type WidthMismatchError struct {
	Left  Width
	Right Width
}

// BitIndexError indicates a bit index outside the value width.
type BitIndexError struct {
	Index int
	Width Width
}

func (err *BitIndexError) Error() string {
	return "shadow: bit index " + itoa(err.Index) + " outside width " + itoa(int(err.Width))
}

func (err *WidthMismatchError) Error() string {
	return "shadow: width mismatch: " + itoa(int(err.Left)) + " and " + itoa(int(err.Right))
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
