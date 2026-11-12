package taskpkg

import (
	"fmt"
	"math"
)

// ApplyFrame is an alias for the LUT's in-place frame method.
func (l *LUT) Apply(frame any, width, height, stride int) error {
	return l.ApplyFrame(frame, width, height, stride)
}

// ApplyFrame applies the LUT in place to a width-by-height linear RGB frame.
//
// pixels must be []float32 or []float64. Pixels are interleaved RGB triples.
// stride is the number of floating-point elements from the R component of one
// pixel to the R component of the next pixel. Consequently stride is at least
// 3; stride 4 processes RGB and leaves the interleaved alpha channel byte-for-
// byte unchanged. Larger strides can describe padding between pixels.
//
// The frame is processed in place. All dimensions and all RGB components are
// validated before the first pixel is written, so an error leaves the frame
// completely unchanged. Alpha components are neither read, validated, nor
// graded.
func (l *LUT) ApplyFrame(pixels any, width, height, stride int) error {
	switch p := pixels.(type) {
	case []float32:
		return applyFrame(l, p, width, height, stride)
	case []float64:
		return applyFrame(l, p, width, height, stride)
	default:
		return fmt.Errorf("%w: unsupported pixel slice type %T", ErrInvalidFrame, pixels)
	}
}

// ApplyFrame32 applies the LUT in place to a float32 RGB frame.
func (l *LUT) ApplyFrame32(pixels []float32, width, height, stride int) error {
	return applyFrame(l, pixels, width, height, stride)
}

// ApplyFrame64 applies the LUT in place to a float64 RGB frame.
func (l *LUT) ApplyFrame64(pixels []float64, width, height, stride int) error {
	return applyFrame(l, pixels, width, height, stride)
}

// ApplyPixels applies the LUT in place to count pixels separated by stride
// floating-point elements. It is the flat-frame equivalent of ApplyFrame.
func (l *LUT) ApplyPixels(pixels any, count, stride int) error {
	if count < 0 {
		return fmt.Errorf("%w: negative pixel count %d", ErrInvalidFrame, count)
	}
	return l.ApplyFrame(pixels, count, 1, stride)
}

// ApplyFrame is the generic form of LUT.ApplyFrame. The compiler infers T
// from the concrete slice type, so float32 and float64 frames use the same
// code path without an interface conversion.
func ApplyFrame[T Float](l *LUT, pixels []T, width, height, stride int) error {
	if l == nil {
		return fmt.Errorf("%w: nil LUT", ErrInvalidLUT)
	}
	return applyFrame(l, pixels, width, height, stride)
}

func applyFrame[T Float](l *LUT, pixels []T, width, height, stride int) error {
	if l == nil {
		return fmt.Errorf("%w: nil LUT", ErrInvalidLUT)
	}
	count, err := validateFrameLayout(pixels, width, height, stride)
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	// Validation is deliberately a complete pass over the RGB components
	// before any write. This is what makes an in-place call atomic on error.
	if err := validateFramePixels(l, pixels, count, stride); err != nil {
		return err
	}

	base := 0
	for i := 0; i < count; i++ {
		out := l.sampleChecked(
			float64(pixels[base+0]),
			float64(pixels[base+1]),
			float64(pixels[base+2]),
		)
		pixels[base+0] = T(out.R)
		pixels[base+1] = T(out.G)
		pixels[base+2] = T(out.B)
		base += stride
	}
	return nil
}

func validateFrameLayout[T Float](pixels []T, width, height, stride int) (int, error) {
	if width < 0 || height < 0 {
		return 0, fmt.Errorf("%w: negative frame dimensions %dx%d", ErrInvalidFrame, width, height)
	}

	if height != 0 && int64(width) > int64(maxInt())/int64(height) {
		return 0, fmt.Errorf("%w: frame pixel count overflows", ErrInvalidFrame)
	}
	count64 := int64(width) * int64(height)
	if count64 == 0 {
		return 0, nil
	}
	if stride < 3 {
		return 0, fmt.Errorf("%w: stride %d is less than an RGB pixel", ErrInvalidFrame, stride)
	}

	lastPixel := count64 - 1
	if lastPixel > (int64(maxInt())-3)/int64(stride) {
		return 0, fmt.Errorf("%w: frame extent overflows", ErrInvalidFrame)
	}
	required64 := lastPixel*int64(stride) + 3
	if required64 > int64(len(pixels)) {
		return 0, fmt.Errorf("%w: frame has %d elements, need at least %d for %d pixels at stride %d",
			ErrInvalidFrame, len(pixels), required64, count64, stride)
	}
	return int(count64), nil
}

func maxInt() int {
	const max = int(^uint(0) >> 1)
	return max
}

func validateFramePixels[T Float](l *LUT, pixels []T, count, stride int) error {
	base := 0
	for i := 0; i < count; i++ {
		for channel := 0; channel < 3; channel++ {
			v := float64(pixels[base+channel])
			if math.IsNaN(v) {
				return &ColorError{Pixel: i, Channel: channel, Value: v, Err: ErrNonFinite}
			}
			if l.mode == BoundaryReject &&
				(math.IsInf(v, 0) || v < l.lower[channel] || v > l.upper[channel]) {
				return &ColorError{Pixel: i, Channel: channel, Value: v, Err: ErrOutOfRange}
			}
		}
		base += stride
	}
	return nil
}
