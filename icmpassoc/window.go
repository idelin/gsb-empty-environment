package icmpassoc

import "errors"

// MaxWindowLength is the largest unambiguous modular sequence window.
// Half of the 32-bit sequence space keeps every distance unambiguous.
const MaxWindowLength uint32 = 1<<31 - 1

// ErrInvalidWindow is returned when a SeqWindow has an unusable length.
var ErrInvalidWindow = errors.New("icmpassoc: sequence window length must be in [1, 2^31-1]")

// SeqWindow is a half-open TCP sequence-number range [Base, Base+Length)
// interpreted modulo 2^32. The zero value is not a valid window.
type SeqWindow struct {
	// Base is the first acceptable sequence number.
	Base uint32
	// Length is the number of acceptable sequence numbers starting at Base.
	Length uint32
}

// Valid reports whether the window has an unambiguous, non-empty length.
func (w SeqWindow) Valid() bool {
	return w.Length >= 1 && w.Length <= MaxWindowLength
}

// Contains reports whether seq falls inside the window using modular
// arithmetic, so windows that cross the 2^32 wrap point work correctly.
func (w SeqWindow) Contains(seq uint32) bool {
	if !w.Valid() {
		return false
	}
	// Unsigned subtraction yields the forward modular distance from Base.
	return seq-w.Base < w.Length
}
