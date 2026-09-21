package floatconv

// RoundingMode selects how an inexact value is rounded to the destination
// format.
type RoundingMode int

const (
	// RoundNearestEven rounds to the nearest representable value; ties go
	// to the value whose least significant mantissa bit is zero.
	RoundNearestEven RoundingMode = iota
	// RoundTowardZero truncates toward zero.
	RoundTowardZero
	// RoundTowardPositive rounds toward positive infinity.
	RoundTowardPositive
	// RoundTowardNegative rounds toward negative infinity.
	RoundTowardNegative
)

func (m RoundingMode) String() string {
	switch m {
	case RoundNearestEven:
		return "nearestEven"
	case RoundTowardZero:
		return "towardZero"
	case RoundTowardPositive:
		return "towardPositive"
	case RoundTowardNegative:
		return "towardNegative"
	}
	return "unknown"
}

// Flags is a bitmask of floating-point exception flags raised by a
// conversion.
type Flags uint8

const (
	// FlagInexact: the delivered result differs from the exact value.
	FlagInexact Flags = 1 << iota
	// FlagUnderflow: the result is tiny after rounding (subnormal or zero)
	// and inexact.
	FlagUnderflow
	// FlagOverflow: the magnitude of the rounded result, computed as if the
	// exponent range were unbounded, exceeds the destination's largest
	// finite exponent. Always accompanied by FlagInexact.
	FlagOverflow
	// FlagInvalid: an invalid operation, e.g. converting a signaling NaN,
	// or a NaN/infinity into a format that cannot represent it.
	FlagInvalid
)

func (f Flags) String() string {
	if f == 0 {
		return "none"
	}
	s := ""
	add := func(name string) {
		if s != "" {
			s += "|"
		}
		s += name
	}
	if f&FlagInexact != 0 {
		add("inexact")
	}
	if f&FlagUnderflow != 0 {
		add("underflow")
	}
	if f&FlagOverflow != 0 {
		add("overflow")
	}
	if f&FlagInvalid != 0 {
		add("invalid")
	}
	return s
}
