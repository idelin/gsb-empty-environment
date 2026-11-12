package hrtf

import "math"

// Convolver applies the interpolated, fractionally delayed response of one ear
// to a stream of input samples. It is stateful: a ring buffer keeps the input
// history needed by the longest delay and the full FIR.
type Convolver struct {
	ear   Channel
	ring  []float64
	head  int // next write position; newest sample lives at head-1
	fir   []float64
	delay float64
	loc   Location
}

// NewConvolver sizes a convolver for FIRs of length firLen whose integer delay
// never exceeds maxDelay. Prefer Engine.NewConvolver when working with a mesh.
func NewConvolver(ear Channel, maxDelay, firLen int) *Convolver {
	if maxDelay < 0 || firLen <= 0 {
		panic("hrtf: invalid convolver dimensions")
	}
	return &Convolver{
		ear:  ear,
		ring: make([]float64, maxDelay+firLen+2),
		fir:  make([]float64, firLen),
	}
}

// NewConvolver builds a convolver sized for the mesh's longest delay.
func (e *Engine) NewConvolver(ear Channel) *Convolver {
	maxDelay := 0
	for i := range e.mesh.vertices {
		if d := e.mesh.vertices[i].channel(ear).Delay; d > maxDelay {
			maxDelay = d
		}
	}
	return NewConvolver(ear, maxDelay, e.mesh.length)
}

// Ear reports which channel the convolver serves.
func (c *Convolver) Ear() Channel { return c.ear }

// Location returns the triangle location of the current response.
func (c *Convolver) Location() Location { return c.loc }

// Delay returns the current interpolated delay in samples.
func (c *Convolver) Delay() float64 { return c.delay }

// SetResponse installs an interpolated response. Filters only change between
// blocks so the output remains continuous inside a block; call both ear
// convolvers from the same Engine.Update to keep them on the same instant.
func (c *Convolver) SetResponse(r Response) {
	if len(r.FIR) != len(c.fir) {
		panic("hrtf: response FIR length does not match convolver")
	}
	c.fir = append(c.fir[:0], r.FIR...)
	c.delay = r.Delay
	c.loc = r.Loc
}

// ProcessBlock convolves one block of input samples and returns one output
// sample per input sample, carrying the internal history across calls.
func (c *Convolver) ProcessBlock(in []float64) []float64 {
	out := make([]float64, len(in))
	if len(in) == 0 {
		return out
	}

	base := math.Floor(c.delay)
	d := int(base)
	frac := c.delay - base
	capn := len(c.ring)

	for n, x := range in {
		c.ring[c.head] = x
		c.head = (c.head + 1) % capn

		// newest sample is at head-1; x[n-k] is at head-1-(k-1) = head-k
		var acc float64
		for j, h := range c.fir {
			k := d + j
			near := c.ring[mod(c.head-1-k, capn)] // x[n-d-j]
			far := c.ring[mod(c.head-2-k, capn)]  // x[n-d-j-1]
			acc += h * ((1-frac)*near + frac*far)
		}
		out[n] = acc
	}
	return out
}

// Update resolves dir once and installs the left and right responses at the
// same sampling instant, preserving interaural timing coherence.
func (e *Engine) Update(dir Vec3, left, right *Convolver) error {
	if left.ear != Left || right.ear != Right {
		panic("hrtf: convolver ear mismatch in Update")
	}
	l, r, err := e.Interpolate(dir)
	if err != nil {
		return err
	}
	left.SetResponse(l)
	right.SetResponse(r)
	return nil
}

func mod(a, n int) int {
	r := a % n
	if r < 0 {
		r += n
	}
	return r
}
