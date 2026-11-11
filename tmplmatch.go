// Package tmplmatch provides multi-scale normalized cross-correlation (NCC)
// template matching for desktop RPA. It locates button-like icons inside
// screenshots even when the rendered icon is scaled or anti-aliased.
//
// The matcher combines:
//   - summed-area (integral) images for O(1)-per-position window mean and
//     variance in the NCC denominator,
//   - an image pyramid with a coarse-to-fine search to avoid scanning the
//     full-resolution image at every scale,
//   - alpha masks so transparent template pixels are ignored,
//   - non-maximum suppression to return distinct matches only.
package tmplmatch

import (
	"errors"
	"image"
	"sort"
	"time"
)

// Sentinel errors returned by the matchers.
var (
	ErrNilImage      = errors.New("tmplmatch: scene and template must not be nil")
	ErrEmptyTemplate = errors.New("tmplmatch: template has no opaque pixels")
	ErrBadScale      = errors.New("tmplmatch: scales must be positive")
)

// Candidate is one detected template placement before cross-scale NMS.
// Coordinates are relative to the source image at native resolution.
type Candidate struct {
	X     int
	Y     int
	Score float64
	Scale float64 // template scale factor this candidate came from
}

// MatchResult is a final, NMS-filtered hit with its bounding rectangle.
type MatchResult struct {
	Rect  image.Rectangle
	Score float64
	Scale float64
}

// Options controls a Match call.
type Options struct {
	// Threshold is the minimum NCC score (0..1) for a hit. Default 0.8.
	Threshold float64
	// Scales are template scale factors, e.g. 0.75, 1.0, 1.25. The template
	// is resized per scale while the screenshot keeps its native resolution.
	// Defaults to a single 1.0 scale.
	Scales []float64
	// MaxPyramidLevels bounds the number of pyramid levels. <=0 means
	// automatic: downsample until the template's short side approaches
	// MinCoarseTemplate.
	MaxPyramidLevels int
	// MinCoarseTemplate is the minimum short-side template size at the
	// coarsest pyramid level. Default 8.
	MinCoarseTemplate int
	// MaxCandidatesPerLevel caps how many coarse hits are refined at the
	// next level, keeping worst-case work bounded. Default 512.
	MaxCandidatesPerLevel int
	// NMSOverlap is the IoU threshold above which two boxes suppress each
	// other. Default 0.5.
	NMSOverlap float64
	// CoarseStep is the position stride at the coarsest level. Default 1
	// (dense scan); raising it trades recall for speed.
	CoarseStep int
}

// Stats reports timings and counters for one Match call.
type Stats struct {
	Durations      map[string]time.Duration
	LevelsPerScale map[float64]int
	Candidates     int
	AfterNMS       int
}

func (o *Options) withDefaults() Options {
	c := *o
	if c.Threshold <= 0 {
		c.Threshold = 0.8
	}
	if len(c.Scales) == 0 {
		c.Scales = []float64{1}
	}
	if c.MinCoarseTemplate <= 0 {
		c.MinCoarseTemplate = 8
	}
	if c.MaxCandidatesPerLevel <= 0 {
		c.MaxCandidatesPerLevel = 512
	}
	if c.NMSOverlap <= 0 {
		c.NMSOverlap = 0.5
	}
	if c.CoarseStep <= 0 {
		c.CoarseStep = 1
	}
	return c
}

// Match finds every distinct placement of template inside scene whose NCC is
// at least opts.Threshold. Template pixels with alpha < 128 are masked out.
func Match(scene, template image.Image, opts Options) ([]MatchResult, error) {
	matches, _, err := MatchWithStats(scene, template, opts)
	return matches, err
}

// MatchWithStats behaves like Match and additionally returns timing/counter
// statistics used to compare the pyramid matcher against brute force.
func MatchWithStats(scene, template image.Image, opts Options) ([]MatchResult, Stats, error) {
	cfg := opts.withDefaults()
	stats := Stats{
		Durations:      map[string]time.Duration{},
		LevelsPerScale: map[float64]int{},
	}
	if scene == nil || template == nil {
		return nil, stats, ErrNilImage
	}
	t0 := time.Now()
	sceneGray := ToGray(scene, false)
	tmplGray := ToGray(template, true)
	if tmplGray.ValidCount == 0 {
		return nil, stats, ErrEmptyTemplate
	}
	stats.Durations["preprocess"] = time.Since(t0)

	tPyramid := time.Now()
	var all []Candidate
	for _, scale := range cfg.Scales {
		if scale <= 0 {
			return nil, stats, ErrBadScale
		}
		cands, levels := matchScale(sceneGray, tmplGray, scale, cfg)
		stats.LevelsPerScale[scale] = levels
		all = append(all, cands...)
	}
	stats.Durations["pyramid_search"] = time.Since(tPyramid)
	stats.Candidates = len(all)

	t1 := time.Now()
	kept := nms(all, tmplGray, cfg.NMSOverlap)
	stats.Durations["nms"] = time.Since(t1)

	matches := make([]MatchResult, 0, len(kept))
	for _, m := range kept {
		if m.Score < cfg.Threshold {
			continue
		}
		matches = append(matches, m)
	}
	stats.AfterNMS = len(matches)
	return matches, stats, nil
}

// matchScale runs the coarse-to-fine search for one template scale and
// returns candidates in full-resolution scene coordinates.
func matchScale(scene, tmpl *GrayImage, scale float64, cfg Options) ([]Candidate, int) {
	scaledW := int(0.5 + float64(tmpl.Width)*scale)
	scaledH := int(0.5 + float64(tmpl.Height)*scale)
	if scaledW < 2 || scaledH < 2 || scaledW > scene.Width || scaledH > scene.Height {
		return nil, 0
	}
	scaledT := ResizeBilinear(tmpl, scaledW, scaledH)
	if scaledT.ValidCount == 0 {
		return nil, 0
	}

	levels := chooseLevels(scene, scaledT, cfg)
	scenePyr := buildPyramid(scene, levels)
	tmplPyr := buildPyramid(scaledT, levels)

	coarsest := levels - 1
	tc := tmplPyr[coarsest]
	sc := scenePyr[coarsest]
	ts := buildTemplateStats(tc)

	var cands []Candidate
	if tc.hasMask() {
		cands = scanBrute(sc, tc, ts, cfg.CoarseStep, cfg.Threshold, cfg.MaxCandidatesPerLevel)
	} else {
		cands = scanIntegral(sc, NewIntegral(sc), tc, ts, cfg.CoarseStep, cfg.Threshold, cfg.MaxCandidatesPerLevel)
	}

	// Refine level by level. Each coarse position maps to a small window of
	// positions at the next finer level; only those are evaluated.
	for level := coarsest - 1; level >= 0; level-- {
		finerScene := scenePyr[level]
		finerTmpl := tmplPyr[level]
		finerTS := buildTemplateStats(finerTmpl)
		next := make([]Candidate, 0, len(cands)*4)
		seen := make(map[[2]int]bool, len(cands)*4)
		for _, c := range cands {
			baseX, baseY := c.X*2, c.Y*2
			for dy := -1; dy <= 2; dy++ {
				for dx := -1; dx <= 2; dx++ {
					x, y := baseX+dx, baseY+dy
					if x < 0 || y < 0 ||
						x+finerTmpl.Width > finerScene.Width ||
						y+finerTmpl.Height > finerScene.Height {
						continue
					}
					key := [2]int{x, y}
					if seen[key] {
						continue
					}
					seen[key] = true
					s := scoreAt(finerScene, finerTmpl, finerTS, x, y)
					if s >= cfg.Threshold {
						next = append(next, Candidate{X: x, Y: y, Score: s})
					}
				}
			}
		}
		cands = capCandidates(next, cfg.MaxCandidatesPerLevel)
	}

	out := make([]Candidate, len(cands))
	for i, c := range cands {
		out[i] = Candidate{X: c.X, Y: c.Y, Score: c.Score, Scale: scale}
	}
	return out, levels
}

// chooseLevels returns the total pyramid level count (level 0 = native).
func chooseLevels(scene, tmpl *GrayImage, cfg Options) int {
	shortSide := tmpl.Width
	if tmpl.Height < shortSide {
		shortSide = tmpl.Height
	}
	levels := 1
	for level := 1; ; level++ {
		shift := uint(level)
		if cfg.MaxPyramidLevels > 0 && level >= cfg.MaxPyramidLevels {
			break
		}
		if shortSide>>shift < cfg.MinCoarseTemplate {
			break
		}
		if scene.Width>>shift < 4 || scene.Height>>shift < 4 {
			break
		}
		if tmpl.Width>>shift < 2 || tmpl.Height>>shift < 2 {
			break
		}
		levels++
	}
	return levels
}

// buildPyramid returns levels[0..count-1]; level 0 is src itself and each
// subsequent level is a 2x2 block-average downsample.
func buildPyramid(src *GrayImage, count int) []*GrayImage {
	pyr := make([]*GrayImage, count)
	pyr[0] = src
	for level := 1; level < count; level++ {
		pyr[level] = downsample2(pyr[level-1])
	}
	return pyr
}

// downsample2 halves width and height using 2x2 block averages. Mask pixels
// survive only when all four contributing pixels are valid, keeping the mask
// conservative.
func downsample2(src *GrayImage) *GrayImage {
	w, h := src.Width, src.Height
	nw, nh := w/2, h/2
	dst := newGray(nw, nh)
	if src.Mask != nil {
		dst.Mask = make([]bool, nw*nh)
		dst.ValidCount = 0
	}
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			sx, sy := x*2, y*2
			i00 := sy*w + sx
			i01 := i00 + 1
			i10 := i00 + w
			i11 := i10 + 1
			dst.Pix[y*nw+x] = (src.Pix[i00] + src.Pix[i01] + src.Pix[i10] + src.Pix[i11]) * 0.25
			if dst.Mask != nil && src.Mask[i00] && src.Mask[i01] && src.Mask[i10] && src.Mask[i11] {
				dst.Mask[y*nw+x] = true
				dst.ValidCount++
			}
		}
	}
	return dst
}

// capCandidates keeps at most limit candidates by dropping the weakest. The
// per-level fan-out is small, so a selection pass is sufficient here.
func capCandidates(cands []Candidate, limit int) []Candidate {
	if len(cands) <= limit {
		return cands
	}
	// Find the score of the limit-th strongest candidate via quickselect.
	scores := make([]float64, len(cands))
	for i, c := range cands {
		scores[i] = c.Score
	}
	cutoff := quickSelect(scores, limit)
	out := cands[:0]
	for _, c := range cands {
		if c.Score >= cutoff && len(out) < limit {
			out = append(out, c)
		}
	}
	return out
}

// quickSelect reorders xs in place and returns the k-th largest value
// (k is 1-based). The implementation is iterative to avoid deep recursion.
func quickSelect(xs []float64, k int) float64 {
	lo, hi := 0, len(xs)-1
	target := len(xs) - k // k-th largest == element at this ascending index
	for lo < hi {
		pivot := xs[(lo+hi)/2]
		i, j := lo, hi
		for i <= j {
			for xs[i] < pivot {
				i++
			}
			for xs[j] > pivot {
				j--
			}
			if i <= j {
				xs[i], xs[j] = xs[j], xs[i]
				i++
				j--
			}
		}
		if target <= j {
			hi = j
		} else if target >= i {
			lo = i
		} else {
			break
		}
	}
	return xs[target]
}

// nms greedily keeps the strongest candidate and suppresses boxes whose IoU
// with an already kept box exceeds overlap. Candidates from different scales
// describe different box sizes, so IoU uses the actual scaled boxes.
func nms(cands []Candidate, tmpl *GrayImage, overlap float64) []MatchResult {
	sorted := make([]Candidate, len(cands))
	copy(sorted, cands)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Score > sorted[j].Score })
	kept := make([]MatchResult, 0, len(sorted))
	suppressed := make([]bool, len(sorted))
	for i, c := range sorted {
		if suppressed[i] {
			continue
		}
		box := candidateBox(c, tmpl)
		kept = append(kept, MatchResult{Rect: box, Score: c.Score, Scale: c.Scale})
		for j := i + 1; j < len(sorted); j++ {
			if suppressed[j] {
				continue
			}
			if iou(box, candidateBox(sorted[j], tmpl)) > overlap {
				suppressed[j] = true
			}
		}
	}
	return kept
}

func candidateBox(c Candidate, tmpl *GrayImage) image.Rectangle {
	w := int(0.5 + float64(tmpl.Width)*c.Scale)
	h := int(0.5 + float64(tmpl.Height)*c.Scale)
	return image.Rect(c.X, c.Y, c.X+w, c.Y+h)
}

func iou(a, b image.Rectangle) float64 {
	inter := a.Intersect(b)
	if inter.Empty() {
		return 0
	}
	interArea := float64(inter.Dx() * inter.Dy())
	unionArea := float64(a.Dx()*a.Dy()+b.Dx()*b.Dy()) - interArea
	if unionArea <= 0 {
		return 0
	}
	return interArea / unionArea
}
