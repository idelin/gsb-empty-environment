// Command tmplmatch-bench generates a synthetic RPA screenshot with several
// scaled, anti-aliased button icons and compares the integral-image pyramid
// matcher against a full-resolution brute-force NCC scan, both in result
// quality and wall-clock time.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"strings"
	"time"

	"example.org/tmplmatch"
)

type lcg struct{ state uint64 }

func newRNG(seed int64) *lcg { return &lcg{state: uint64(seed)*6364136223846793005 + 1} }

func (r *lcg) intn(n int) int {
	r.state = r.state*6364136223846793005 + 1442695040888963407
	return int((r.state >> 33) % uint64(n))
}

type target struct {
	center image.Point
	scale  float64
	masked bool
}

func main() {
	width := flag.Int("width", 640, "screenshot width")
	height := flag.Int("height", 480, "screenshot height")
	tw := flag.Int("tw", 32, "template width")
	th := flag.Int("th", 32, "template height")
	threshold := flag.Float64("threshold", 0.8, "NCC threshold")
	scalesFlag := flag.String("scales", "0.75,0.9,1.0,1.1,1.25", "comma-separated template scales")
	flag.Parse()

	scales, err := parseScales(*scalesFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	targets := []target{
		{center: image.Point{X: 120, Y: 110}, scale: 0.9},
		{center: image.Point{X: 380, Y: 150}, scale: 1.1},
		{center: image.Point{X: 520, Y: 360}, scale: 1.0},
		{center: image.Point{X: 200, Y: 380}, scale: 0.75, masked: true},
	}

	scene, tmpl := buildScene(*width, *height, *tw, *th, targets, 20261111)
	opts := tmplmatch.Options{Threshold: *threshold, Scales: scales}

	fmt.Println("== template matching benchmark ==")
	fmt.Printf("scene=%dx%d template=%dx%d scales=%v threshold=%.2f targets=%d\n\n",
		*width, *height, *tw, *th, scales, *threshold, len(targets))

	pyr, pstats, perr := tmplmatch.MatchWithStats(scene, tmpl, opts)
	if perr != nil {
		fmt.Fprintln(os.Stderr, perr)
		os.Exit(1)
	}
	brute, bstats, berr := tmplmatch.BruteForceMatch(scene, tmpl, opts)
	if berr != nil {
		fmt.Fprintln(os.Stderr, berr)
		os.Exit(1)
	}

	printPyramidSection(pstats)
	printMatches("pyramid (integral image + coarse-to-fine)", pyr, targets)
	fmt.Println()
	printMatches("brute force (full-resolution NCC)", brute, targets)
	fmt.Println()

	pyrTotal := totalDuration(pstats)
	fmt.Printf("wall time: pyramid=%v  brute=%v  speedup=%.1fx\n",
		pyrTotal.Round(time.Microsecond), bstats.Duration.Round(time.Microsecond),
		float64(bstats.Duration)/float64(pyrTotal))
	fmt.Printf("candidates before NMS: pyramid=%d brute=%d\n", pstats.Candidates, bstats.Candidates)

	compare(pyr, brute)
}

func printPyramidSection(s tmplmatch.Stats) {
	fmt.Println("pyramid timing:")
	for _, phase := range []string{"preprocess", "pyramid_search", "nms"} {
		if d, ok := s.Durations[phase]; ok {
			fmt.Printf("  %-15s %v\n", phase, d.Round(time.Microsecond))
		}
	}
	scales := make([]float64, 0, len(s.LevelsPerScale))
	for scale := range s.LevelsPerScale {
		scales = append(scales, scale)
	}
	sortFloat64(scales)
	for _, scale := range scales {
		fmt.Printf("  scale %.2f -> %d pyramid level(s)\n", scale, s.LevelsPerScale[scale])
	}
	fmt.Println()
}

func printMatches(title string, matches []tmplmatch.MatchResult, targets []target) {
	fmt.Printf("%s: %d hit(s)\n", title, len(matches))
	for i, m := range matches {
		center := image.Point{
			X: m.Rect.Min.X + m.Rect.Dx()/2,
			Y: m.Rect.Min.Y + m.Rect.Dy()/2,
		}
		nearest := nearestTarget(center, targets)
		err := chebyshev(center, nearest.center)
		fmt.Printf("  #%d rect=%s score=%.4f scale=%.2f center=%s nearest_target_scale=%.2f center_err=%dpx\n",
			i+1, m.Rect, m.Score, m.Scale, center, nearest.scale, err)
	}
}

func compare(pyr, brute []tmplmatch.MatchResult) {
	fmt.Println()
	if len(pyr) != len(brute) {
		fmt.Printf("RESULT MISMATCH: pyramid found %d hits, brute found %d\n", len(pyr), len(brute))
		return
	}
	maxPosErr, maxScoreErr := 0, 0.0
	for i := range pyr {
		if pe := chebyshev(pyr[i].Rect.Min, brute[i].Rect.Min); pe > maxPosErr {
			maxPosErr = pe
		}
		if se := math.Abs(pyr[i].Score - brute[i].Score); se > maxScoreErr {
			maxScoreErr = se
		}
	}
	fmt.Printf("agreement: %d/%d hits identical, max position error=%dpx, max score error=%.2e\n",
		len(pyr), len(brute), maxPosErr, maxScoreErr)
	if maxPosErr == 0 && maxScoreErr < 1e-9 {
		fmt.Println("verdict: pyramid results are identical to full-resolution brute force.")
	}
}

func buildScene(w, h, tw, th int, targets []target, seed int64) (*image.RGBA, *image.RGBA) {
	rng := newRNG(seed)
	scene := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Smooth-ish desktop background with mild noise.
			base := 70 + int(30*math.Sin(float64(x)/55.0)) + int(20*math.Cos(float64(y)/41.0))
			v := clamp255(base + rng.intn(25) - 12)
			scene.SetRGBA(x, y, color.RGBA{R: uint8(v), G: uint8(v), B: uint8(v), A: 255})
		}
	}

	tmpl := image.NewRGBA(image.Rect(0, 0, tw, th))
	for y := 0; y < th; y++ {
		for x := 0; x < tw; x++ {
			dx := float64(x - tw/2)
			dy := float64(y - th/2)
			r := math.Hypot(dx, dy)
			v := uint8(55)
			if r < float64(tw)/2.5 {
				v = uint8(clamp255(190 + rng.intn(50)))
			}
			if r < float64(tw)/5 {
				v = uint8(clamp255(235 + rng.intn(20)))
			}
			tmpl.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}

	// One target uses a template with transparent corners; stamp a matching
	// masked variant so the mask path is exercised end to end.
	maskedTmpl := image.NewRGBA(tmpl.Bounds())
	b := tmpl.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			corner := (x-b.Min.X < 6 && y-b.Min.Y < 6) ||
				(x-b.Min.X >= b.Dx()-6 && y-b.Min.Y >= b.Dy()-6)
			if corner {
				maskedTmpl.SetRGBA(x, y, color.RGBA{A: 0})
				continue
			}
			r, g, bl, _ := tmpl.At(x, y).RGBA()
			maskedTmpl.SetRGBA(x, y, color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: 255})
		}
	}

	for _, tgt := range targets {
		src := tmpl
		if tgt.masked {
			src = maskedTmpl
		}
		sw := int(0.5 + float64(tw)*tgt.scale)
		sh := int(0.5 + float64(th)*tgt.scale)
		resized := tmplmatch.ResizeBilinear(tmplmatch.ToGray(src, true), sw, sh)
		x0, y0 := tgt.center.X-sw/2, tgt.center.Y-sh/2
		for y := 0; y < sh; y++ {
			for x := 0; x < sw; x++ {
				idx := y*sw + x
				if resized.Mask != nil && !resized.Mask[idx] {
					continue
				}
				v := uint8(math.Round(resized.Pix[idx] * 255))
				scene.SetRGBA(x0+x, y0+y, color.RGBA{R: v, G: v, B: v, A: 255})
			}
		}
	}
	return scene, tmpl
}

func nearestTarget(p image.Point, targets []target) target {
	best := targets[0]
	bestD := chebyshev(p, best.center)
	for _, t := range targets[1:] {
		if d := chebyshev(p, t.center); d < bestD {
			best, bestD = t, d
		}
	}
	return best
}

func chebyshev(a, b image.Point) int {
	dx, dy := a.X-b.X, a.Y-b.Y
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

func clamp255(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

func totalDuration(s tmplmatch.Stats) time.Duration {
	var total time.Duration
	for _, d := range s.Durations {
		total += d
	}
	return total
}

func sortFloat64(xs []float64) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

func parseScales(s string) ([]float64, error) {
	var out []float64
	for _, part := range strings.Split(s, ",") {
		var f float64
		if _, err := fmt.Sscanf(part, "%f", &f); err != nil {
			return nil, fmt.Errorf("invalid scale %q: %w", part, err)
		}
		if f <= 0 {
			return nil, fmt.Errorf("scale must be positive, got %f", f)
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no scales provided")
	}
	return out, nil
}
