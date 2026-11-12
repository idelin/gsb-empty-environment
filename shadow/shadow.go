// Package shadow plans cascaded shadow frusta for a directional light.
//
// The planner is deterministic and frame-coherent: the light rotation depends
// only on the light direction (a "fixed" light space), while each cascade is
// fitted with a bounding sphere derived from the mean of its frustum corners.
// The sphere center is quantized to shadow-map texel units so that sub-pixel
// camera motion cannot make the cascade bounds shimmer.
package shadow

import (
	"errors"
	"math"
)

var (
	// ErrZeroLightVector is returned when LightDirection has zero length.
	ErrZeroLightVector = errors.New("shadow: zero-length light direction vector")
	// ErrInvalidSplit is returned for malformed cascade split fractions.
	ErrInvalidSplit = errors.New("shadow: splits must be strictly increasing fractions in (0,1] ending at 1")
	// ErrInvalidConfig is returned for non-finite or non-positive configuration.
	ErrInvalidConfig = errors.New("shadow: invalid configuration value")
	// ErrInvalidCasterRange is returned when a caster range is inverted or non-finite.
	ErrInvalidCasterRange = errors.New("shadow: caster range Min must be <= Max")
	// ErrSingularMatrix is returned when a supplied matrix cannot be inverted.
	ErrSingularMatrix = errors.New("shadow: singular camera matrix")
)

// CasterRange is a depth interval measured along the light axis in light
// space, expressed in world units as the range of dot(position, lightDir).
// Under the right-handed light rotation this equals -viewZ, so the planner
// folds the interval into the orthographic near/far span. Every shadow
// frustum is extended to include this interval.
type CasterRange struct {
	Min float64
	Max float64
}

// Config is the input to Plan.
type Config struct {
	// Projection is the camera projection matrix (NDC x,y in [-1,1]).
	Projection Mat4
	// View is the camera world-to-view matrix.
	View Mat4
	// LightDirection points from the light source toward the scene. A
	// zero-length vector is rejected with ErrZeroLightVector.
	LightDirection Vec3
	// Splits are cumulative normalized depth fractions of the camera depth
	// range, strictly increasing, each in (0,1], with the final entry equal to
	// 1. Cascade i spans Splits[i-1]..Splits[i] (with an implicit 0 prefix).
	Splits []float64
	// ShadowMapSize is the edge length in pixels of the (square) shadow map.
	ShadowMapSize float64
	// ReverseZ selects reverse-depth conventions for both corner unprojection
	// and the generated light projection.
	ReverseZ bool
	// CasterRange, when non-nil, extends every cascade's depth span.
	CasterRange *CasterRange
	// UpHint orients the light camera. Defaults to world +Y when zero.
	UpHint Vec3
}

// Cascade describes one fitted shadow frustum.
type Cascade struct {
	// Index is the zero-based cascade number.
	Index int
	// SplitNear and SplitFar are the normalized depth fractions of this slice.
	SplitNear float64
	SplitFar  float64
	// FrustumCorners are the eight world-space corners of this camera slice.
	FrustumCorners [8]Vec3
	// SphereCenter is the mean of FrustumCorners in world space.
	SphereCenter Vec3
	// Radius is the maximum distance from SphereCenter to any corner.
	Radius float64
	// TexelWorld is one shadow-map texel expressed in world units.
	TexelWorld float64
	// HalfExtent is the half width/height of the fitted orthographic box in
	// world units (Radius plus a one-texel safety margin).
	HalfExtent float64
	// SnappedCenter is the quantized sphere center in (rotation-only) light
	// space. X and Y are snapped to texel units; Z is left untouched.
	SnappedCenter Vec3
	// Eye is the light camera position in world space.
	Eye Vec3
	// MinZ and MaxZ are the view-space z limits (negative in front of the
	// light) including CasterRange.
	MinZ float64
	MaxZ float64
	// LightView is the per-cascade world-to-light view matrix. Its rotation is
	// shared across cascades; only the translation differs.
	LightView Mat4
	// LightProjection is the fitted off-center orthographic projection.
	LightProjection Mat4
	// LightViewProjection is LightProjection multiplied by LightView.
	LightViewProjection Mat4
}

// Plan is the planner output.
type Result struct {
	// LightRotation is the fixed, translation-free rotation into light space,
	// shared by every cascade and stable while LightDirection is unchanged.
	LightRotation Mat4
	// Cascades are ordered from nearest to farthest.
	Cascades []Cascade
}

// Plan fits one shadow frustum per depth split and returns the result.
func Plan(cfg Config) (*Result, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}

	lightDir, err := normalizeLight(cfg.LightDirection)
	if err != nil {
		return nil, err
	}
	up := cfg.UpHint
	if up == (Vec3{}) {
		up = Vec3{Y: 1}
	}
	upN, ok := normalize(up)
	if !ok {
		upN = Vec3{Z: 1}
	}
	if math.Abs(lightDir.dot(upN)) > 1-1e-9 {
		// Parallel to the up hint: fall back to a stable alternative axis.
		if math.Abs(lightDir.Y) > 1-1e-9 {
			upN = Vec3{Z: 1}
		} else {
			upN = Vec3{Y: 1}
		}
	}

	// Fixed light rotation (eye at the origin): identical for all cascades and
	// frames as long as LightDirection does not change.
	lightRotation := LookAt(Vec3{}, lightDir, upN)

	viewProj := mul(cfg.Projection, cfg.View)
	invVP, err := invert(viewProj)
	if err != nil {
		return nil, err
	}

	out := &Result{
		LightRotation: lightRotation,
		Cascades:      make([]Cascade, 0, len(cfg.Splits)),
	}

	prev := 0.0
	for i, split := range cfg.Splits {
		cas, err := fitCascade(cfg, lightRotation, invVP, i, prev, split)
		if err != nil {
			return nil, err
		}
		out.Cascades = append(out.Cascades, cas)
		prev = split
	}
	return out, nil
}

func validate(cfg Config) error {
	if cfg.ShadowMapSize <= 0 || math.IsNaN(cfg.ShadowMapSize) || math.IsInf(cfg.ShadowMapSize, 0) {
		return ErrInvalidConfig
	}
	if len(cfg.Splits) == 0 {
		return ErrInvalidSplit
	}
	prev := 0.0
	for i, s := range cfg.Splits {
		if math.IsNaN(s) || math.IsInf(s, 0) || s <= 0 || s > 1 || s <= prev {
			return ErrInvalidSplit
		}
		if i == len(cfg.Splits)-1 && math.Abs(s-1) > 1e-12 {
			return ErrInvalidSplit
		}
		prev = s
	}
	if cfg.CasterRange != nil {
		r := cfg.CasterRange
		if math.IsNaN(r.Min) || math.IsNaN(r.Max) || math.IsInf(r.Min, 0) ||
			math.IsInf(r.Max, 0) || r.Min > r.Max {
			return ErrInvalidCasterRange
		}
	}
	return nil
}

func fitCascade(cfg Config, lightRotation, invVP Mat4,
	index int, splitNear, splitFar float64,
) (Cascade, error) {
	var cas Cascade
	cas.Index = index
	cas.SplitNear = splitNear
	cas.SplitFar = splitFar

	// NDC depth at each split plane. Forward Z maps near->0, far->1; reverse Z
	// maps near->1, far->0.
	zNear := splitNear
	zFar := splitFar
	if cfg.ReverseZ {
		zNear = 1 - splitNear
		zFar = 1 - splitFar
	}

	nds := [8][3]float64{
		{-1, -1, zNear}, {1, -1, zNear}, {1, 1, zNear}, {-1, 1, zNear},
		{-1, -1, zFar}, {1, -1, zFar}, {1, 1, zFar}, {-1, 1, zFar},
	}
	var center Vec3
	for i, nd := range nds {
		p := transformPoint(invVP, Vec3{nd[0], nd[1], nd[2]})
		cas.FrustumCorners[i] = p
		center = center.add(p)
	}
	center = center.scale(1 / float64(len(nds)))
	cas.SphereCenter = center

	radius := 0.0
	for _, p := range cas.FrustumCorners {
		if d := p.sub(center).length(); d > radius {
			radius = d
		}
	}
	cas.Radius = radius

	// Project into the fixed light space. Rotation preserves distances, so the
	// radius is unchanged; only coordinates rotate.
	centerL := transformPoint(lightRotation, center)
	// View-space z is negative in front of the light camera.
	minViewZ := math.Inf(1)
	maxViewZ := math.Inf(-1)
	for _, p := range cas.FrustumCorners {
		pl := transformPoint(lightRotation, p)
		minViewZ = math.Min(minViewZ, pl.Z)
		maxViewZ = math.Max(maxViewZ, pl.Z)
	}
	if cfg.CasterRange != nil {
		// Caster ranges are expressed as dot(position, lightDir), which equals
		// -viewZ under the right-handed light rotation.
		minViewZ = math.Min(minViewZ, -cfg.CasterRange.Max)
		maxViewZ = math.Max(maxViewZ, -cfg.CasterRange.Min)
	}
	cas.MinZ, cas.MaxZ = minViewZ, maxViewZ

	// Texel-sized quantization of the center in light space.
	texel := (2 * radius) / cfg.ShadowMapSize
	cas.TexelWorld = texel
	snapped := Vec3{
		X: snap(centerL.X, texel),
		Y: snap(centerL.Y, texel),
		Z: centerL.Z,
	}
	cas.SnappedCenter = snapped

	// One texel of padding guarantees every corner stays inside the box even in
	// the worst case: snapping can move the center by up to half a texel on each
	// axis, whose combined length is at most texel/sqrt(2) < one texel.
	half := radius + texel
	cas.HalfExtent = half

	// The light view is the fixed rotation followed by a translation that
	// recenters the origin on the snapped sphere center in light space xy.
	// Depth is left untranslated so measured view-z values stay valid for the
	// orthographic near and far planes.
	cas.LightView = lightViewAt(lightRotation, Vec3{X: snapped.X, Y: snapped.Y})

	// World-space eye (light camera position), derived from the orthonormal
	// rotation; exposed for host renderers that need a concrete camera point.
	// The z coordinate of the eye is the sphere center depth.
	cas.Eye = transformPoint(transpose(lightRotation), Vec3{
		X: snapped.X,
		Y: snapped.Y,
		Z: centerL.Z,
	})

	// The view already recenters xy on the snapped center, so the orthographic
	// box is centered at zero. Depth is untranslated: convert the negative
	// view-z extents into positive distances along the view axis.
	nearDist := -maxViewZ // closest to the light
	farDist := -minViewZ
	if cfg.ReverseZ {
		cas.LightProjection = orthoReverseZ(-half, half, -half, half, nearDist, farDist)
	} else {
		cas.LightProjection = ortho(-half, half, -half, half, nearDist, farDist)
	}
	cas.LightViewProjection = mul(cas.LightProjection, cas.LightView)
	return cas, nil
}

func snap(v, step float64) float64 {
	return math.Round(v/step) * step
}

func normalizeLight(v Vec3) (Vec3, error) {
	n, ok := normalize(v)
	if !ok {
		return Vec3{}, ErrZeroLightVector
	}
	return n, nil
}

func normalize(v Vec3) (Vec3, bool) {
	l := v.length()
	if l == 0 || math.IsNaN(l) || math.IsInf(l, 0) {
		return Vec3{}, false
	}
	return v.scale(1 / l), true
}

func transpose(m Mat4) Mat4 {
	var t Mat4
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			t[r][c] = m[c][r]
		}
	}
	return t
}

// lightViewAt rebuilds a light view with the exact fixed rotation followed by
// a translation that subtracts center (the snapped light-space center).
func lightViewAt(rotation Mat4, center Vec3) Mat4 {
	var t Mat4
	t[0][0], t[1][1], t[2][2], t[3][3] = 1, 1, 1, 1
	t[0][3] = -center.X
	t[1][3] = -center.Y
	t[2][3] = -center.Z
	return mul(t, rotation)
}
