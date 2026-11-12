package stft

import "math"

// fftPlan performs forward and inverse DFTs of a fixed size. The transform is
// an iterative radix-2 FFT for power-of-two sizes and Bluestein's algorithm
// otherwise, so callers can use any positive window length.
type fftPlan struct {
	n    int
	rad2 *radix2Plan
	blue *bluesteinPlan
}

type radix2Plan struct {
	n     int
	rev   []int
	roots []complex128
}

type bluesteinPlan struct {
	n     int
	m     int
	chirp []complex128
	b     []complex128
	work  []complex128
	rad2  *radix2Plan
}

func newFFTPlan(n int) *fftPlan {
	if n <= 0 {
		panic("stft: non-positive FFT size")
	}
	p := &fftPlan{n: n}
	if isPowerOfTwo(n) {
		p.rad2 = newRadix2Plan(n)
	} else {
		p.blue = newBluesteinPlan(n)
	}
	return p
}

func (p *fftPlan) forward(a []complex128) {
	if len(a) != p.n {
		panic("stft: FFT input has wrong length")
	}
	if p.rad2 != nil {
		p.rad2.transform(a)
		return
	}
	p.blue.transform(a)
}

// inverse computes the unnormalised conjugate transform followed by 1/n
// scaling: IFFT(x) = conj(FFT(conj(x))) / n.
func (p *fftPlan) inverse(a []complex128) {
	for i := range a {
		a[i] = complex(real(a[i]), -imag(a[i]))
	}
	p.forward(a)
	scale := 1 / float64(p.n)
	for i := range a {
		a[i] = complex(real(a[i])*scale, -imag(a[i])*scale)
	}
}

func newRadix2Plan(n int) *radix2Plan {
	if n < 1 || !isPowerOfTwo(n) {
		panic("stft: radix-2 plan requires a power-of-two size")
	}
	p := &radix2Plan{n: n}
	if n == 1 {
		return p
	}

	p.rev = make([]int, n)
	bits := 0
	for v := n; v > 1; v >>= 1 {
		bits++
	}
	for i := 1; i < n; i++ {
		p.rev[i] = (p.rev[i>>1] >> 1) | ((i & 1) << (bits - 1))
	}

	p.roots = make([]complex128, n/2)
	for i := range p.roots {
		angle := -2 * math.Pi * float64(i) / float64(n)
		p.roots[i] = complex(math.Cos(angle), math.Sin(angle))
	}
	return p
}

func (p *radix2Plan) transform(a []complex128) {
	n := p.n
	if n == 1 {
		return
	}
	for i := 0; i < n; i++ {
		j := p.rev[i]
		if j > i {
			a[i], a[j] = a[j], a[i]
		}
	}

	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := n / size
		for base := 0; base < n; base += size {
			for j := 0; j < half; j++ {
				u := a[base+j]
				v := a[base+j+half] * p.roots[j*step]
				a[base+j] = u + v
				a[base+j+half] = u - v
			}
		}
	}
}

func newBluesteinPlan(n int) *bluesteinPlan {
	if n < 2 || isPowerOfTwo(n) {
		panic("stft: Bluestein plan requires a non-power-of-two size >= 2")
	}
	m := 1
	for m < 2*n-1 {
		m <<= 1
	}

	p := &bluesteinPlan{
		n:     n,
		m:     m,
		chirp: make([]complex128, n),
		b:     make([]complex128, m),
		work:  make([]complex128, m),
		rad2:  newRadix2Plan(m),
	}

	fn := float64(n)
	for k := 0; k < n; k++ {
		kf := float64(k)
		angle := -math.Pi * kf * kf / fn
		p.chirp[k] = complex(math.Cos(angle), math.Sin(angle))
	}

	p.b[0] = 1
	for k := 1; k < n; k++ {
		kf := float64(k)
		angle := math.Pi * kf * kf / fn
		v := complex(math.Cos(angle), math.Sin(angle))
		p.b[k] = v
		p.b[m-k] = v
	}
	p.rad2.transform(p.b)
	return p
}

func (p *bluesteinPlan) transform(a []complex128) {
	for i := 0; i < p.n; i++ {
		p.work[i] = a[i] * p.chirp[i]
	}
	clear(p.work[p.n:])

	p.rad2.transform(p.work)
	for i := range p.work {
		p.work[i] *= p.b[i]
	}

	// Inverse FFT of the convolution product via conjugation.
	for i := range p.work {
		p.work[i] = complex(real(p.work[i]), -imag(p.work[i]))
	}
	p.rad2.transform(p.work)

	scale := 1 / float64(p.m)
	for i := 0; i < p.n; i++ {
		conv := complex(real(p.work[i])*scale, -imag(p.work[i])*scale)
		a[i] = conv * p.chirp[i]
	}
}

func isPowerOfTwo(n int) bool {
	return n > 0 && n&(n-1) == 0
}
