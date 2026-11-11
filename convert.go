package yuvconv

import "image"

// bit depths of the supported formats
const (
	depth8  = 8
	depth10 = 10
)

// converter holds the precomputed normalization and matrix coefficients for a
// particular parameter set so that per-pixel work stays branch-light.
type converter struct {
	p Params

	cw, ch int // chroma plane dimensions: ceil(w/2), ceil(h/2)

	// yScale/yOffset turn a code value into normalized luma (16..235 style).
	yScale, yOffset float64
	// cScale/cOffset turn a code value into a centered chroma offset (-0.5..0.5).
	cScale, cOffset float64

	// Y'CbCr (full-range normalized) -> linear-ish sRGB gamma-domain RGB.
	rCr float64
	gCr float64
	gCb float64
	bCb float64
}

func newConverter(p Params, depth int) (*converter, error) {
	if p.Width <= 0 || p.Height <= 0 || !p.Matrix.valid() || !p.Range.valid() || !p.Chroma.valid() {
		return nil, ErrInvalidParams
	}

	c := &converter{
		p:  p,
		cw: (p.Width + 1) / 2,
		ch: (p.Height + 1) / 2,
	}

	// Range expansion. Limited range keeps the historical head/foot room
	// (16..235 for luma, 16..240 for chroma), rescaled to the actual bit depth.
	max := float64(int(1)<<depth - 1)
	if p.Range == RangeLimited {
		low := 16.0 * max / 255.0
		yHigh := 235.0 * max / 255.0
		cHigh := 240.0 * max / 255.0
		c.yScale = 1.0 / (yHigh - low)
		c.yOffset = -low / (yHigh - low)
		c.cScale = 1.0 / (cHigh - low)
		c.cOffset = -(0.5 + low/(cHigh-low))
	} else {
		c.yScale = 1.0 / max
		c.yOffset = 0
		c.cScale = 1.0 / max
		c.cOffset = -128.0 / max
	}

	// Matrix coefficients derived from the standard (Kr, Kb) luma weights.
	// Chroma is normalized to -0.5..0.5 here, so the textbook -1..1
	// coefficients are multiplied by 2.
	kr, kb := matrixWeights(p.Matrix)
	kg := 1 - kr - kb
	c.bCb = 2 * (1 - kb)
	c.rCr = 2 * (1 - kr)
	c.gCb = -4 * (1 - kb) * kb / kg
	c.gCr = -4 * (1 - kr) * kr / kg

	return c, nil
}

func matrixWeights(m Matrix) (kr, kb float64) {
	switch m {
	case MatrixBT601:
		return 0.299, 0.114
	case MatrixBT709:
		return 0.2126, 0.0722
	case MatrixBT2020:
		return 0.2627, 0.0593
	default:
		panic(ErrInvalidParams)
	}
}

// sampleFn returns the raw code value of chroma component cb (true) or cr
// (false) at chroma sample (cx, cy).
type sampleFn func(cx, cy int, cb bool) float64

// chromaAt bilinearly upsamples chroma for luma pixel (x, y).
func (c *converter) chromaAt(x, y int, sample sampleFn, cb bool) float64 {
	if c.p.Chroma == ChromaLeft {
		// One sample per 2x2 luma block, co-sited with its top-left pixel.
		return sample(x/2, y/2, cb)
	}

	// Center siting: a luma pixel at even index (except the border) lies
	// halfway between two chroma samples; odd indices share their block
	// sample. Borders clamp so odd-sized frames never leave the plane.
	cx0 := x / 2
	cy0 := y / 2
	wx, wy := 0.0, 0.0
	if x%2 == 0 && x > 0 {
		cx0 = x/2 - 1
		wx = 0.5
	}
	if y%2 == 0 && y > 0 {
		cy0 = y/2 - 1
		wy = 0.5
	}
	cx1 := min(cx0+1, c.cw-1)
	cy1 := min(cy0+1, c.ch-1)

	v00 := sample(cx0, cy0, cb)
	v10 := sample(cx1, cy0, cb)
	v01 := sample(cx0, cy1, cb)
	v11 := sample(cx1, cy1, cb)

	top := v00*(1-wx) + v10*wx
	bot := v01*(1-wx) + v11*wx
	return top*(1-wy) + bot*wy
}

// run performs the per-pixel conversion; yAt returns raw luma code values and
// sample the raw chroma code values.
func (c *converter) run(yAt func(x, y int) float64, sample sampleFn) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, c.p.Width, c.p.Height))

	for y := 0; y < c.p.Height; y++ {
		for x := 0; x < c.p.Width; x++ {
			yn := yAt(x, y)*c.yScale + c.yOffset
			cb := c.chromaAt(x, y, sample, true)*c.cScale + c.cOffset
			cr := c.chromaAt(x, y, sample, false)*c.cScale + c.cOffset

			r := yn + c.rCr*cr
			g := yn + c.gCr*cr + c.gCb*cb
			b := yn + c.bCb*cb

			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = clamp8(r)
			dst.Pix[i+1] = clamp8(g)
			dst.Pix[i+2] = clamp8(b)
			dst.Pix[i+3] = 255
		}
	}
	return dst
}

func clamp8(v float64) uint8 {
	// Round half away from zero, then clamp to the 8-bit sRGB code range.
	iv := int(v*255 + 0.5)
	if iv < 0 {
		return 0
	}
	if iv > 255 {
		return 255
	}
	return uint8(iv)
}

// I420 converts a planar I420 frame to 8-bit sRGB.
func I420(frame I420Frame, p Params) (*image.RGBA, error) {
	c, err := newConverter(p, depth8)
	if err != nil {
		return nil, err
	}
	yStride, cStride, err := i420Strides(frame, c)
	if err != nil {
		return nil, err
	}

	yAt := func(x, y int) float64 {
		return float64(frame.Y[y*yStride+x])
	}
	sample := func(cx, cy int, cb bool) float64 {
		plane := frame.V
		if cb {
			plane = frame.U
		}
		return float64(plane[cy*cStride+cx])
	}
	return c.run(yAt, sample), nil
}

// NV12 converts a semi-planar NV12 frame to 8-bit sRGB.
func NV12(frame NV12Frame, p Params) (*image.RGBA, error) {
	c, err := newConverter(p, depth8)
	if err != nil {
		return nil, err
	}
	yStride, cStride, err := nv12Strides(frame, c)
	if err != nil {
		return nil, err
	}

	yAt := func(x, y int) float64 {
		return float64(frame.Y[y*yStride+x])
	}
	sample := func(cx, cy int, cb bool) float64 {
		// Cb and Cr interleave byte-wise: Cb at even offsets, Cr at odd ones.
		off := cy*cStride + cx*2
		if cb {
			return float64(frame.C[off])
		}
		return float64(frame.C[off+1])
	}
	return c.run(yAt, sample), nil
}

// P010 converts a 10-bit semi-planar P010 frame to 8-bit sRGB. Components are
// stored in the high 10 bits of each uint16.
func P010(frame P010Frame, p Params) (*image.RGBA, error) {
	c, err := newConverter(p, depth10)
	if err != nil {
		return nil, err
	}
	yStride, cStride, err := p010Strides(frame, c)
	if err != nil {
		return nil, err
	}

	yAt := func(x, y int) float64 {
		return float64(frame.Y[y*yStride+x] >> 6)
	}
	sample := func(cx, cy int, cb bool) float64 {
		off := cy*cStride + cx*2
		if cb {
			return float64(frame.C[off] >> 6)
		}
		return float64(frame.C[off+1] >> 6)
	}
	return c.run(yAt, sample), nil
}
