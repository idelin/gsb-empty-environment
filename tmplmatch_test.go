package tmplmatch

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// makeScene builds a noise-textured scene and stamps copies of the template
// at the given centers. The scene and template share RNG texture so the
// stamped region matches even after anti-aliased resizing.
func makeScene(w, h, tw, th int, centers []image.Point, scale float64, rngSeed int64) (*image.RGBA, *image.RGBA) {
	rng := newRNG(rngSeed)
	scene := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(80 + rng.intn(140))
			scene.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}

	tmpl := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		for x := 0; x < tw; x++ {
			// A bright disc on a darker background gives the icon structure.
			dx := float64(x - tw/2)
			dy := float64(y - th/2)
			v := uint8(60)
			if dx*dx+dy*dy < float64(tw*th)/8 {
				v = uint8(200 + rng.intn(50))
			}
			tmpl.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}

	stamp(scene, tmpl, centers, scale)
	return scene, tmpl
}

func stamp(scene *image.RGBA, tmpl *image.RGBA, centers []image.Point, scale float64) {
	sw := int(0.5 + float64(tmpl.Bounds().Dx())*scale)
	sh := int(0.5 + float64(tmpl.Bounds().Dy())*scale)
	resized := ResizeBilinear(ToGray(tmpl, false), sw, sh)
	for _, c := range centers {
		x0, y0 := c.X-sw/2, c.Y-sh/2
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				v := uint8(math.Round(resized.Pix[y*sw+x] * 255))
				scene.SetRGBA(x0+x, y0+y, color.RGBA{R: v, G: v, B: v, A: 255})
			}
		}
	}
}

// simple deterministic LCG so tests don't depend on math/rand versioning.
type lcg struct{ state uint64 }

func newRNG(seed int64) *lcg { return &lcg{state: uint64(seed)*6364136223846793005 + 1} }

func (r *lcg) intn(n int) int {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return int((r.state >> 33) % uint64(n))
}

func TestIntegralMatchesBruteForce(t *testing.T) {
	scene, tmpl := makeScene(120, 100, 16, 16, []image.Point{{X: 60, Y: 50}}, 1, 42)
	sg := ToGray(scene, false)
	tg := ToGray(tmpl, true)
	ts := buildTemplateStats(tg)
	ii := NewIntegral(sg)

	got := scanIntegral(sg, ii, tg, ts, 1, 0, 0)
	want := scanBrute(sg, tg, ts, 1, 0, 0)
	if len(got) != len(want) {
		t.Fatalf("candidate count: integral=%d brute=%d", len(got), len(want))
	}
	best := func(cs []Candidate) Candidate {
		b := cs[0]
		for _, c := range cs[1:] {
			if c.Score > b.Score {
				b = c
			}
		}
		return b
	}
	bi, bw := best(got), best(want)
	if bi.X != bw.X || bi.Y != bw.Y {
		t.Fatalf("argmax mismatch: integral=(%d,%d) brute=(%d,%d)", bi.X, bi.Y, bw.X, bw.Y)
	}
	if math.Abs(bi.Score-bw.Score) > 1e-10 {
		t.Fatalf("score mismatch: integral=%.6f brute=%.6f", bi.Score, bw.Score)
	}
}

func TestIntegralRectSum(t *testing.T) {
	g := newGray(5, 4)
	for i := range g.Pix {
		g.Pix[i] = float64(i + 1)
	}
	ii := NewIntegral(g)
	// Whole-image sum must equal 1+2+...+20 = 210.
	if got := ii.rectSum(ii.S1, 0, 0, 5, 4); math.Abs(got-210) > 1e-9 {
		t.Fatalf("whole sum = %v, want 210", got)
	}
	// Sub-rectangle [1,3) x [1,3): pixels 7,8,12,13 = 40.
	if got := ii.rectSum(ii.S1, 1, 1, 2, 2); math.Abs(got-40) > 1e-9 {
		t.Fatalf("rect sum = %v, want 40", got)
	}
}

func TestPyramidFindsStampedTargets(t *testing.T) {
	centers := []image.Point{{X: 80, Y: 70}, {X: 240, Y: 200}}
	scene, tmpl := makeScene(320, 240, 24, 24, centers, 1, 7)
	matches, err := Match(scene, tmpl, Options{Threshold: 0.8})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != len(centers) {
		t.Fatalf("got %d matches, want %d: %+v", len(matches), len(centers), matches)
	}
	for i, c := range centers {
		gotCenter := image.Point{
			X: matches[i].Rect.Min.X + matches[i].Rect.Dx()/2,
			Y: matches[i].Rect.Min.Y + matches[i].Rect.Dy()/2,
		}
		if dist(gotCenter, c) > 2 {
			t.Fatalf("match %d center = %v, want near %v", i, gotCenter, c)
		}
	}
}

func TestPyramidVersusBruteForceAgree(t *testing.T) {
	centers := []image.Point{{X: 90, Y: 80}, {X: 260, Y: 190}}
	scene, tmpl := makeScene(320, 240, 24, 24, centers, 1, 11)
	opts := Options{Threshold: 0.8, Scales: []float64{1}}

	pyr, _, err := MatchWithStats(scene, tmpl, opts)
	if err != nil {
		t.Fatal(err)
	}
	brute, _, err := BruteForceMatch(scene, tmpl, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(pyr) != len(brute) {
		t.Fatalf("count mismatch: pyramid=%d brute=%d", len(pyr), len(brute))
	}
	for i := range pyr {
		if pyr[i].Rect.Min != brute[i].Rect.Min {
			t.Fatalf("match %d position: pyramid=%v brute=%v", i, pyr[i].Rect, brute[i].Rect)
		}
		if math.Abs(pyr[i].Score-brute[i].Score) > 1e-9 {
			t.Fatalf("match %d score: pyramid=%.6f brute=%.6f", i, pyr[i].Score, brute[i].Score)
		}
	}
}

func TestMultiScaleRecall(t *testing.T) {
	centers := []image.Point{{X: 100, Y: 90}, {X: 250, Y: 180}}
	// Targets rendered at 0.8 scale while matcher searches several scales.
	scene, tmpl := makeScene(320, 240, 28, 28, centers, 0.8, 23)
	matches, err := Match(scene, tmpl, Options{
		Threshold: 0.75,
		Scales:    []float64{0.7, 0.8, 0.9, 1.0, 1.1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) < len(centers) {
		t.Fatalf("got %d matches, want at least %d: %+v", len(matches), len(centers), matches)
	}
}

func TestMaskedTemplate(t *testing.T) {
	scene, tmpl := makeScene(200, 160, 20, 20, []image.Point{{X: 100, Y: 80}}, 1, 99)

	// Punch transparent corners into the template; those pixels must be
	// ignored and the target must still be located.
	masked := image.NewRGBA(tmpl.Bounds())
	b := tmpl.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			corner := (x-b.Min.X < 4 && y-b.Min.Y < 4) ||
				(x-b.Min.X >= b.Dx()-4 && y-b.Min.Y >= b.Dy()-4)
			if corner {
				masked.SetRGBA(x, y, color.RGBA{A: 0})
				continue
			}
			r, g, bl, _ := tmpl.At(x, y).RGBA()
			masked.SetRGBA(x, y, color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: 255})
		}
	}
	g := ToGray(masked, true)
	if g.ValidCount >= 20*20 {
		t.Fatalf("mask not applied: ValidCount=%d", g.ValidCount)
	}
	matches, err := Match(scene, masked, Options{Threshold: 0.8})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1: %+v", len(matches), matches)
	}
	center := image.Point{
		X: matches[0].Rect.Min.X + matches[0].Rect.Dx()/2,
		Y: matches[0].Rect.Min.Y + matches[0].Rect.Dy()/2,
	}
	if dist(center, image.Point{X: 100, Y: 80}) > 2 {
		t.Fatalf("masked match center = %v", center)
	}
}

func TestNMSRemovesDuplicates(t *testing.T) {
	tmpl := &GrayImage{Width: 10, Height: 10, ValidCount: 100}
	cands := []Candidate{
		{X: 0, Y: 0, Score: 0.95, Scale: 1},
		{X: 1, Y: 0, Score: 0.93, Scale: 1},  // heavily overlaps the first
		{X: 50, Y: 50, Score: 0.9, Scale: 1}, // separate box
	}
	kept := nms(cands, tmpl, 0.5)
	if len(kept) != 2 {
		t.Fatalf("nms kept %d boxes, want 2: %+v", len(kept), kept)
	}
	if kept[0].Score != 0.95 || kept[1].Score != 0.9 {
		t.Fatalf("nms order/scores wrong: %+v", kept)
	}
}

func TestCapCandidatesAndQuickSelect(t *testing.T) {
	cands := make([]Candidate, 20)
	for i := range cands {
		cands[i] = Candidate{X: i, Score: float64(i) / 20.0}
	}
	kept := capCandidates(cands, 5)
	if len(kept) != 5 {
		t.Fatalf("kept %d, want 5", len(kept))
	}
	minScore := kept[0].Score
	for _, c := range kept {
		if c.Score < minScore {
			minScore = c.Score
		}
	}
	// The five strongest original scores are 0.75..0.95.
	if math.Abs(minScore-0.75) > 1e-9 {
		t.Fatalf("weakest kept score = %.2f, want 0.75", minScore)
	}

	xs := []float64{3, 1, 4, 1, 5, 9, 2, 6}
	if got := quickSelect(append([]float64(nil), xs...), 3); math.Abs(got-5) > 1e-9 {
		t.Fatalf("quickSelect k=3 = %.2f, want 5", got)
	}
}

func TestThresholdFiltersLowScores(t *testing.T) {
	scene, tmpl := makeScene(160, 120, 16, 16, []image.Point{{X: 80, Y: 60}}, 1, 5)
	if _, err := Match(scene, tmpl, Options{Threshold: 1.5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	matches, err := Match(scene, tmpl, Options{Threshold: 0.999})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		if m.Score < 0.999 {
			t.Fatalf("sub-threshold match returned: %+v", m)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	scene := image.NewRGBA(image.Rect(0, 0, 10, 10))
	tmpl := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range tmpl.Pix {
		tmpl.Pix[i] = 0xff
	}
	if _, err := Match(nil, tmpl, Options{}); err != ErrNilImage {
		t.Fatalf("nil scene err = %v", err)
	}
	transparent := image.NewRGBA(image.Rect(0, 0, 4, 4)) // all alpha 0
	if _, err := Match(scene, transparent, Options{}); err != ErrEmptyTemplate {
		t.Fatalf("empty template err = %v", err)
	}
	opaque := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range opaque.Pix {
		opaque.Pix[i] = 0xff
	}
	if _, _, err := BruteForceMatch(scene, opaque, Options{Scales: []float64{0}}); err != ErrBadScale {
		t.Fatalf("bad scale err = %v", err)
	}
}

func dist(a, b image.Point) int {
	dx := a.X - b.X
	dy := a.Y - b.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}
