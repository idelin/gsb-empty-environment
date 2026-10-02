package floatwire

import "math/bits"

// Mode selects the conversion contract between widths.
type Mode int

const (
	// ModeBitPreserving guarantees bit-level preservation of everything the
	// narrower value can carry:
	//   - the sign of zero is always kept;
	//   - infinities map to infinities of the same sign;
	//   - NaN payloads are propagated with the documented truncation policy
	//     (see below), and a NaN never becomes a non-NaN;
	//   - finite nonzero values are converted numerically (round to
	//     nearest, ties to even) exactly as in ModeNumeric.
	//
	// Widening is always lossless, so width A -> width B -> width A with
	// B >= A is the identity on every bit pattern.
	//
	// NaN payload policy (both modes):
	//   - widening: the payload (including the signaling/quiet bit) is
	//     placed in the most significant mantissa bits, low bits zeroed;
	//   - narrowing: the most significant payload bits are kept, the rest
	//     truncated; if truncation would erase the whole payload, the
	//     lowest payload bit is set so the result stays NaN;
	//   - the signaling/quiet bit is always preserved; quieting a
	//     signaling NaN never happens implicitly.
	ModeBitPreserving Mode = iota

	// ModeNumeric performs IEEE 754 numeric conversion with
	// round-to-nearest-ties-to-even and full exception flags. The NaN
	// payload policy is identical to ModeBitPreserving; the difference is
	// purely contractual: no bit-level guarantee is made, only the numeric
	// value (plus flags) is promised.
	//
	// Subnormal policy (both modes): gradual underflow. Subnormal inputs
	// are converted exactly like normals; results too small for a normal
	// encoding become subnormals, raising FlagUnderflow when the rounding
	// was inexact. There is no flush-to-zero.
	ModeNumeric
)

// Flags collects IEEE 754 exception flags raised by a conversion.
type Flags struct {
	Invalid   bool // signaling NaN input was encountered
	Inexact   bool // the rounded result differs from the exact value
	Overflow  bool // magnitude too large; result is an infinity
	Underflow bool // result is subnormal and the rounding was inexact
}

// String renders the raised flags in a compact, stable form.
func (f Flags) String() string {
	if f == (Flags{}) {
		return "-"
	}
	s := ""
	if f.Invalid {
		s += "V"
	}
	if f.Overflow {
		s += "O"
	}
	if f.Underflow {
		s += "U"
	}
	if f.Inexact {
		s += "X"
	}
	return s
}

// Convert maps bits from width from to width to under the given mode,
// discarding exception flags. Unsupported widths are rejected with
// ErrUnsupportedWidth before any work.
func Convert(from, to Width, bitsIn uint64, mode Mode) (uint64, error) {
	out, _, err := ConvertFlags(from, to, bitsIn, mode)
	return out, err
}

// ConvertFlags is Convert plus the raised exception flags.
func ConvertFlags(from, to Width, bitsIn uint64, mode Mode) (uint64, Flags, error) {
	fl, ok := layoutOf(from)
	if !ok {
		return 0, Flags{}, ErrUnsupportedWidth
	}
	tl, ok := layoutOf(to)
	if !ok {
		return 0, Flags{}, ErrUnsupportedWidth
	}
	out, flags := convertWithFlags(fl, tl, MaskBits(from, bitsIn))
	return out, flags, nil
}

// convertWithFlags is the pure-integer conversion core. It never touches
// host floating point, so NaN payloads cannot be lost.
func convertWithFlags(fl, tl layout, b uint64) (uint64, Flags) {
	var flags Flags
	sign := b & fl.signMask()
	exp := (b & fl.expMask()) >> fl.manBits
	man := b & fl.manMask()
	tsign := uint64(0)
	if sign != 0 {
		tsign = tl.signMask()
	}
	expAll := mask(fl.expBits)

	switch {
	case exp == expAll:
		if man == 0 {
			// Infinity maps to infinity, same sign.
			return tsign | tl.expMask(), flags
		}
		// NaN: propagate payload, preserve the signaling/quiet bit.
		if b&fl.quietBit() == 0 {
			flags.Invalid = true
		}
		return tsign | tl.expMask() | convertNaNPayload(fl, tl, b), flags

	case exp == 0 && man == 0:
		// Zero: only the sign survives.
		return tsign, flags
	}

	// Finite nonzero: express the value as mant * 2^e2 with mant an
	// integer, then round to the target precision with pure integer math.
	var mant uint64
	var e2 int
	if exp == 0 {
		mant = man
		e2 = 1 - fl.bias - int(fl.manBits)
	} else {
		mant = man | (uint64(1) << fl.manBits)
		e2 = int(exp) - fl.bias - int(fl.manBits)
	}
	return tsign | roundFinite(tl, mant, e2, &flags), flags
}

// convertNaNPayload moves a NaN payload between formats, keeping the most
// significant bits and the signaling/quiet bit, and never producing a
// zero mantissa.
func convertNaNPayload(fl, tl layout, b uint64) uint64 {
	man := b & fl.manMask()
	var out uint64
	switch {
	case tl.manBits >= fl.manBits:
		out = man << (tl.manBits - fl.manBits)
	default:
		out = man >> (fl.manBits - tl.manBits)
		if out == 0 {
			out = 1 // truncation erased the payload: stay NaN
		}
	}
	return out
}

// roundFinite rounds mant * 2^e2 (mant != 0) into the target format using
// round-to-nearest-ties-to-even, with gradual underflow. Only the sign
// bits are left to the caller. Each value is rounded exactly once: the
// subnormal path rounds the original significand at the final quantum,
// never an already-rounded intermediate (no double rounding).
func roundFinite(tl layout, mant uint64, e2 int, flags *Flags) uint64 {
	p := int(tl.manBits) + 1 // precision including the implicit bit
	bl := bits.Len64(mant)
	emax := int(mask(tl.expBits)) - 1 - tl.bias // largest normal exponent
	emin := 1 - tl.bias                         // exponent of the smallest normal

	// Unbiased exponent of the leading bit of the exact value.
	expUnb := e2 + bl - 1

	if expUnb >= emin {
		// Normal result (or overflow): round to p significant bits.
		shift := bl - p
		var keep uint64
		if shift > 0 {
			keep = mant >> uint(shift)
			rem := mant & mask(uint(shift))
			if rem != 0 {
				flags.Inexact = true
				half := uint64(1) << uint(shift-1)
				if rem > half || (rem == half && keep&1 == 1) {
					keep++
					if keep>>uint(p) != 0 { // carried out of p bits
						keep >>= 1
						expUnb++
					}
				}
			}
		} else {
			keep = mant << uint(-shift)
		}
		if expUnb > emax {
			// Overflow: infinity, IEEE default for round-to-nearest.
			flags.Overflow = true
			flags.Inexact = true
			return tl.expMask()
		}
		field := uint64(expUnb + tl.bias)
		return field<<tl.manBits | (keep & tl.manMask())
	}

	// Subnormal territory: round the original significand once, at the
	// subnormal quantum 2^(emin-p+1).
	total := emin - e2 - p + 1 // right shift aligning mant to the quantum
	if total >= 64 {
		// mant < 2^53, so the value is far below half of the smallest
		// subnormal and rounds to zero.
		flags.Inexact = true
		flags.Underflow = true
		return 0
	}
	if total <= 0 {
		// Exact subnormal: the value lands on the quantum as-is.
		return mant << uint(-total)
	}
	out := mant >> uint(total)
	rem := mant & mask(uint(total))
	if rem != 0 {
		flags.Inexact = true
		half := uint64(1) << uint(total-1)
		if rem > half || (rem == half && out&1 == 1) {
			out++
			// Rounding may carry into the smallest normal; the bit
			// pattern is identical either way.
		}
	}
	if flags.Inexact && out < (uint64(1)<<tl.manBits) {
		// Underflow is detected after rounding: a result that rounded up
		// to the smallest normal is not tiny.
		flags.Underflow = true
	}
	return out
}
