package taskpkg

import "math"

// fft performs an in-place radix-2 decimation-in-time discrete Fourier
// transform. len(a) must be a power of two. Inverse is selected with
// inverse == true; it applies the standard 1/N normalization.
func fft(a []complex128, inverse bool) {
	n := len(a)
	if n <= 1 {
		return
	}

	// Bit-reversal permutation: swap each index with the bit-reversed
	// index once, so butterflies run on natural-order output.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}

	sign := 1.0
	if inverse {
		sign = -1.0
	}
	for length := 2; length <= n; length <<= 1 {
		half := length >> 1
		// Forward DFT uses negative-sign exponentials; the inverse flips it.
		angle := -sign * 2 * math.Pi / float64(length)
		for start := 0; start < n; start += length {
			for k := 0; k < half; k++ {
				w := complex(math.Cos(angle*float64(k)),
					math.Sin(angle*float64(k)))
				even := a[start+k]
				odd := a[start+k+half] * w
				a[start+k] = even + odd
				a[start+k+half] = even - odd
			}
		}
	}

	if inverse {
		scale := 1 / float64(n)
		for i := range a {
			a[i] *= complex(scale, 0)
		}
	}
}

// hannWindow returns a length-n periodic Hann window suitable for
// overlap-add analysis and synthesis.
func hannWindow(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n)))
	}
	return w
}
