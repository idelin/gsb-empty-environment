package tmplmatch

import (
	"image"
	"time"
)

// BruteStats is the brute-force counterpart of Stats.
type BruteStats struct {
	Duration   time.Duration
	Candidates int
	AfterNMS   int
}

// BruteForceMatch scans the full-resolution scene for every template scale
// and evaluates NCC at every legal top-left position without a pyramid. It
// uses the same score function, threshold and NMS as Match, so its output is
// the reference the coarse-to-fine matcher is compared against.
func BruteForceMatch(scene, template image.Image, opts Options) ([]MatchResult, BruteStats, error) {
	cfg := opts.withDefaults()
	stats := BruteStats{}
	if scene == nil || template == nil {
		return nil, stats, ErrNilImage
	}
	start := time.Now()
	sceneGray := ToGray(scene, false)
	tmplGray := ToGray(template, true)
	if tmplGray.ValidCount == 0 {
		return nil, stats, ErrEmptyTemplate
	}

	var all []Candidate
	for _, scale := range cfg.Scales {
		if scale <= 0 {
			return nil, stats, ErrBadScale
		}
		scaledW := int(0.5 + float64(tmplGray.Width)*scale)
		scaledH := int(0.5 + float64(tmplGray.Height)*scale)
		if scaledW < 2 || scaledH < 2 || scaledW > sceneGray.Width || scaledH > sceneGray.Height {
			continue
		}
		scaledT := ResizeBilinear(tmplGray, scaledW, scaledH)
		if scaledT.ValidCount == 0 {
			continue
		}
		ts := buildTemplateStats(scaledT)
		// The reference stays faithful to "every position", while bounding
		// memory and NMS work: nothing above the threshold is discarded.
		cands := scanBrute(sceneGray, scaledT, ts, 1, cfg.Threshold, maxBruteCandidates)
		for i := range cands {
			cands[i].Scale = scale
		}
		all = append(all, cands...)
	}
	stats.Candidates = len(all)

	kept := nms(all, tmplGray, cfg.NMSOverlap)
	matches := kept[:0]
	for _, m := range kept {
		if m.Score >= cfg.Threshold {
			matches = append(matches, m)
		}
	}
	stats.AfterNMS = len(matches)
	stats.Duration = time.Since(start)
	return matches, stats, nil
}

// maxBruteCandidates bounds brute-force output per scale; positions below
// the configured threshold are excluded regardless of this cap.
const maxBruteCandidates = 20000
