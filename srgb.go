package linscale

import "math"

// sRGB transfer function constants (IEC 61966-2-1).
const (
	srgbThreshold = 0.04045
	srgbLinearCut = 0.0031308
	srgbScale     = 12.92
	srgbGamma     = 2.4
	srgbA         = 1.055
	srgbB         = 0.055
)

// decodeTable maps every 8-bit sRGB channel value to its linear-light value.
var decodeTable [256]float64

// encodeTable maps 16-bit quantized linear values (linear*65535) back to an
// 8-bit sRGB value. The 65536-entry table keeps the reverse conversion branch
// free inside the hot pixel loop while staying exact at the 8-bit granularity
// we emit.
var encodeTable [65536]uint8

func init() {
	for i := 0; i < 256; i++ {
		c := float64(i) / 255.0
		if c <= srgbThreshold {
			decodeTable[i] = c / srgbScale
		} else {
			decodeTable[i] = math.Pow((c+srgbB)/srgbA, srgbGamma)
		}
	}
	for i := 0; i < len(encodeTable); i++ {
		encodeTable[i] = encodeLinear(float64(i) / 65535.0)
	}
}

// decodeSRGB converts an 8-bit sRGB component to linear light in [0,1].
func decodeSRGB(v uint8) float64 { return decodeTable[v] }

// encodeLinear converts a linear-light component in [0,1] to an 8-bit sRGB
// component, clamped against round-off that may push values slightly outside
// the representable range.
func encodeLinear(c float64) uint8 {
	if c <= 0 {
		return 0
	}
	var s float64
	if c <= srgbLinearCut {
		s = srgbScale * c
	} else {
		s = srgbA*math.Pow(c, 1.0/srgbGamma) - srgbB
	}
	if s >= 1 {
		return 255
	}
	return uint8(s*255.0 + 0.5)
}

// encodeLinearLUT is the table-driven counterpart of encodeLinear used in the
// output hot path. c must be a value in [0,1].
func encodeLinearLUT(c float64) uint8 {
	if c <= 0 {
		return 0
	}
	q := int(c*65535.0 + 0.5)
	if q >= len(encodeTable) {
		return 255
	}
	return encodeTable[q]
}
