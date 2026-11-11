package texsample

import "math"

// Sampler holds the sampling state shared by fragment-shader texture calls.
type Sampler struct {
	// Filter selects nearest, bilinear or trilinear filtering.
	Filter Filter
	// WrapS and WrapT select the address mode for the u and v axes.
	WrapS, WrapT WrapMode
	// LODBias is added to the LOD computed from the screen-space derivatives.
	LODBias float32
	// MinLOD and MaxLOD clamp the selected mip level. The default zero value
	// of MinLOD is correct; set MaxLOD to math.MaxFloat32 when constructing a
	// sampler manually, or use NewSampler / DefaultSampler.
	MinLOD, MaxLOD float32
}

// NewSampler returns a sampler with safe defaults (clamped LOD range).
func NewSampler(filter Filter, wrapS, wrapT WrapMode) Sampler {
	return Sampler{
		Filter: filter,
		WrapS:  wrapS,
		WrapT:  wrapT,
		MinLOD: 0,
		MaxLOD: math.MaxFloat32,
	}
}

// DefaultSampler returns a trilinear sampler with repeat addressing.
func DefaultSampler() Sampler {
	return NewSampler(FilterTrilinear, WrapRepeat, WrapRepeat)
}

// Sample fetches level 0 without using derivatives (equivalent to zero
// screen-space derivatives, i.e. magnification at level 0).
func (s Sampler) Sample(t *Texture, u, v float32) [4]float32 {
	return s.SampleGrad(t, u, v, 0, 0, 0, 0)
}

// SampleGrad samples the texture at (u, v). ddx/ddy are the derivatives of
// (u, v) with respect to the two screen-space axes; the LOD is
// log2(max(length(dx), length(dy))) as in the common GPU convention. The
// returned color is linear RGBA regardless of the source color space.
func (s Sampler) SampleGrad(t *Texture, u, v, dudx, dvdx, dudy, dvdy float32) [4]float32 {
	lod := screenLOD(t, dudx, dvdx, dudy, dvdy) + s.LODBias
	lod = clampLOD(lod, s.MinLOD, s.MaxLOD, t.MaxLOD())

	switch s.Filter {
	case FilterNearest:
		level := clampInt(int(math.Floor(float64(lod)+0.5)), 0, t.MaxLOD())
		return sampleNearestLevel(t.Levels[level], u, v, s.WrapS, s.WrapT)
	case FilterBilinear:
		level := clampInt(int(math.Floor(float64(lod)+0.5)), 0, t.MaxLOD())
		return sampleLinearLevel(t.Levels[level], u, v, s.WrapS, s.WrapT)
	default: // FilterTrilinear
		if lod <= 0 {
			return sampleLinearLevel(t.Levels[0], u, v, s.WrapS, s.WrapT)
		}
		lo := clampInt(int(math.Floor(float64(lod))), 0, t.MaxLOD())
		hi := lo + 1
		if hi > t.MaxLOD() {
			return sampleLinearLevel(t.Levels[lo], u, v, s.WrapS, s.WrapT)
		}
		f := lod - float32(lo)
		c0 := sampleLinearLevel(t.Levels[lo], u, v, s.WrapS, s.WrapT)
		c1 := sampleLinearLevel(t.Levels[hi], u, v, s.WrapS, s.WrapT)
		return [4]float32{
			c0[0] + (c1[0]-c0[0])*f,
			c0[1] + (c1[1]-c0[1])*f,
			c0[2] + (c1[2]-c0[2])*f,
			c0[3] + (c1[3]-c0[3])*f,
		}
	}
}

// screenLOD implements lambda = log2(max(rho_x, rho_y)) where each rho is the
// length of the derivative vector expressed in texel units. Zero derivatives
// yield LOD 0 (magnification).
func screenLOD(t *Texture, dudx, dvdx, dudy, dvdy float32) float32 {
	w := float32(t.Levels[0].W)
	h := float32(t.Levels[0].H)
	rx := math.Hypot(float64(dudx*w), float64(dvdx*h))
	ry := math.Hypot(float64(dudy*w), float64(dvdy*h))
	rho := math.Max(rx, ry)
	if !(rho > 0) || math.IsInf(rho, 0) {
		return 0
	}
	return float32(math.Log2(rho))
}

func clampLOD(lod, minLOD, maxLOD float32, maxLevel int) float32 {
	if lod < minLOD {
		lod = minLOD
	}
	if maxLOD < lod {
		lod = maxLOD
	}
	if lod < 0 {
		lod = 0
	}
	if cap := float32(maxLevel); lod > cap {
		lod = cap
	}
	return lod
}

func sampleNearestLevel(l Level, u, v float32, wrapS, wrapT WrapMode) [4]float32 {
	x := nearestIndex(u, l.W, wrapS)
	y := nearestIndex(v, l.H, wrapT)
	p := (y*l.W + x) * 4
	return [4]float32{l.Data[p], l.Data[p+1], l.Data[p+2], l.Data[p+3]}
}

func sampleLinearLevel(l Level, u, v float32, wrapS, wrapT WrapMode) [4]float32 {
	x0, x1, fx := linearFootprint(u, l.W, wrapS)
	y0, y1, fy := linearFootprint(v, l.H, wrapT)

	p00 := (y0*l.W + x0) * 4
	p10 := (y0*l.W + x1) * 4
	p01 := (y1*l.W + x0) * 4
	p11 := (y1*l.W + x1) * 4

	var out [4]float32
	for c := 0; c < 4; c++ {
		top := l.Data[p00+c] + (l.Data[p10+c]-l.Data[p00+c])*fx
		bottom := l.Data[p01+c] + (l.Data[p11+c]-l.Data[p01+c])*fx
		out[c] = top + (bottom-top)*fy
	}
	return out
}

// nearestIndex maps a normalized coordinate to the nearest texel index.
// Texel centers sit at (i+0.5)/n, matching the half-texel-inset convention
// used by linearFootprint, so nearest and bilinear sampling agree at centers.
func nearestIndex(u float32, n int, wrap WrapMode) int {
	// With the half-texel inset, nearest selection reduces to floor(u*n);
	// the tiny epsilon absorbs float32 representation error at exact centers.
	idx := int(math.Floor(float64(u)*float64(n) + nearestEpsilon))
	return wrapIndex(idx, n, wrap)
}

// nearestEpsilon covers float32 representation error (~1e-7 relative) while
// remaining far below one texel for any realistic texture size.
const nearestEpsilon = 1e-9

// linearFootprint returns the two wrapped texel indices bracketing u together
// with the interpolation weight, following the half-texel inset convention so
// that u == 0 and u == 1 land on texel centers.
func linearFootprint(u float32, n int, wrap WrapMode) (i0, i1 int, f float32) {
	scaled := float64(u)*float64(n) - 0.5
	fl := math.Floor(scaled)
	f = float32(scaled - fl)
	base := int(fl)
	i0 = wrapIndex(base, n, wrap)
	i1 = wrapIndex(base+1, n, wrap)
	return i0, i1, f
}

// wrapIndex applies the address mode to an integer texel index.
func wrapIndex(i, n int, wrap WrapMode) int {
	switch wrap {
	case WrapClamp:
		if i < 0 {
			return 0
		}
		if i >= n {
			return n - 1
		}
		return i
	case WrapMirror:
		// Period of 2n; the second half reverses direction.
		p := 2 * n
		// Go's % truncates toward zero, so use floor division to normalize
		// negative indices correctly (e.g. -p maps back to 0).
		q := i / p
		if (i < 0) != (p < 0) && i%p != 0 {
			q--
		}
		m := i - q*p
		if m >= n {
			return p - 1 - m
		}
		return m
	default: // WrapRepeat
		q := i / n
		if (i < 0) != (n < 0) && i%n != 0 {
			q--
		}
		return i - q*n
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
