package taskpkg

import (
	"math/big"
)

// Segment describes one continuous, rational, linear piece of a playback
// mapping. The target interval is the output-timeline domain. The source
// interval is the media-timeline range visited while that target interval
// plays. SourceStart == SourceEnd denotes a freeze; SourceEnd < SourceStart
// denotes reverse playback.
//
// Segments supplied to NewTimeMap must be ordered, contiguous, and
// continuous. Use NewRat to construct endpoints without floating-point
// conversion.
type Segment struct {
	TargetStart *big.Rat
	TargetEnd   *big.Rat
	SourceStart *big.Rat
	SourceEnd   *big.Rat
}

// Subtitle is a source-timeline subtitle. Start and End are half-open:
// the subtitle is visible for source times in [Start, End).
type Subtitle struct {
	ID    string
	Text  string
	Start *big.Rat
	End   *big.Rat
}

// Interval is a half-open interval on the target timeline: [Start, End).
type Interval struct {
	Start *big.Rat
	End   *big.Rat
}

// Cue is one visible occurrence of a subtitle on the target timeline. ID and
// Text are copied unchanged from the source Subtitle.
type Cue struct {
	ID    string
	Text  string
	Start *big.Rat
	End   *big.Rat
}

// RetimedSubtitle keeps all target intervals for one source subtitle together.
type RetimedSubtitle struct {
	ID        string
	Text      string
	Intervals []Interval
}

// TargetInterval and RetimedCue are descriptive aliases for callers that
// prefer names matching the target timeline.
type TargetInterval = Interval
type RetimedCue = Cue

// NewRat returns an exact rational number. A zero denominator causes a
// panic, matching big.NewRat.
func NewRat(numerator, denominator int64) *big.Rat {
	return big.NewRat(numerator, denominator)
}

func cloneRat(r *big.Rat) *big.Rat {
	if r == nil {
		return nil
	}
	return new(big.Rat).Set(r)
}

func ratCmp(a, b *big.Rat) int {
	return a.Cmp(b)
}
