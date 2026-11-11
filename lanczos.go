package linscale

import "math"

// lanczosA is the Lanczos window parameter (Lanczos-3).
const lanczosA = 3.0

// sinc function used by the Lanczos kernel.
func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

// lanczos3 evaluates the Lanczos-3 kernel at distance x.
func lanczos3(x float64) float64 {
	if x < 0 {
		x = -x
	}
	if x >= lanczosA {
		return 0
	}
	return sinc(x) * sinc(x/lanczosA)
}

// contrib holds one axis' precomputed filter: destination sample dstStart[i]
// draws weights[dstStart[i]:dstStart[i+1]] from consecutive source samples
// beginning at srcStart[i]. Source indices outside the image are clamped at
// sampling time, so several weights may accumulate onto the same edge pixel.
type contrib struct {
	srcStart []int
	dstStart []int
	weights  []float32
}

// buildAxis precomputes the separable 1-D filter for resizing srcLen samples
// into dstLen samples. scale is the source-to-destination sampling ratio
// (output / input): it is below 1 when downscaling. In that case the kernel
// support is widened inversely with the scale so the filter acts as a proper
// low-pass anti-aliasing filter; upscaling keeps the native three-tap
// support.
func buildAxis(srcLen, dstLen int) contrib {
	scale := float64(dstLen) / float64(srcLen)
	// For downscaling the filter is evaluated in input space at distance
	// (center-j)*scale, which is equivalent to a kernel stretched to
	// support lanczosA/scale; upscaling uses scale 1.
	kernelScale := scale
	if kernelScale > 1 {
		kernelScale = 1
	}
	support := lanczosA / kernelScale

	c := contrib{
		srcStart: make([]int, dstLen),
		dstStart: make([]int, dstLen+1),
	}

	for i := 0; i < dstLen; i++ {
		center := (float64(i)+0.5)/scale - 0.5
		first := int(math.Ceil(center - support))
		last := int(math.Floor(center + support))

		// Normalize against the raw, unclamped kernel mass so edges keep
		// their full energy under clamp-to-border handling.
		var mass float64
		for j := first; j <= last; j++ {
			mass += lanczos3((center - float64(j)) * kernelScale)
		}

		c.srcStart[i] = first
		c.dstStart[i] = len(c.weights)
		for j := first; j <= last; j++ {
			w := lanczos3((center-float64(j))*kernelScale) / mass
			c.weights = append(c.weights, float32(w))
		}
	}
	c.dstStart[dstLen] = len(c.weights)

	return c
}

// clampIndex clamps a source sample index into [0, n-1].
func clampIndex(j, n int) int {
	if j < 0 {
		return 0
	}
	if j >= n {
		return n - 1
	}
	return j
}
