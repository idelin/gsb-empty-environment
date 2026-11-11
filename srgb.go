package texsample

import "math"

// DecodeSRGB converts one sRGB-encoded channel value (assumed in [0, 1]) to
// linear light. The conversion is evaluated in float64 and rounded to
// float32 so the package and the high-precision reference share the exact
// same transfer function.
func DecodeSRGB(c float32) float32 {
	return float32(decodeSRGB64(float64(c)))
}

// EncodeSRGB converts one linear-light channel value (assumed in [0, 1]) to
// the sRGB-encoded representation.
func EncodeSRGB(c float32) float32 {
	return float32(encodeSRGB64(float64(c)))
}

func decodeSRGB64(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func encodeSRGB64(c float64) float64 {
	if c <= 0.0031308 {
		return c * 12.92
	}
	return 1.055*math.Pow(c, 1.0/2.4) - 0.055
}
