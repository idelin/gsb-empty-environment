package taskpkg

import (
	"errors"
	"math"
	"testing"
)

var (
	normalFront = Vec3{0, 0, 1}
	red         = RGBA{1, 0, 0, 1}
	green       = RGBA{0, 1, 0, 1}
	blue        = RGBA{0, 0, 1, 1}
)

func mustOrtho(t *testing.T, w, h int, near, far float64) Camera {
	t.Helper()
	cam, err := NewOrthographicCamera(w, h, near, far)
	if err != nil {
		t.Fatalf("NewOrthographicCamera: %v", err)
	}
	return cam
}

func newFrame(w, h int, cam Camera) Frame {
	n := w * h
	return Frame{
		Width:       w,
		Height:      h,
		Camera:      cam,
		Depth:       make([]float64, n),
		Color:       make([]RGBA, n),
		Normals:     make([]Vec3, n),
		ObjectIDs:   make([]ObjectID, n),
		Generations: make([]uint32, n),
	}
}

func fillFrame(f Frame, id ObjectID, gen uint32, depth float64, color RGBA, normal Vec3) {
	for i := range f.Color {
		f.Color[i] = color
		f.Depth[i] = depth
		f.Normals[i] = normal
		f.ObjectIDs[i] = id
		f.Generations[i] = gen
	}
}

func mustReproject(t *testing.T, prev, cur Frame, motions map[ObjectID]Mat4, cfg Config) Result {
	t.Helper()
	r, err := Reproject(prev, cur, motions, cfg)
	if err != nil {
		t.Fatalf("Reproject: %v", err)
	}
	return r
}

func requireColor(t *testing.T, got, want RGBA, tol float64) {
	t.Helper()
	for name, pair := range map[string][2]float64{
		"R": {got.R, want.R},
		"G": {got.G, want.G},
		"B": {got.B, want.B},
		"A": {got.A, want.A},
	} {
		if math.Abs(pair[0]-pair[1]) > tol {
			t.Fatalf("%s = %g, want %g (got %+v, want %+v)", name, pair[0], pair[1], got, want)
		}
	}
}

func TestStaticIdentityNormalizesAndBlends(t *testing.T) {
	const w, h = 4, 4
	cam := mustOrtho(t, w, h, 1, 100)
	prev := newFrame(w, h, cam)
	cur := newFrame(w, h, cam)
	fillFrame(prev, 7, 3, 2, RGBA{0.2, 0.4, 0.8, 1}, normalFront)
	fillFrame(cur, 7, 3, 2, RGBA{0.2, 0.4, 0.8, 1}, normalFront)

	r := mustReproject(t, prev, cur, nil, DefaultConfig())
	for i := range r.Frame.Color {
		requireColor(t, r.HistoryColor[i], prev.Color[i], 1e-12)
		requireColor(t, r.Frame.Color[i], cur.Color[i], 1e-12)
		if math.Abs(r.HistoryWeight[i]-1) > 1e-12 {
			t.Fatalf("history weight[%d] = %g, want 1", i, r.HistoryWeight[i])
		}
	}
}

func TestNoHistoryUsesCurrentExactly(t *testing.T) {
	const w, h = 3, 2
	cam := mustOrtho(t, w, h, 1, 100)
	prev := newFrame(w, h, cam)
	cur := newFrame(w, h, cam)
	fillFrame(prev, 1, 1, 2, red, normalFront)
	fillFrame(cur, 2, 1, 2, blue, normalFront)

	r := mustReproject(t, prev, cur, nil, DefaultConfig())
	for i := range cur.Color {
		if r.Frame.Color[i] != cur.Color[i] || r.HistoryColor[i] != cur.Color[i] {
			t.Fatalf("pixel %d: got %+v/%+v, want exact current %+v",
				i, r.Frame.Color[i], r.HistoryColor[i], cur.Color[i])
		}
		if r.HistoryWeight[i] != 0 {
			t.Fatalf("pixel %d: weight %g, want 0", i, r.HistoryWeight[i])
		}
	}
	if r.Stats.PixelsWithoutHistory != w*h {
		t.Fatalf("missing pixels = %d, want %d", r.Stats.PixelsWithoutHistory, w*h)
	}
}

func TestDoubleLayerOcclusionNeverMixesRearHistory(t *testing.T) {
	const w, h = 12, 1
	cam := mustOrtho(t, w, h, 1, 100)
	prev := newFrame(w, h, cam)
	cur := newFrame(w, h, cam)

	for x := 0; x < w; x++ {
		if x < 6 {
			fillPixel(&prev, x, 0, 2, 5, blue)
		} else {
			fillPixel(&prev, x, 0, 1, 2, red)
		}
		if x < 2 || x >= 8 {
			fillPixel(&cur, x, 0, 2, 5, blue)
		} else {
			fillPixel(&cur, x, 0, 1, 2, red)
		}
	}

	motions := map[ObjectID]Mat4{
		1: Translation(-4, 0, 0),
	}
	r := mustReproject(t, prev, cur, motions, DefaultConfig())
	for x := 2; x < 8; x++ {
		requireColor(t, r.Frame.Color[x], red, 1e-12)
	}
	for x := 8; x < w; x++ {
		if r.Frame.Color[x] != blue || r.HistoryWeight[x] != 0 {
			t.Fatalf("disoccluded pixel %d got %+v weight=%g, want exact blue with no history",
				x, r.Frame.Color[x], r.HistoryWeight[x])
		}
	}
	if r.Stats.RejectedObjectGeneration == 0 {
		t.Fatal("expected rear-layer history to be rejected by object/generation evidence")
	}
}

func fillPixel(f *Frame, x, y int, id ObjectID, depth float64, color RGBA) {
	i := f.Index(x, y)
	f.Color[i] = color
	f.Depth[i] = depth
	f.Normals[i] = normalFront
	f.ObjectIDs[i] = id
	f.Generations[i] = 1
}

func TestCameraShakeReprojectsAtCorrectPixel(t *testing.T) {
	const w, h = 8, 3
	prevCam := mustOrtho(t, w, h, 1, 100)
	curCam := prevCam
	curCam.View = Translation(-1, 0, 0) // camera moved right by one pixel

	prev := newFrame(w, h, prevCam)
	cur := newFrame(w, h, curCam)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			prevColor := RGBA{R: float64(x) / float64(w), G: float64(y) / float64(h), B: 0.25, A: 1}
			fillPixel(&prev, x, y, 1, 2, prevColor)
			currentColor := RGBA{R: 0.9, G: 0.9, B: 0.9, A: 1}
			if x+1 < w {
				currentColor = RGBA{
					R: float64(x+1) / float64(w),
					G: float64(y) / float64(h),
					B: 0.25,
					A: 1,
				}
			}
			fillPixel(&cur, x, y, 1, 2, currentColor)
		}
	}

	r := mustReproject(t, prev, cur, nil, DefaultConfig())
	for y := 0; y < h; y++ {
		for x := 0; x < w-1; x++ {
			i := y*w + x
			requireColor(t, r.HistoryColor[i], prev.Color[y*w+x+1], 1e-12)
			if r.HistoryWeight[i] == 0 {
				t.Fatalf("pixel (%d,%d) received no history", x, y)
			}
		}
		last := y*w + w - 1
		if r.HistoryWeight[last] != 0 || r.Frame.Color[last] != cur.Color[last] {
			t.Fatalf("right border pixel %d should have no history, weight=%g", last, r.HistoryWeight[last])
		}
	}
}

func TestNearPlaneRejectsSourceAndReprojectedPoint(t *testing.T) {
	const w, h = 2, 1
	cam := mustOrtho(t, w, h, 1, 100)

	t.Run("source depth", func(t *testing.T) {
		prev := newFrame(w, h, cam)
		cur := newFrame(w, h, cam)
		fillFrame(prev, 1, 1, 0.5, red, normalFront)
		fillFrame(cur, 1, 1, 2, green, normalFront)
		r := mustReproject(t, prev, cur, nil, DefaultConfig())
		if r.Stats.SourceNearPlane == 0 || r.HistoryWeight[0] != 0 {
			t.Fatalf("expected near-plane source rejection, got stats=%+v weight=%g", r.Stats, r.HistoryWeight[0])
		}
	})

	t.Run("reprojected point", func(t *testing.T) {
		prev := newFrame(w, h, cam)
		cur := newFrame(w, h, cam)
		fillFrame(prev, 1, 1, 2, red, normalFront)
		fillFrame(cur, 1, 1, 2, green, normalFront)
		motions := map[ObjectID]Mat4{1: Translation(0, 0, 1.5)}
		r := mustReproject(t, prev, cur, motions, DefaultConfig())
		if r.Stats.SourceNearPlane == 0 || r.HistoryWeight[0] != 0 {
			t.Fatalf("expected near-plane reprojection rejection, got stats=%+v weight=%g", r.Stats, r.HistoryWeight[0])
		}
	})
}

func TestNegativeScaleIsRejected(t *testing.T) {
	const w, h = 2, 1
	cam := mustOrtho(t, w, h, 1, 100)
	prev := newFrame(w, h, cam)
	cur := newFrame(w, h, cam)
	fillFrame(prev, 1, 1, 2, red, normalFront)
	fillFrame(cur, 1, 1, 2, green, normalFront)
	motions := map[ObjectID]Mat4{1: Scale(-1, 1, 1)}
	r := mustReproject(t, prev, cur, motions, DefaultConfig())
	if r.Stats.SourceNegativeScale == 0 {
		t.Fatalf("expected negative-scale rejection, stats=%+v", r.Stats)
	}
	if r.Frame.Color[0] != green || r.HistoryWeight[0] != 0 {
		t.Fatalf("expected fallback to current, got %+v weight=%g", r.Frame.Color[0], r.HistoryWeight[0])
	}
}

func TestAlphaZeroPolicy(t *testing.T) {
	const w, h = 2, 1
	cam := mustOrtho(t, w, h, 1, 100)

	t.Run("current alpha zero resolves to zero", func(t *testing.T) {
		prev := newFrame(w, h, cam)
		cur := newFrame(w, h, cam)
		fillFrame(prev, 1, 1, 2, red, normalFront)
		fillFrame(cur, 1, 1, 2, green, normalFront)
		for i := range cur.Color {
			cur.Color[i] = RGBA{R: 0.5, G: 0.5, B: 0.5, A: 0}
		}
		r := mustReproject(t, prev, cur, nil, DefaultConfig())
		for i := range r.Frame.Color {
			if r.Frame.Color[i] != (RGBA{}) || r.HistoryWeight[i] != 0 {
				t.Fatalf("pixel %d = %+v weight=%g, want zero", i, r.Frame.Color[i], r.HistoryWeight[i])
			}
		}
		if r.Stats.PixelsResolvedToZeroAlpha != w*h {
			t.Fatalf("zero-alpha pixels = %d, want %d", r.Stats.PixelsResolvedToZeroAlpha, w*h)
		}
	})

	t.Run("history alpha zero is rejected", func(t *testing.T) {
		prev := newFrame(w, h, cam)
		cur := newFrame(w, h, cam)
		fillFrame(prev, 1, 1, 2, RGBA{R: 1, G: 1, B: 1, A: 0}, normalFront)
		fillFrame(cur, 1, 1, 2, green, normalFront)
		r := mustReproject(t, prev, cur, nil, DefaultConfig())
		for i := range r.Frame.Color {
			if r.Frame.Color[i] != green || r.HistoryWeight[i] != 0 {
				t.Fatalf("pixel %d = %+v weight=%g, want current green", i, r.Frame.Color[i], r.HistoryWeight[i])
			}
		}
		if r.Stats.SourceAlphaZero != w*h {
			t.Fatalf("zero-alpha sources = %d, want %d", r.Stats.SourceAlphaZero, w*h)
		}
	})
}

func TestDepthAndNormalEvidence(t *testing.T) {
	const w, h = 2, 1
	cam := mustOrtho(t, w, h, 1, 100)

	t.Run("depth error", func(t *testing.T) {
		prev := newFrame(w, h, cam)
		cur := newFrame(w, h, cam)
		fillFrame(prev, 1, 1, 2, red, normalFront)
		fillFrame(cur, 1, 1, 3, green, normalFront)
		r := mustReproject(t, prev, cur, nil, DefaultConfig())
		if r.Stats.RejectedDepthError == 0 || r.HistoryWeight[0] != 0 {
			t.Fatalf("expected depth rejection, stats=%+v", r.Stats)
		}
	})

	t.Run("normal angle", func(t *testing.T) {
		prev := newFrame(w, h, cam)
		cur := newFrame(w, h, cam)
		fillFrame(prev, 1, 1, 2, red, normalFront)
		fillFrame(cur, 1, 1, 2, green, Vec3{1, 0, 0})
		r := mustReproject(t, prev, cur, nil, DefaultConfig())
		if r.Stats.RejectedNormalAngle == 0 || r.HistoryWeight[0] != 0 {
			t.Fatalf("expected normal rejection, stats=%+v", r.Stats)
		}
	})
}

func TestHistoryIsClampedToCurrentSameObjectNeighborhood(t *testing.T) {
	const w, h = 5, 5
	cam := mustOrtho(t, w, h, 1, 100)
	prev := newFrame(w, h, cam)
	cur := newFrame(w, h, cam)
	fillFrame(prev, 1, 1, 2, red, normalFront)
	fillFrame(cur, 1, 1, 2, green, normalFront)
	// Keep one same-object neighbor different, and mark an adjacent pixel as
	// a different object. The latter must not widen the clamp range.
	cur.Color[1*cur.Width+1] = RGBA{0, 1, 0, 1}
	cur.ObjectIDs[1*cur.Width+2] = 2
	cur.Color[1*cur.Width+2] = RGBA{1, 0, 0, 1}

	r := mustReproject(t, prev, cur, nil, DefaultConfig())
	i := 2*w + 2
	if r.HistoryColor[i].R != 0 || r.HistoryColor[i].G != 1 || r.HistoryColor[i].B != 0 {
		t.Fatalf("history was not clamped to same-object range: %+v", r.HistoryColor[i])
	}
}

func TestPerspectiveCameraRoundTrip(t *testing.T) {
	const w, h = 64, 48
	const near, far = 0.1, 100.0
	f := 1.0 / math.Tan((60*math.Pi/180)/2)
	aspect := float64(w) / float64(h)
	cam := Camera{
		View: Identity4(),
		Projection: Mat4{
			{f / aspect, 0, 0, 0},
			{0, f, 0, 0},
			{0, 0, (far + near) / (near - far), 2 * far * near / (near - far)},
			{0, 0, -1, 0},
		},
		Near: near,
		Far:  far,
	}
	if err := cam.Validate(); err != nil {
		t.Fatal(err)
	}
	world, ok := cam.UnprojectPixel(17, 13, w, h, 3.25)
	if !ok {
		t.Fatal("UnprojectPixel failed")
	}
	pixel, depth, ok := cam.ProjectWorld(world, w, h, 0)
	if !ok {
		t.Fatal("ProjectWorld failed")
	}
	if math.Abs(pixel.X-17) > 1e-9 || math.Abs(pixel.Y-13) > 1e-9 || math.Abs(depth-3.25) > 1e-9 {
		t.Fatalf("round trip got pixel %+v depth %g", pixel, depth)
	}

	shifted := cam
	shifted.View = Translation(-0.5, 0.25, 0)
	shiftedPixel, _, ok := shifted.ProjectWorld(world, w, h, 0)
	if !ok {
		t.Fatal("shifted ProjectWorld failed")
	}
	view := shifted.View.MulVec(Vec4{X: world.X, Y: world.Y, Z: world.Z, W: 1})
	clip := shifted.Projection.MulVec(view)
	ndcX, ndcY := clip.X/clip.W, clip.Y/clip.W
	wantX := ((ndcX + 1) * 0.5 * float64(w)) - 0.5
	wantY := ((1 - ndcY) * 0.5 * float64(h)) - 0.5
	if math.Abs(shiftedPixel.X-wantX) > 1e-9 || math.Abs(shiftedPixel.Y-wantY) > 1e-9 {
		t.Fatalf("shifted pixel = %+v, want (%g, %g)", shiftedPixel, wantX, wantY)
	}
	if shiftedPixel.X >= 17 {
		t.Fatalf("camera moving right should move projected pixel left, got %+v", shiftedPixel)
	}
}

func TestTileParallelMatchesWholeFrameExactly(t *testing.T) {
	const w, h = 16, 12
	prevCam := mustOrtho(t, w, h, 1, 100)
	curCam := prevCam
	curCam.View = Translation(-0.37, 0.21, 0)
	prev := newFrame(w, h, prevCam)
	cur := newFrame(w, h, curCam)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			band := (x / 4) % 3
			id := ObjectID(band + 1)
			depth := 2.0 + float64(band)*0.25
			prevColor := RGBA{R: float64((x+y)%7) / 7, G: float64((x*3+y)%5) / 5, B: float64((x+y*2)%11) / 11, A: 1}
			curColor := prevColor
			fillPixel(&prev, x, y, id, depth, prevColor)
			fillPixel(&cur, x, y, id, depth, curColor)
		}
	}
	motions := map[ObjectID]Mat4{
		1: Translation(0.1, -0.07, 0),
		2: Translation(-0.2, 0.11, 0),
		3: Translation(0.05, 0.03, 0),
	}

	wholeCfg := DefaultConfig()
	tileCfg := DefaultConfig()
	// Deliberately non-divisible tile sizes exercise empty/uneven boundaries.
	tileCfg.TilesX, tileCfg.TilesY = 5, 4
	tileCfg.Workers = 3
	whole := mustReproject(t, prev, cur, motions, wholeCfg)
	tiled := mustReproject(t, prev, cur, motions, tileCfg)
	if whole.Stats != tiled.Stats {
		t.Fatalf("stats differ:\nwhole=%+v\ntiled=%+v", whole.Stats, tiled.Stats)
	}
	for i := range whole.Frame.Color {
		if whole.Frame.Color[i] != tiled.Frame.Color[i] ||
			whole.HistoryColor[i] != tiled.HistoryColor[i] ||
			whole.HistoryWeight[i] != tiled.HistoryWeight[i] {
			t.Fatalf("pixel %d differs: whole=%+v/%+v/%g tiled=%+v/%+v/%g",
				i, whole.Frame.Color[i], whole.HistoryColor[i], whole.HistoryWeight[i],
				tiled.Frame.Color[i], tiled.HistoryColor[i], tiled.HistoryWeight[i])
		}
	}
}

func TestReprojectDoesNotMutateInputs(t *testing.T) {
	const w, h = 6, 4
	prevCam := mustOrtho(t, w, h, 1, 100)
	curCam := prevCam
	curCam.View = Translation(-0.25, 0.1, 0)
	prev := newFrame(w, h, prevCam)
	cur := newFrame(w, h, curCam)
	fillFrame(prev, 1, 1, 2, red, normalFront)
	fillFrame(cur, 1, 1, 2, green, normalFront)
	prevBefore := prev.Clone()
	curBefore := cur.Clone()

	_ = mustReproject(t, prev, cur, nil, DefaultConfig())
	assertFrameEqual(t, prev, prevBefore)
	assertFrameEqual(t, cur, curBefore)
}

func assertFrameEqual(t *testing.T, got, want Frame) {
	t.Helper()
	if got.Width != want.Width || got.Height != want.Height {
		t.Fatalf("dimensions differ")
	}
	for i := range want.Color {
		if got.Depth[i] != want.Depth[i] || got.Color[i] != want.Color[i] ||
			got.Normals[i] != want.Normals[i] || got.ObjectIDs[i] != want.ObjectIDs[i] ||
			got.Generations[i] != want.Generations[i] {
			t.Fatalf("frame differs at %d: %+v vs %+v", i, got.Color[i], want.Color[i])
		}
	}
}

func TestHistoryBufferPublishesOnlyCompletedFrames(t *testing.T) {
	const w, h = 2, 1
	cam := mustOrtho(t, w, h, 1, 100)
	initial := newFrame(w, h, cam)
	fillFrame(initial, 1, 1, 2, red, normalFront)
	hbuf, err := NewHistoryBuffer(initial)
	if err != nil {
		t.Fatal(err)
	}
	current := newFrame(w, h, cam)
	fillFrame(current, 1, 1, 2, red, normalFront)

	result, version, err := hbuf.Resolve(current, nil, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, beforeVersion := hbuf.Snapshot()
	if beforeVersion != version {
		t.Fatalf("snapshot version %d, resolve version %d", beforeVersion, version)
	}
	assertFrameEqual(t, snapshot, initial)

	if err := hbuf.Commit(version, result); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := hbuf.Commit(version, result); !errors.Is(err, ErrStaleHistory) {
		t.Fatalf("second commit error = %v, want ErrStaleHistory", err)
	}
	snapshot, afterVersion := hbuf.Snapshot()
	if afterVersion != version+1 {
		t.Fatalf("version = %d, want %d", afterVersion, version+1)
	}
	assertFrameEqual(t, snapshot, result.Frame)
}

func TestInvalidInputsReturnWrappedErrors(t *testing.T) {
	cam := mustOrtho(t, 2, 1, 1, 100)
	frame := newFrame(2, 1, cam)
	fillFrame(frame, 1, 1, 2, red, normalFront)

	t.Run("dimension mismatch", func(t *testing.T) {
		other := newFrame(3, 1, mustOrtho(t, 3, 1, 1, 100))
		fillFrame(other, 1, 1, 2, red, normalFront)
		_, err := Reproject(frame, other, nil, DefaultConfig())
		if !errors.Is(err, ErrDimensionMismatch) {
			t.Fatalf("error = %v, want ErrDimensionMismatch", err)
		}
	})
	t.Run("invalid frame", func(t *testing.T) {
		bad := frame.Clone()
		bad.Color = bad.Color[:1]
		_, err := Reproject(bad, frame, nil, DefaultConfig())
		if !errors.Is(err, ErrInvalidFrame) {
			t.Fatalf("error = %v, want ErrInvalidFrame", err)
		}
	})
	t.Run("invalid config", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.DepthTolerance = -1
		_, err := Reproject(frame, frame, nil, cfg)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("error = %v, want ErrInvalidConfig", err)
		}
	})
}

func TestNormalCheckCanBeDisabled(t *testing.T) {
	const w, h = 2, 1
	cam := mustOrtho(t, w, h, 1, 100)
	prev := newFrame(w, h, cam)
	cur := newFrame(w, h, cam)
	fillFrame(prev, 1, 1, 2, red, normalFront)
	fillFrame(cur, 1, 1, 2, green, Vec3{1, 0, 0})
	cfg := DefaultConfig()
	cfg.MaxNormalAngle = -1
	r := mustReproject(t, prev, cur, nil, cfg)
	if r.HistoryWeight[0] == 0 {
		t.Fatalf("normal-disabled reprojection unexpectedly produced no history: %+v", r.Stats)
	}
}
