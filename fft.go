package taskpkg

import "math"

// fft performs an in-place radix-2 DFT on a, whose length must be a power of
// two. The forward transform uses the exp(-2*pi*i/n) convention; the inverse
// transform is scaled by 1/n so that fft(fft(x), true) returns x.
func fft(a []complex128, inverse bool) {
	n := len(a)
	if n <= 1 {
		return
	}
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j &^= bit
		}
		j |= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		ang := -2 * math.Pi / float64(size)
		if inverse {
			ang = -ang
		}
		wlen := complex(math.Cos(ang), math.Sin(ang))
		for i := 0; i < n; i += size {
			w := complex(1, 0)
			for k := 0; k < half; k++ {
				u := a[i+k]
				v := a[i+k+half] * w
				a[i+k] = u + v
				a[i+k+half] = u - v
				w *= wlen
			}
		}
	}
	if inverse {
		inv := complex(1/float64(n), 0)
		for i := range a {
			a[i] *= inv
		}
	}
}

// nextPow2 returns the smallest power of two greater than or equal to n.
func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
