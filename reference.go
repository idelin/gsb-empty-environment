package texsample

import "math"

// ReferenceTexture is a high-precision float64 reimplementation of the
// sampling pipeline. It exists to measure the numerical error of the
// production float32 path against an independent, higher-precision result.
type ReferenceTexture struct {
	levels [][][4]float64
	dims   [][2]int
	srgb   bool
}

// NewReferenceTexture mirrors NewTexture but keeps every intermediate value
// in float64.
func NewReferenceTexture(rgba []float32, w, h int, srgb bool) *ReferenceTexture {
	if w <= 0 || h <= 0 || len(rgba) != w*h*4 {
		panic("texsample: invalid reference texture input")
	}
	r := &ReferenceTexture{srgb: srgb}

	base := make([][4]float64, w*h)
	for i := 0; i < w*h; i++ {
		p := i * 4
		if srgb {
			base[i] = [4]float64{
				decodeSRGB64(float64(rgba[p])),
				decodeSRGB64(float64(rgba[p+1])),
				decodeSRGB64(float64(rgba[p+2])),
				float64(rgba[p+3]),
			}
		} else {
			base[i] = [4]float64{
				float64(rgba[p]), float64(rgba[p+1]),
				float64(rgba[p+2]), float64(rgba[p+3]),
			}
		}
	}
	r.levels = append(r.levels, base)
	r.dims = append(r.dims, [2]int{w, h})

	for r.dims[len(r.dims)-1][0] > 1 || r.dims[len(r.dims)-1][1] > 1 {
		r.levels = append(r.levels, r.downsample())
	}
	return r
}

func (r *ReferenceTexture) MaxLOD() int { return len(r.levels) - 1 }

func (r *ReferenceTexture) downsample() [][4]float64 {
	top := len(r.levels) - 1
	src, dim := r.levels[top], r.dims[top]
	sw, sh := dim[0], dim[1]
	dw, dh := (sw+1)/2, (sh+1)/2
	dst := make([][4]float64, dw*dh)

	for dy := 0; dy < dh; dy++ {
		y0, y1 := dy*2, dy*2+2
		if y1 > sh {
			y1 = sh
		}
		for dx := 0; dx < dw; dx++ {
			x0, x1 := dx*2, dx*2+2
			if x1 > sw {
				x1 = sw
			}
			var sum [4]float64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := src[sy*sw+sx]
					for k := 0; k < 4; k++ {
						sum[k] += c[k]
					}
				}
			}
			count := float64((x1 - x0) * (y1 - y0))
			for k := 0; k < 4; k++ {
				sum[k] /= count
			}
			dst[dy*dw+dx] = sum
		}
	}
	r.dims = append(r.dims, [2]int{dw, dh})
	return dst
}

// SampleGrad mirrors Sampler.SampleGrad with all arithmetic in float64.
func (r *ReferenceTexture) SampleGrad(f Filter, wrapS, wrapT WrapMode,
	u, v, dudx, dvdx, dudy, dvdy float64, lodBias, minLOD, maxLOD float64) [4]float64 {

	// Quantize the inputs the same way the float32 path receives them, so the
	// comparison measures arithmetic precision rather than divergent texel
	// selection at half-texel boundaries.
	u, v = float64(float32(u)), float64(float32(v))
	dudx, dvdx = float64(float32(dudx)), float64(float32(dvdx))
	dudy, dvdy = float64(float32(dudy)), float64(float32(dvdy))

	w, h := r.dims[0][0], r.dims[0][1]
	rx := math.Hypot(dudx*float64(w), dvdx*float64(h))
	ry := math.Hypot(dudy*float64(w), dvdy*float64(h))
	rho := math.Max(rx, ry)
	lod := 0.0
	if rho > 0 && !math.IsInf(rho, 0) {
		lod = math.Log2(rho)
	}
	lod += lodBias
	if lod < minLOD {
		lod = minLOD
	}
	if maxLOD < lod {
		lod = maxLOD
	}
	if lod < 0 {
		lod = 0
	}
	if cap := float64(r.MaxLOD()); lod > cap {
		lod = cap
	}

	switch f {
	case FilterNearest:
		level := int(math.Floor(lod + 0.5))
		return r.sampleNearest(level, u, v, wrapS, wrapT)
	case FilterBilinear:
		level := int(math.Floor(lod + 0.5))
		return r.sampleLinear(level, u, v, wrapS, wrapT)
	default:
		if lod <= 0 {
			return r.sampleLinear(0, u, v, wrapS, wrapT)
		}
		lo := int(math.Floor(lod))
		hi := lo + 1
		if hi > r.MaxLOD() {
			return r.sampleLinear(lo, u, v, wrapS, wrapT)
		}
		frac := lod - float64(lo)
		c0 := r.sampleLinear(lo, u, v, wrapS, wrapT)
		c1 := r.sampleLinear(hi, u, v, wrapS, wrapT)
		var out [4]float64
		for k := 0; k < 4; k++ {
			out[k] = c0[k] + (c1[k]-c0[k])*frac
		}
		return out
	}
}

func (r *ReferenceTexture) sampleNearest(level int, u, v float64, wrapS, wrapT WrapMode) [4]float64 {
	w, h := r.dims[level][0], r.dims[level][1]
	x := wrapIndex64(int(math.Floor(u*float64(w)+1e-9)), w, wrapS)
	y := wrapIndex64(int(math.Floor(v*float64(h)+1e-9)), h, wrapT)
	return r.levels[level][y*w+x]
}

func (r *ReferenceTexture) sampleLinear(level int, u, v float64, wrapS, wrapT WrapMode) [4]float64 {
	w, h := r.dims[level][0], r.dims[level][1]
	x0, x1, fx := footprint64(u, w, wrapS)
	y0, y1, fy := footprint64(v, h, wrapT)

	c00 := r.levels[level][y0*w+x0]
	c10 := r.levels[level][y0*w+x1]
	c01 := r.levels[level][y1*w+x0]
	c11 := r.levels[level][y1*w+x1]

	var out [4]float64
	for k := 0; k < 4; k++ {
		top := c00[k] + (c10[k]-c00[k])*fx
		bottom := c01[k] + (c11[k]-c01[k])*fx
		out[k] = top + (bottom-top)*fy
	}
	return out
}

func footprint64(u float64, n int, wrap WrapMode) (i0, i1 int, f float64) {
	scaled := u*float64(n) - 0.5
	fl := math.Floor(scaled)
	f = scaled - fl
	base := int(fl)
	return wrapIndex64(base, n, wrap), wrapIndex64(base+1, n, wrap), f
}

func wrapIndex64(i, n int, wrap WrapMode) int {
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
		p := 2 * n
		q := i / p
		if (i < 0) != (p < 0) && i%p != 0 {
			q--
		}
		m := i - q*p
		if m >= n {
			return p - 1 - m
		}
		return m
	default:
		q := i / n
		if (i < 0) != (n < 0) && i%n != 0 {
			q--
		}
		return i - q*n
	}
}
