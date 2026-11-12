package taskpkg

import (
	"fmt"
	"math"
)

// wordRecord keeps the normalized geometry and the source position next to the
// public Word. Source order is used for deterministic evidence and for the
// final Words list.
type wordRecord struct {
	Word
	source   int
	bounds   Bounds
	baseline float64
	left     float64
	right    float64
	width    float64
}

func normalizeOptions(opts Options) (Options, error) {
	if math.IsNaN(opts.PageRotationDegrees) || math.IsInf(opts.PageRotationDegrees, 0) {
		return Options{}, fmt.Errorf("%w: page rotation is not finite", ErrInvalidOptions)
	}
	if math.IsNaN(opts.BaselineThreshold) || math.IsInf(opts.BaselineThreshold, 0) ||
		opts.BaselineThreshold < 0 {
		return Options{}, fmt.Errorf("%w: baseline threshold must be finite and non-negative",
			ErrInvalidOptions)
	}
	if math.IsNaN(opts.LinePenalty) || math.IsInf(opts.LinePenalty, 0) ||
		opts.LinePenalty < 0 {
		return Options{}, fmt.Errorf("%w: line penalty must be finite and non-negative",
			ErrInvalidOptions)
	}

	opts.MaxOverlapRatio = clampRatio(opts.MaxOverlapRatio, 0.25)
	opts.MaxGapRatio = clampRatio(opts.MaxGapRatio, 0.25)
	opts.DuplicateCoverage = clampRatio(opts.DuplicateCoverage, 0.80)
	if math.IsNaN(opts.MaxOverlapRatio) || math.IsInf(opts.MaxOverlapRatio, 0) {
		return Options{}, fmt.Errorf("%w: overlap ratio is not finite", ErrInvalidOptions)
	}
	if math.IsNaN(opts.MaxGapRatio) || math.IsInf(opts.MaxGapRatio, 0) {
		return Options{}, fmt.Errorf("%w: gap ratio is not finite", ErrInvalidOptions)
	}
	if math.IsNaN(opts.DuplicateCoverage) || math.IsInf(opts.DuplicateCoverage, 0) {
		return Options{}, fmt.Errorf("%w: duplicate coverage is not finite", ErrInvalidOptions)
	}

	if opts.MaxWords == 0 {
		opts.MaxWords = MaxWords
	}
	if opts.MaxWords < 1 || opts.MaxWords > MaxWords {
		return Options{}, fmt.Errorf("%w: MaxWords must be between 1 and %d",
			ErrInvalidOptions, MaxWords)
	}
	return opts, nil
}

func normalizeWords(words []Word, rotationDegrees float64) ([]wordRecord, error) {
	records := make([]wordRecord, 0, len(words))
	for i, w := range words {
		if !validQuad(w.Quad) {
			return nil, fmt.Errorf("%w at input index %d", ErrInvalidBox, i)
		}
		if math.IsNaN(w.Confidence) || math.IsInf(w.Confidence, 0) {
			return nil, fmt.Errorf("%w at input index %d: confidence is not finite",
				ErrInvalidWord, i)
		}
		q := NormalizeQuad(w.Quad, rotationDegrees)
		w.Quad = q
		b := q.Bounds()
		records = append(records, wordRecord{
			Word:     w,
			source:   i,
			bounds:   b,
			baseline: Baseline(q),
			left:     b.MinX,
			right:    b.MaxX,
			width:    b.Width(),
		})
	}
	return records, nil
}

func selectedWords(records []wordRecord) []Word {
	out := make([]Word, len(records))
	for i, r := range records {
		out[i] = r.Word
	}
	return out
}
