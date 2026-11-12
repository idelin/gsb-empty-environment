package shadow

import (
	"math"
	"math/rand"
	"testing"
)

const (
	testNear = 0.1
	testFar  = 200.0
	testSize = 2048.0
	eps      = 1e-9
)

func testConfig(eye Vec3, yaw, pitch, aspect float64, reverseZ bool) Config {
	proj := Perspective(math.Pi/4, aspect, testNear, testFar)
	if reverseZ {
		proj = PerspectiveReverseZ(math.Pi/4, aspect, testNear, testFar)
	}
	dir := yawPitchDir(yaw, pitch)
	view := LookAt(eye, dir, Vec3{Y: 1})
	return Config{
		Projection:     proj,
		View:           view,
		LightDirection: Vec3{X: -0.5, Y: -1, Z: -0.3},
		Splits:         []float64{0.12, 0.35, 1.0},
		ShadowMapSize:  testSize,
		ReverseZ:       reverseZ,
	}
}

func yawPitchDir(yaw, pitch float64) Vec3 {
	return Vec3{
		X: math.Cos(pitch) * math.Sin(yaw),
		Y: math.Sin(pitch),
		Z: math.Cos(pitch) * math.Cos(yaw),
	}
}

func dist(a, b Vec3) float64 { return a.sub(b).length() }

func mustNormalize(v Vec3) Vec3 {
	n, _ := normalize(v)
	return n
}

// assertCoverage verifies that every frustum corner lands inside the
// cascade's NDC box, including the configured depth span.
func assertCoverage(t *testing.T, res *Result) {
	t.Helper()
	for _, cas := range res.Cascades {
		for _, corner := range cas.FrustumCorners {
			ndc := transformPoint(cas.LightViewProjection, corner)
			if ndc.X < -1-eps || ndc.X > 1+eps || ndc.Y < -1-eps || ndc.Y > 1+eps {
				t.Fatalf("cascade %d corner %v outside xy NDC: %v", cas.Index, corner, ndc)
			}
			if ndc.Z < -eps || ndc.Z > 1+eps {
				t.Fatalf("cascade %d corner %v outside z NDC: %v", cas.Index, corner, ndc)
			}
		}
	}
}

func TestRejectsZeroLight(t *testing.T) {
	cfg := testConfig(Vec3{}, 0, 0, 1.7, false)
	cfg.LightDirection = Vec3{}
	if _, err := Plan(cfg); err != ErrZeroLightVector {
		t.Fatalf("want ErrZeroLightVector, got %v", err)
	}
}

func TestRejectsBadSplits(t *testing.T) {
	cases := [][]float64{
		nil,
		{0.5, 0.4, 1.0},
		{0.0, 0.5, 1.0},
		{0.2, 0.9},
		{0.2, 1.1},
		{-0.1, 1.0},
	}
	for _, splits := range cases {
		cfg := testConfig(Vec3{}, 0, 0, 1.7, false)
		cfg.Splits = splits
		if _, err := Plan(cfg); err != ErrInvalidSplit {
			t.Fatalf("splits %v: want ErrInvalidSplit, got %v", splits, err)
		}
	}
}

func TestRejectsBadCasterRange(t *testing.T) {
	cfg := testConfig(Vec3{}, 0, 0, 1.7, false)
	cfg.CasterRange = &CasterRange{Min: 5, Max: -5}
	if _, err := Plan(cfg); err != ErrInvalidCasterRange {
		t.Fatalf("want ErrInvalidCasterRange, got %v", err)
	}
}

func TestBasicCoverageAndSphere(t *testing.T) {
	cfg := testConfig(Vec3{Z: 5}, 0.3, 0.1, 1.7, false)
	res, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cascades) != 3 {
		t.Fatalf("want 3 cascades, got %d", len(res.Cascades))
	}
	assertCoverage(t, res)

	for _, cas := range res.Cascades {
		var mean Vec3
		for _, p := range cas.FrustumCorners {
			mean = mean.add(p)
		}
		mean = mean.scale(1.0 / 8.0)
		if dist(mean, cas.SphereCenter) > eps {
			t.Fatalf("cascade %d center not at corner mean", cas.Index)
		}
		maxD := 0.0
		for _, p := range cas.FrustumCorners {
			maxD = math.Max(maxD, dist(p, cas.SphereCenter))
		}
		if math.Abs(maxD-cas.Radius) > eps {
			t.Fatalf("cascade %d radius mismatch", cas.Index)
		}
	}
}

func TestSubpixelTranslationStable(t *testing.T) {
	base := testConfig(Vec3{}, 0, 0, 1.7, false)
	res0, err := Plan(base)
	if err != nil {
		t.Fatal(err)
	}

	offsets := []Vec3{
		{X: 1e-3}, {X: -2e-4}, {Y: 3e-4}, {Z: 7e-4},
		{X: 1e-4, Y: -2e-4, Z: 3e-4},
	}
	for _, off := range offsets {
		cfg := testConfig(off, 0, 0, 1.7, false)
		res, err := Plan(cfg)
		if err != nil {
			t.Fatal(err)
		}
		assertCoverage(t, res)
		for i := range res.Cascades {
			a, b := res0.Cascades[i], res.Cascades[i]
			if math.Abs(a.TexelWorld-b.TexelWorld) > eps {
				t.Fatalf("cascade %d texel drifted: %.12f vs %.12f", i, a.TexelWorld, b.TexelWorld)
			}
			if math.Abs(a.HalfExtent-b.HalfExtent) > eps {
				t.Fatalf("cascade %d half extent drifted: %.12f vs %.12f", i, a.HalfExtent, b.HalfExtent)
			}
			wantWidth := a.TexelWorld*testSize + 2*a.TexelWorld
			if math.Abs(2*a.HalfExtent-wantWidth) > a.TexelWorld*1e-9 {
				t.Fatalf("cascade %d texel units inconsistent across quantization", i)
			}
			if rem := math.Mod(math.Abs(b.SnappedCenter.X), b.TexelWorld); rem > eps && math.Abs(rem-b.TexelWorld) > eps {
				t.Fatalf("cascade %d snapped X not texel-aligned: %v", i, b.SnappedCenter)
			}
			if rem := math.Mod(math.Abs(b.SnappedCenter.Y), b.TexelWorld); rem > eps && math.Abs(rem-b.TexelWorld) > eps {
				t.Fatalf("cascade %d snapped Y not texel-aligned: %v", i, b.SnappedCenter)
			}
		}
	}
}

func TestRotationCoverage(t *testing.T) {
	yaws := []float64{0, 0.001, 0.7, 1.3, 2.5, 3.9}
	pitches := []float64{-0.6, -0.05, 0.05, 0.9}
	for _, yaw := range yaws {
		for _, pitch := range pitches {
			cfg := testConfig(Vec3{X: 2, Y: 1, Z: -3}, yaw, pitch, 1.7, false)
			res, err := Plan(cfg)
			if err != nil {
				t.Fatal(err)
			}
			assertCoverage(t, res)
		}
	}
}

func TestExtremeAspectRatios(t *testing.T) {
	aspects := []float64{0.05, 0.2, 5.0, 32.0, 200.0}
	for _, aspect := range aspects {
		cfg := testConfig(Vec3{}, 0.4, 0.05, aspect, false)
		res, err := Plan(cfg)
		if err != nil {
			t.Fatal(err)
		}
		assertCoverage(t, res)
		for _, cas := range res.Cascades {
			if cas.Radius <= 0 || cas.HalfExtent <= 0 || cas.TexelWorld <= 0 {
				t.Fatalf("aspect %v produced non-positive fit: %+v", aspect, cas)
			}
		}
	}
}

func TestCasterRangeExtendsDepth(t *testing.T) {
	cfg := testConfig(Vec3{}, 0, 0, 1.7, false)
	res, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}

	lightDir := mustNormalize(cfg.LightDirection)
	rot := LookAt(Vec3{}, lightDir, Vec3{Y: 1})
	minProj := math.Inf(1) // dot(position, lightDir)
	maxProj := math.Inf(-1)
	for _, cas := range res.Cascades {
		for _, p := range cas.FrustumCorners {
			pl := transformPoint(rot, p)
			minProj = math.Min(minProj, -pl.Z)
			maxProj = math.Max(maxProj, -pl.Z)
		}
	}
	cfg.CasterRange = &CasterRange{Min: minProj - 30, Max: maxProj + 20}
	res2, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, cas := range res2.Cascades {
		// MinZ/MaxZ are view-z (negative), caster projection is -viewZ.
		if -cas.MaxZ < minProj-30-eps || -cas.MinZ > maxProj+20+eps {
			t.Fatalf("caster range not included: viewZ [%v,%v]", cas.MinZ, cas.MaxZ)
		}
		base := res.Cascades[cas.Index]
		if math.Abs(base.HalfExtent-cas.HalfExtent) > eps {
			t.Fatalf("xy fit changed after caster extension")
		}
	}
}

func TestReverseZ(t *testing.T) {
	cfg := testConfig(Vec3{Z: 4}, 0.6, -0.2, 1.7, true)
	res, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	assertCoverage(t, res)

	for _, cas := range res.Cascades {
		maxNDC, minNDC := math.Inf(-1), math.Inf(1)
		for _, corner := range cas.FrustumCorners {
			z := transformPoint(cas.LightViewProjection, corner).Z
			maxNDC = math.Max(maxNDC, z)
			minNDC = math.Min(minNDC, z)
		}
		// Reverse Z: the closest geometry maps to ~1 and the farthest to ~0.
		if maxNDC < 0.999 || minNDC > 0.001 {
			t.Fatalf("reverse-Z depth range wrong: [%.4f, %.4f]", minNDC, maxNDC)
		}
	}
}

func TestMatrixRoundTrip(t *testing.T) {
	for _, reverseZ := range []bool{false, true} {
		cfg := testConfig(Vec3{X: 1, Y: 2, Z: 3}, 0.8, 0.3, 1.7, reverseZ)
		res, err := Plan(cfg)
		if err != nil {
			t.Fatal(err)
		}
		invVP, err := invert(mul(cfg.Projection, cfg.View))
		if err != nil {
			t.Fatal(err)
		}
		for _, cas := range res.Cascades {
			invLVP, err := invert(cas.LightViewProjection)
			if err != nil {
				t.Fatal(err)
			}
			for _, corner := range cas.FrustumCorners {
				ndc := transformPoint(cas.LightViewProjection, corner)
				back := transformPoint(invLVP, ndc)
				if dist(back, corner) > 1e-8 {
					t.Fatalf("LVP roundtrip error %v (reverseZ=%v)", dist(back, corner), reverseZ)
				}
				ndcCam := transformPoint(mul(cfg.Projection, cfg.View), corner)
				backCam := transformPoint(invVP, ndcCam)
				if dist(backCam, corner) > 1e-8 {
					t.Fatalf("camera VP roundtrip error %v", dist(backCam, corner))
				}
			}
		}
	}
}

func TestFixedLightRotationAcrossCascades(t *testing.T) {
	cfg := testConfig(Vec3{}, 0, 0, 1.7, false)
	res, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := res.Cascades[0].LightView
	for i := 1; i < len(res.Cascades); i++ {
		for r := 0; r < 3; r++ {
			for c := 0; c < 3; c++ {
				if math.Abs(base[r][c]-res.Cascades[i].LightView[r][c]) > eps {
					t.Fatalf("cascade %d light rotation differs", i)
				}
			}
		}
	}
}

func TestInverseAndMul(t *testing.T) {
	m := Perspective(math.Pi/3, 1.3, 0.25, 120)
	inv, err := invert(m)
	if err != nil {
		t.Fatal(err)
	}
	prod := mul(m, inv)
	id := identity()
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			if math.Abs(prod[r][c]-id[r][c]) > 1e-10 {
				t.Fatalf("inverse product off at [%d][%d]: %v", r, c, prod[r][c])
			}
		}
	}
	if _, err := invert(Mat4{}); err != ErrSingularMatrix {
		t.Fatalf("want ErrSingularMatrix, got %v", err)
	}
}

func TestOrthoDepthConventions(t *testing.T) {
	const n, f = 2.0, 10.0
	fwd := ortho(-1, 1, -1, 1, n, f)
	if got := transformPoint(fwd, Vec3{Z: -n}).Z; math.Abs(got) > eps {
		t.Fatalf("forward near maps to %v, want 0", got)
	}
	if got := transformPoint(fwd, Vec3{Z: -f}).Z; math.Abs(got-1) > eps {
		t.Fatalf("forward far maps to %v, want 1", got)
	}
	rev := orthoReverseZ(-1, 1, -1, 1, n, f)
	if got := transformPoint(rev, Vec3{Z: -n}).Z; math.Abs(got-1) > eps {
		t.Fatalf("reverse near maps to %v, want 1", got)
	}
	if got := transformPoint(rev, Vec3{Z: -f}).Z; math.Abs(got) > eps {
		t.Fatalf("reverse far maps to %v, want 0", got)
	}
	// XY extents map to the full NDC range in both variants.
	for _, m := range []Mat4{fwd, rev} {
		if got := transformPoint(m, Vec3{X: 1, Y: 1, Z: -n}); math.Abs(got.X-1) > eps || math.Abs(got.Y-1) > eps {
			t.Fatalf("xy corner not mapped to NDC (1,1): %v", got)
		}
	}
}

func TestLookAtRotationOrthonormal(t *testing.T) {
	v := LookAt(Vec3{X: 1, Y: 2, Z: 3}, Vec3{X: 0.3, Y: -1, Z: 0.2}, Vec3{Y: 1})
	for r := 0; r < 3; r++ {
		row := Vec3{v[r][0], v[r][1], v[r][2]}
		if math.Abs(row.length()-1) > 1e-12 {
			t.Fatalf("rotation row %d not unit: %v", r, row.length())
		}
		for c := r + 1; c < 3; c++ {
			other := Vec3{v[c][0], v[c][1], v[c][2]}
			if math.Abs(row.dot(other)) > 1e-12 {
				t.Fatalf("rotation rows %d,%d not orthogonal", r, c)
			}
		}
	}
	// A point straight ahead of the camera must land at negative view z.
	eye := Vec3{X: 5}
	dir := Vec3{Z: -1}
	view := LookAt(eye, dir, Vec3{Y: 1, Z: 0.3})
	ahead := transformPoint(view, eye.add(dir))
	if ahead.Z >= 0 {
		t.Fatalf("point ahead has view z %v, want negative", ahead.Z)
	}
}

func TestLightRotationStableAcrossFrames(t *testing.T) {
	cfg := testConfig(Vec3{}, 0, 0, 1.7, false)
	first, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, move := range []Vec3{{X: 0.013}, {Y: -0.027}, {Z: 0.041}} {
		c := testConfig(move, 0, 0, 1.7, false)
		got, err := Plan(c)
		if err != nil {
			t.Fatal(err)
		}
		for r := 0; r < 4; r++ {
			for col := 0; col < 4; col++ {
				if math.Abs(first.LightRotation[r][col]-got.LightRotation[r][col]) > eps {
					t.Fatalf("light rotation changed under camera translation at [%d][%d]", r, col)
				}
			}
		}
	}
}

func TestTexelSizeMonotonicWithMapSize(t *testing.T) {
	cfg := testConfig(Vec3{}, 0, 0, 1.7, false)
	sizes := []float64{512, 1024, 2048, 4096}
	prevTexel := map[int]float64{}
	for _, size := range sizes {
		cfg.ShadowMapSize = size
		res, err := Plan(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, cas := range res.Cascades {
			if old, ok := prevTexel[cas.Index]; ok && cas.TexelWorld >= old {
				t.Fatalf("cascade %d texel did not shrink with larger map", cas.Index)
			}
			prevTexel[cas.Index] = cas.TexelWorld
		}
	}
}

func TestFuzzRandomConfigurations(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 300; iter++ {
		reverseZ := rng.Intn(2) == 0
		aspect := math.Exp(rng.NormFloat64() * 1.5) // spans very narrow to wide
		eye := Vec3{
			X: rng.NormFloat64() * 20,
			Y: rng.NormFloat64() * 20,
			Z: rng.NormFloat64() * 20,
		}
		cfg := testConfig(eye, rng.Float64()*2*math.Pi,
			(rng.Float64()-0.5)*1.4, aspect, reverseZ)
		// A few distinct light directions, including near-axis ones.
		cfg.LightDirection = Vec3{
			X: rng.NormFloat64(),
			Y: rng.NormFloat64(),
			Z: rng.NormFloat64(),
		}
		n := 1 + rng.Intn(4)
		splits := make([]float64, n)
		cur := 0.0
		for i := range splits {
			cur += (1 - cur) * (0.25 + rng.Float64()*0.5)
			splits[i] = cur
		}
		splits[n-1] = 1.0
		cfg.Splits = splits

		res, err := Plan(cfg)
		if err != nil {
			t.Fatalf("iter %d: unexpected error %v", iter, err)
		}
		assertCoverage(t, res)
		for _, cas := range res.Cascades {
			if cas.TexelWorld != (2*cas.Radius)/testSize {
				t.Fatalf("iter %d cascade %d texel formula mismatch", iter, cas.Index)
			}
			// Snapped center must stay within half a texel of the raw center.
			raw := transformPoint(res.LightRotation, cas.SphereCenter)
			dx := cas.SnappedCenter.X - raw.X
			dy := cas.SnappedCenter.Y - raw.Y
			if math.Abs(dx) > cas.TexelWorld/2+eps || math.Abs(dy) > cas.TexelWorld/2+eps {
				t.Fatalf("iter %d cascade %d snap moved too far", iter, cas.Index)
			}
		}
	}
}

func TestZeroNearSplitNotRequired(t *testing.T) {
	// A single full-range cascade must be accepted.
	cfg := testConfig(Vec3{}, 0, 0, 1.7, false)
	cfg.Splits = []float64{1.0}
	res, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cascades) != 1 {
		t.Fatalf("want 1 cascade, got %d", len(res.Cascades))
	}
	assertCoverage(t, res)
}

func TestSnappingEliminatesSubpixelJitter(t *testing.T) {
	cfg := testConfig(Vec3{}, 0, 0, 1.7, false)
	res0, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Compare raw (unquantized) drift against snapped drift under purely
	// sub-pixel camera translation. Snapping can only hold a cell or jump by a
	// whole texel, never produce sub-texel shimmer.
	for step := 0; step < 40; step++ {
		shift := float64(step) * 3e-5
		c := testConfig(Vec3{X: shift, Y: -shift * 0.5}, 0, 0, 1.7, false)
		res, err := Plan(c)
		if err != nil {
			t.Fatal(err)
		}
		for i, cas := range res.Cascades {
			raw := transformPoint(res.LightRotation, cas.SphereCenter)
			snap0 := res0.Cascades[i].SnappedCenter
			// The raw center must remain inside one texel cell of its snap.
			if d := math.Abs(raw.X - cas.SnappedCenter.X); d > cas.TexelWorld/2+eps {
				t.Fatalf("step %d cascade %d raw center escaped texel cell", step, i)
			}
			// Snapped movement, when it happens, must be an exact integer
			// number of texels (no sub-texel residual = no shimmer).
			dx := cas.SnappedCenter.X - snap0.X
			dy := cas.SnappedCenter.Y - snap0.Y
			if math.Abs(math.Mod(dx+cas.TexelWorld*1e6, cas.TexelWorld)) > eps &&
				math.Abs(math.Abs(math.Mod(dx+cas.TexelWorld*1e6, cas.TexelWorld))-cas.TexelWorld) > eps {
				t.Fatalf("step %d cascade %d non-texel-quantized x jump %v", step, i, dx)
			}
			if math.Abs(math.Mod(dy+cas.TexelWorld*1e6, cas.TexelWorld)) > eps &&
				math.Abs(math.Abs(math.Mod(dy+cas.TexelWorld*1e6, cas.TexelWorld))-cas.TexelWorld) > eps {
				t.Fatalf("step %d cascade %d non-texel-quantized y jump %v", step, i, dy)
			}
			// The raw center moves continuously while the snapped center only
			// ever rests on grid points, so continuous sub-texel shimmer cannot
			// occur. The integer-texel assertions above already enforce this.
		}
	}
}
