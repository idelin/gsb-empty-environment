package chromaresample

import "math"

// tap is one weighted source sample in a resampling filter.
type tap struct {
	index  int
	weight float64
}

// axisTable holds the precomputed filter for every output sample of one axis.
type axisTable struct {
	tables [][]tap
}

// mirrorIndex maps any integer sample coordinate onto [0, n-1] using
// pixel-mirror ("reflect") boundaries: the edge sample is repeated and the
// pattern inside is 0,1,...,n-1,n-1,...,1,0,1,...
func mirrorIndex(i, n int) int {
	if n == 1 {
		return 0
	}
	period := 2 * n
	m := i % period
	if m < 0 {
		m += period
	}
	if m >= n {
		m = period - 1 - m
	}
	return m
}

// buildAxis precomputes area-weighted taps along one axis.
//
// nIn/nOut are the pixel counts on the axis, cIn/cOut the sampling centers and
// dIn/dOut the sample spacings, all in common reference-grid pixel units.
// Source sample k occupies the continuous cell [pIn*k-cIn-1/2*dIn,
// pIn*k-cIn+1/2*dIn), where pIn = dIn in input-pixel units. The kernel is
// scaled by the output-to-input spacing ratio and integrated over each input
// cell covered by the output cell.
func buildAxis(k Kernel, nIn, nOut int, cIn, cOut, dIn, dOut float64) axisTable {
	scale := dOut / dIn
	support := k.Support() * scale
	out := axisTable{tables: make([][]tap, nOut)}

	for j := 0; j < nOut; j++ {
		// uj is the output sample center in input-pixel coordinates; the
		// output cell spans [uj-scale/2, uj+scale/2].
		uj := (float64(j)*dOut + cOut - cIn) / dIn
		lo := int(math.Floor(uj-support)) - 1
		hi := int(math.Ceil(uj+support)) + 1

		var taps []tap
		sum := 0.0
		for idx := lo; idx <= hi; idx++ {
			cellLo := math.Max(float64(idx)-0.5, uj-support)
			cellHi := math.Min(float64(idx)+0.5, uj+support)
			if cellHi-cellLo <= 0 {
				continue
			}
			w := integrate(k, (cellLo-uj)/scale, (cellHi-uj)/scale)
			if w == 0 {
				continue
			}
			taps = append(taps, tap{index: idx, weight: w})
			sum += w
		}

		if len(taps) == 0 {
			// Degenerate geometry: fall back to the nearest source sample.
			taps = append(taps, tap{index: int(math.Round(uj)), weight: 1})
			sum = 1
		}

		// Normalize so constant signals are preserved regardless of scale.
		for t := range taps {
			taps[t].weight /= sum
		}
		out.tables[j] = taps
	}
	return out
}

// Resampler relocates samples of one planar component between two sampling
// grids. A Resampler is safe for repeated use with different planes matching
// the geometry it was built for.
type Resampler struct {
	geo Geometry
	k   Kernel
	h   axisTable
	v   axisTable
}

// New builds a Resampler for the given geometry and separable kernel. It
// returns ErrInvalidGeometry for non-positive dimensions, strides or
// sampling centers with non-positive denominators.
func New(geo Geometry, k Kernel) (*Resampler, error) {
	if err := geo.validate(); err != nil {
		return nil, err
	}
	if k == nil || k.Support() <= 0 {
		return nil, ErrInvalidGeometry
	}

	inW, inH := geo.Src.Width, geo.Src.Height
	outW, outH := geo.Dst.Width, geo.Dst.Height
	dInX := float64(geo.RefWidth) / float64(inW)
	dInY := float64(geo.RefHeight) / float64(inH)
	dOutX := float64(geo.RefWidth) / float64(outW)
	dOutY := float64(geo.RefHeight) / float64(outH)

	r := &Resampler{
		geo: geo,
		k:   k,
		h: buildAxis(k, inW, outW,
			geo.Src.CenterX.Float(), geo.Dst.CenterX.Float(), dInX, dOutX),
		v: buildAxis(k, inH, outH,
			geo.Src.CenterY.Float(), geo.Dst.CenterY.Float(), dInY, dOutY),
	}
	return r, nil
}

// Geometry returns the geometry the resampler was built for.
func (r *Resampler) Geometry() Geometry { return r.geo }

// Relocate resamples src into dst. Both planes must match the source and
// destination grids of the resampler geometry. All intermediate values stay
// in float64; rounding to nearest-even and saturation to the uint8 range
// happen only when each destination sample is written.
func (r *Resampler) Relocate(dst, src Plane) error {
	if err := src.validate(); err != nil {
		return err
	}
	if err := dst.validate(); err != nil {
		return err
	}
	if src.Grid != r.geo.Src || dst.Grid != r.geo.Dst {
		return ErrInvalidGeometry
	}

	outW, outH := r.geo.Dst.Width, r.geo.Dst.Height
	inW, inH := r.geo.Src.Width, r.geo.Src.Height
	srcStride, dstStride := src.Stride, dst.Stride

	// Horizontal pass: every source row contributes one float64 row of
	// outW samples, then the vertical pass consumes it immediately.
	row := make([]float64, outW)
	intermediate := make([][]float64, inH)
	for y := 0; y < inH; y++ {
		srcRow := src.Pix[y*srcStride : y*srcStride+inW]
		cur := make([]float64, outW)
		for x := 0; x < outW; x++ {
			acc := 0.0
			for _, t := range r.h.tables[x] {
				acc += t.weight * float64(srcRow[mirrorIndex(t.index, inW)])
			}
			cur[x] = acc
		}
		intermediate[y] = cur
	}

	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			acc := 0.0
			for _, t := range r.v.tables[y] {
				acc += t.weight * intermediate[mirrorIndex(t.index, inH)][x]
			}
			row[x] = acc
		}
		dstRow := dst.Pix[y*dstStride : y*dstStride+outW]
		for x := 0; x < outW; x++ {
			dstRow[x] = saturate8(roundEven(row[x]))
		}
	}
	return nil
}

// RelocateFloat performs the same separable reconstruction as Relocate but
// returns the high precision float64 samples without rounding or saturation.
// It is intended for analytic verification.
func (r *Resampler) RelocateFloat(src Plane) ([][]float64, error) {
	if err := src.validate(); err != nil {
		return nil, err
	}
	if src.Grid != r.geo.Src {
		return nil, ErrInvalidGeometry
	}
	outW, outH := r.geo.Dst.Width, r.geo.Dst.Height
	inW, inH := r.geo.Src.Width, r.geo.Src.Height

	intermediate := make([][]float64, inH)
	for y := 0; y < inH; y++ {
		srcRow := src.Pix[y*src.Stride : y*src.Stride+inW]
		cur := make([]float64, outW)
		for x := 0; x < outW; x++ {
			acc := 0.0
			for _, t := range r.h.tables[x] {
				acc += t.weight * float64(srcRow[mirrorIndex(t.index, inW)])
			}
			cur[x] = acc
		}
		intermediate[y] = cur
	}

	out := make([][]float64, outH)
	for y := 0; y < outH; y++ {
		cur := make([]float64, outW)
		for x := 0; x < outW; x++ {
			acc := 0.0
			for _, t := range r.v.tables[y] {
				acc += t.weight * intermediate[mirrorIndex(t.index, inH)][x]
			}
			cur[x] = acc
		}
		out[y] = cur
	}
	return out, nil
}

// Relocate is a one-shot convenience wrapper around New for a single plane.
func Relocate(dst, src Plane, geo Geometry, k Kernel) error {
	r, err := New(geo, k)
	if err != nil {
		return err
	}
	return r.Relocate(dst, src)
}

// RelocatePlanes relocates the Cb and Cr planes of planar YUV data against a
// common reference grid (normally the luma grid). Luma is not resampled here;
// each chroma plane carries its own explicit sampling center.
func RelocatePlanes(dstCb, srcCb, dstCr, srcCr Plane, geo Geometry, k Kernel) error {
	r, err := New(geo, k)
	if err != nil {
		return err
	}
	if err := r.Relocate(dstCb, srcCb); err != nil {
		return err
	}
	return r.Relocate(dstCr, srcCr)
}

// roundEven rounds to nearest integer with ties rounded to even (banker's
// rounding), matching IEEE-754 roundTiesToEven for the magnitudes involved.
func roundEven(x float64) float64 {
	// math.Round is half-away-from-zero; implement half-even explicitly.
	floor := math.Floor(x)
	frac := x - floor
	if frac < 0.5 {
		return floor
	}
	if frac > 0.5 {
		return floor + 1
	}
	if math.Mod(floor, 2) == 0 {
		return floor
	}
	return floor + 1
}

// saturate8 clamps an already rounded value to the uint8 sample range.
func saturate8(x float64) uint8 {
	switch {
	case x <= 0:
		return 0
	case x >= 255:
		return 255
	default:
		return uint8(x)
	}
}
