package floatwire

import (
	"fmt"
	"math"
	"strconv"
)

// Format renders a bit pattern as human-readable text. It is a display
// facility only: the exact storage form is Encode, and Format output is
// never parsed back. NaN payloads and the signaling bit are shown from
// the raw bits, never via host floats, so no payload information is lost.
//
// Grammar:
//
//	+0 / -0                     signed zero
//	+Inf / -Inf                 infinities
//	NaN(0x<payload>)            quiet NaN with payload (quiet bit excluded)
//	sNaN(0x<payload>)           signaling NaN
//	<number>                    finite value via strconv shortest form
func Format(w Width, bits uint64) (string, error) {
	l, ok := layoutOf(w)
	if !ok {
		return "", ErrUnsupportedWidth
	}
	bits = MaskBits(w, bits)
	sign := ""
	if bits&l.signMask() != 0 {
		sign = "-"
	}
	switch Classify(w, bits) {
	case NaN:
		kind := "NaN"
		if IsSignaling(w, bits) {
			kind = "sNaN"
		}
		return fmt.Sprintf("%s%s(0x%x)", sign, kind, NaNPayload(w, bits)), nil
	case Infinite:
		return sign + "Inf", nil
	case Zero:
		return sign + "0", nil
	}
	// Finite nonzero: shortest round-trip decimal via the host, display
	// only. The stored bits are untouched.
	return sign + formatFinite(w, bits&^l.signMask()), nil
}

func formatFinite(w Width, mag uint64) string {
	switch w {
	case Float16:
		// No host float16 exists; decode the value manually into a
		// float64 (every finite float16 is exactly representable).
		return strconv.FormatFloat(float16ToFloat64(mag), 'g', -1, 64)
	case Float32:
		return strconv.FormatFloat(float64(math.Float32frombits(uint32(mag))), 'g', -1, 32)
	default:
		return strconv.FormatFloat(math.Float64frombits(mag), 'g', -1, 64)
	}
}

// float16ToFloat64 converts a finite float16 magnitude (sign bit clear)
// to float64 exactly, for display purposes only.
func float16ToFloat64(bits uint64) float64 {
	l, _ := layoutOf(Float16)
	exp := (bits & l.expMask()) >> l.manBits
	man := bits & l.manMask()
	if exp == 0 {
		return math.Ldexp(float64(man), 1-l.bias-int(l.manBits))
	}
	return math.Ldexp(float64(man|(1<<l.manBits)), int(exp)-l.bias-int(l.manBits))
}
