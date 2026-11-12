package taskpkg

import "math"

// DefaultMaxAnisotropy is the fallback major/minor axis ratio limit.
const DefaultMaxAnisotropy = 8.0

// hardMaxAnisotropy bounds iteration cost regardless of caller input.
const hardMaxAnisotropy = 64.0

// degenerateFootprint is the texel length below which a footprint is treated
// as a point and filtered with bilinear sampling.
const degenerateFootprint = 1e-8

// SampleParams carries a filter request. Dx and Dy are the screen-space
// derivatives of the texture coordinate vector (U, V), i.e. how (U, V)
// changes from the current pixel to its right and lower neighbors.
type SampleParams struct {
	U, V       float64
	DuDx, DvDx float64
	DuDy, DvDy float64
	// MaxAnisotropy clamps the footprint major/minor axis ratio. Values at or
	// below zero select DefaultMaxAnisotropy. The effective value never
	// exceeds hardMaxAnisotropy.
	MaxAnisotropy float64
}

// EllipseWeight implements the EWA radial profile. It returns zero outside
// the unit ellipse (r2 > 1) and exp(-2*r^2) - exp(-2) inside it, so the
// weight smoothly vanishes at the boundary.
func EllipseWeight(r2 float64) float64 {
	if r2 > 1.0 {
		return 0
	}
	return math.Exp(-2.0*r2) - math.Exp(-2.0)
}

// Sample evaluates the EWA filter at params on texture t. Colors are kept in
// linear, premultiplied-alpha space throughout the computation. A
// zero-volume or numerically degenerate footprint returns bilinear filtering
// of the base level instead.
func Sample(t *Texture, params SampleParams) ColorRGBA {
	if t == nil {
		panic("taskpkg: nil texture")
	}
	if !allFinite(params.U, params.V, params.DuDx, params.DvDx, params.DuDy, params.DvDy) {
		panic("taskpkg: non-finite sampling parameters")
	}

	if degenerate(params) {
		return t.bilinear(0, params.U*float64(t.width), params.V*float64(t.height))
	}

	maxAniso := params.MaxAnisotropy
	if maxAniso <= 0 || math.IsNaN(maxAniso) || math.IsInf(maxAniso, 0) {
		maxAniso = DefaultMaxAnisotropy
	}
	if maxAniso > hardMaxAnisotropy {
		maxAniso = hardMaxAnisotropy
	}

	// Jacobian of the mapping from screen pixels to level-zero texel space.
	// Texel centers sit at integer coordinates.
	j00 := params.DuDx * float64(t.width)
	j01 := params.DuDy * float64(t.width)
	j10 := params.DvDx * float64(t.height)
	j11 := params.DvDy * float64(t.height)

	minor := minorAxisLength(j00, j01, j10, j11)
	if minor < degenerateFootprint {
		return t.bilinear(0, params.U*float64(t.width), params.V*float64(t.height))
	}

	// Mip level chosen from the minor axis: at the selected level the minor
	// axis spans one to two texels.
	levelFloat := math.Log2(minor)
	level := int(math.Floor(levelFloat))
	if level < 0 {
		level = 0
	}
	if level > t.levels-1 {
		level = t.levels - 1
	}

	scale := math.Ldexp(1.0, -level)
	return sampleEllipse(t, level,
		params.U*float64(t.width)*scale,
		params.V*float64(t.height)*scale,
		j00*scale, j01*scale, j10*scale, j11*scale,
		maxAniso)
}

// sampleEllipse accumulates texels inside the footprint ellipse on one level.
// (centerU, centerV) and the Jacobian entries are expressed in that level's
// texel units.
func sampleEllipse(t *Texture, level int, centerU, centerV, j00, j01, j10, j11, maxAniso float64) ColorRGBA {
	gram00, gram01, gram11 := eigenGram(j00, j01, j10, j11)
	g00, g01, g11 := clampGram(gram00, gram01, gram11, maxAniso)

	det := g00*g11 - g01*g01
	if det <= 0 || !allFinite(g00, g01, g11) {
		return t.bilinear(level, centerU, centerV)
	}

	// Q = G^-1; the ellipse satisfies r^2 = d^T Q d <= 1.
	invDet := 1.0 / det
	q00 := g11 * invDet
	q01 := -g01 * invDet
	q11 := g00 * invDet

	// Axis-aligned bounding box of the ellipse in texel units. Since Q = G^-1,
	// the extremal |d_u| over d^T Q d <= 1 equals sqrt(G_00), and likewise
	// sqrt(G_11) for d_v.
	radiusU := math.Sqrt(math.Max(0, g00))
	radiusV := math.Sqrt(math.Max(0, g11))

	minX := int(math.Floor(centerU - radiusU))
	maxX := int(math.Floor(centerU + radiusU))
	minY := int(math.Floor(centerV - radiusV))
	maxY := int(math.Floor(centerV + radiusV))

	var accR, accG, accB, accA, weightSum float64
	for y := minY; y <= maxY; y++ {
		dy := float64(y) - centerV
		for x := minX; x <= maxX; x++ {
			dx := float64(x) - centerU
			r2 := q00*dx*dx + 2.0*q01*dx*dy + q11*dy*dy
			weight := EllipseWeight(r2)
			if weight <= 0 {
				continue
			}
			c := t.texelAt(level, x, y)
			accR += c.R * weight
			accG += c.G * weight
			accB += c.B * weight
			accA += c.A * weight
			weightSum += weight
		}
	}

	if weightSum <= 0 {
		return t.bilinear(level, centerU, centerV)
	}

	invWeight := 1.0 / weightSum
	return ColorRGBA{
		R: accR * invWeight,
		G: accG * invWeight,
		B: accB * invWeight,
		A: accA * invWeight,
	}
}

// eigenGram returns the symmetric Gram matrix J J^T whose eigenvalues are the
// squared principal axis lengths of the footprint.
func eigenGram(j00, j01, j10, j11 float64) (g00, g01, g11 float64) {
	g00 = j00*j00 + j01*j01
	g01 = j00*j10 + j01*j11
	g11 = j10*j10 + j11*j11
	return g00, g01, g11
}

// eigenLengths2x2 returns the minor and major axis lengths (square roots of
// the Gram matrix eigenvalues).
func eigenLengths2x2(j00, j01, j10, j11 float64) (minor, major float64) {
	g00, g01, g11 := eigenGram(j00, j01, j10, j11)
	halfTrace := 0.5 * (g00 + g11)
	discriminant := math.Sqrt(math.Max(0, halfTrace*halfTrace-(g00*g11-g01*g01)))
	minor = math.Sqrt(math.Max(0, halfTrace-discriminant))
	major = math.Sqrt(math.Max(0, halfTrace+discriminant))
	return minor, major
}

// minorAxisLength returns only the minor axis length of the footprint.
func minorAxisLength(j00, j01, j10, j11 float64) float64 {
	minor, _ := eigenLengths2x2(j00, j01, j10, j11)
	return minor
}

// clampGram limits the major/minor axis ratio of the ellipse described by the
// Gram matrix to maxAniso by inflating the minor eigenvalue. The eigenvectors
// of a symmetric 2x2 matrix are recovered from the off-diagonal entry.
func clampGram(g00, g01, g11, maxAniso float64) (out00, out01, out11 float64) {
	halfTrace := 0.5 * (g00 + g11)
	discriminant := math.Sqrt(math.Max(0, halfTrace*halfTrace-(g00*g11-g01*g01)))
	lambdaMin := math.Max(0, halfTrace-discriminant)
	lambdaMax := math.Max(0, halfTrace+discriminant)

	floor := lambdaMax / (maxAniso * maxAniso)
	if lambdaMin < floor {
		lambdaMin = floor
	}

	if math.Abs(g01) < 1e-20 {
		if g00 >= g11 {
			return lambdaMax, 0, lambdaMin
		}
		return lambdaMin, 0, lambdaMax
	}

	// Eigenvector of lambdaMax: (g01, lambdaMax-g00), normalized.
	vx := g01
	vy := lambdaMax - g00
	norm := math.Hypot(vx, vy)
	if norm == 0 {
		return g00, g01, g11
	}
	vx /= norm
	vy /= norm

	out00 = lambdaMax*vx*vx + lambdaMin*vy*vy
	out01 = (lambdaMax - lambdaMin) * vx * vy
	out11 = lambdaMax*vy*vy + lambdaMin*vx*vx
	return out00, out01, out11
}

func degenerate(p SampleParams) bool {
	det := p.DuDx*p.DvDy - p.DuDy*p.DvDx
	return math.Abs(det) < degenerateFootprint*degenerateFootprint
}

func allFinite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}
