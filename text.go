package fpcodec

import (
	"fmt"
	"math"
	"strconv"
)

// Text renders a human-readable form of a bit pattern. It is strictly a
// display function: the wire format never uses text, and Text output is
// never parsed back. NaN payloads are shown from the raw bits (never via a
// host float, which could canonicalize them); finite values are widened
// exactly to float64 solely for hex-float formatting.
func Text(w Width, bits uint64) string {
	l, err := layoutOf(w)
	if err != nil {
		return fmt.Sprintf("<unsupported width %d>", int(w))
	}
	u := unpack(l, bits)
	sign := "+"
	if u.sign != 0 {
		sign = "-"
	}
	switch u.cls {
	case clsNaN:
		kind := "qNaN"
		if u.signaling {
			kind = "sNaN"
		}
		return fmt.Sprintf("%s%s(payload=0x%X)", sign, kind, u.payload)
	case clsInf:
		return sign + "Inf"
	case clsZero:
		return sign + "0"
	}
	// Exact widening to float64 for display only.
	b64, _, err := Convert(bits, w, Width64, DefaultPolicy())
	if err != nil {
		return fmt.Sprintf("<convert error: %v>", err)
	}
	f := math.Float64frombits(b64)
	return strconv.FormatFloat(f, 'x', -1, 64)
}
