package taskpkg

import (
	"errors"
	"fmt"
)

// MaxWords is the hard upper bound for the exact line-partition search.
const MaxWords = 24

var (
	// ErrTooManyWords is returned when more than the supported number of
	// distinct words remain after duplicate removal.
	ErrTooManyWords = errors.New("taskpkg: too many distinct words")
	// ErrInvalidBox is returned for a non-finite or malformed word quad.
	ErrInvalidBox = errors.New("taskpkg: invalid word box")
	// ErrInvalidOptions is returned for impossible search parameters.
	ErrInvalidOptions = errors.New("taskpkg: invalid options")
	// ErrInvalidWord is returned for non-finite confidence values.
	ErrInvalidWord = errors.New("taskpkg: invalid word")
)

// Word is an OCR word candidate. ID is the stable identifier used as the final
// deterministic tie-breaker; it may be empty, in which case the original input
// position is used as a fallback during tie comparison.
type Word struct {
	ID         string
	Quad       Quad
	Text       string
	Confidence float64
}

// Options controls normalization, duplicate removal and line partitioning.
//
// All distances are in normalized page units. Ratios are fractions of the
// shorter word's width.
type Options struct {
	// PageRotationDegrees is the clockwise page rotation to remove.
	PageRotationDegrees float64

	// BaselineThreshold is the maximum absolute baseline difference between
	// horizontally adjacent words on one line.
	BaselineThreshold float64

	// LinePenalty is added once for every line. It must be non-negative.
	LinePenalty float64

	// MaxOverlapRatio bounds the horizontal overlap of adjacent boxes. A zero
	// value selects the default 0.25; a negative value disables the check.
	MaxOverlapRatio float64

	// MaxGapRatio bounds the horizontal gap between adjacent boxes. A zero
	// value selects the default 0.25; a negative value disables the check.
	// The gap check is what keeps two widely separated table columns from
	// being merged merely because they share a baseline.
	MaxGapRatio float64

	// DuplicateCoverage is the minimum intersection-over-smaller-area needed
	// to treat two boxes as the same OCR position. A zero value selects 0.80;
	// a negative value disables duplicate grouping.
	DuplicateCoverage float64

	// MaxWords limits the number of distinct words. Zero means MaxWords;
	// values above MaxWords are rejected.
	MaxWords int
}

// DefaultOptions returns options suitable for ordinary OCR boxes. The baseline
// threshold is caller-specific and defaults to zero, so callers should
// normally set it explicitly.
func DefaultOptions() Options {
	return Options{
		MaxOverlapRatio:   0.25,
		MaxGapRatio:       0.25,
		DuplicateCoverage: 0.80,
		MaxWords:          MaxWords,
	}
}

// Line is one reconstructed text line. Words are ordered by horizontal center.
type Line struct {
	Words    []Word
	Baseline float64
	Cost     float64
}

// Text joins the line's word texts with single spaces.
func (l Line) Text() string {
	out := ""
	for i, w := range l.Words {
		if i > 0 {
			out += " "
		}
		out += w.Text
	}
	return out
}

// WordIDs returns the identifiers of the line's words in x-center order.
func (l Line) WordIDs() []string {
	ids := make([]string, len(l.Words))
	for i, w := range l.Words {
		ids[i] = w.ID
	}
	return ids
}

// DuplicateGroup is the retained evidence for one same-position OCR group.
// Candidates are normalized and sorted by descending confidence.
type DuplicateGroup struct {
	Selected   Word
	Candidates []Word
	Bounds     Bounds
}

// Result is the line reconstruction result.
type Result struct {
	Lines []Line

	// Cost is the global objective:
	// line count * LinePenalty + sum of squared adjacent baseline distances.
	Cost float64

	// Words contains the selected, normalized words in their input order.
	Words []Word

	// Duplicates contains one entry per group of two or more same-position
	// candidates. Singletons are not reported.
	Duplicates []DuplicateGroup

	// CandidateLineCount records how many valid line subsets were examined by
	// the exact search. It is useful for diagnostics, not part of the cost.
	CandidateLineCount int

	// PageRotationDegrees is the rotation that was removed.
	PageRotationDegrees float64
}

// Layout is an alias for Result.
type Layout = Result

// BuildLines reconstructs lines from OCR word boxes.
func BuildLines(words []Word, opts Options) (Result, error) {
	cfg, err := normalizeOptions(opts)
	if err != nil {
		return Result{}, err
	}

	normalized, err := normalizeWords(words, cfg.PageRotationDegrees)
	if err != nil {
		return Result{}, err
	}

	selected, groups, err := deduplicate(normalized, cfg)
	if err != nil {
		return Result{}, err
	}
	if len(selected) > cfg.MaxWords {
		return Result{}, fmt.Errorf("%w: got %d after duplicate removal, limit %d",
			ErrTooManyWords, len(selected), cfg.MaxWords)
	}
	if len(selected) == 0 {
		return Result{
			Lines:               []Line{},
			Words:               []Word{},
			Duplicates:          groups,
			PageRotationDegrees: cfg.PageRotationDegrees,
		}, nil
	}

	ordered, lineMasks, cost, candidateCount := partition(selected, cfg)
	resultLines := make([]Line, 0, len(lineMasks))
	for _, mask := range lineMasks {
		resultLines = append(resultLines, makeLine(ordered, mask, cfg.LinePenalty))
	}
	sortLines(resultLines)

	wordsInInputOrder := selectedWords(selected)

	return Result{
		Lines:               resultLines,
		Cost:                cost,
		Words:               wordsInInputOrder,
		Duplicates:          groups,
		CandidateLineCount:  candidateCount,
		PageRotationDegrees: cfg.PageRotationDegrees,
	}, nil
}

// Build is a short alias for BuildLines.
func Build(words []Word, opts Options) (Result, error) {
	return BuildLines(words, opts)
}

// BuildLineStructure is a descriptive alias for BuildLines.
func BuildLineStructure(words []Word, opts Options) (Result, error) {
	return BuildLines(words, opts)
}
