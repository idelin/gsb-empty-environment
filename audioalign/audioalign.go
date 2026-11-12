// Package audioalign aligns two PCM recordings whose clocks drift slowly
// relative to each other.
//
// The analysis runs on overlapping windows. Every non-silent window produces
// candidate delays from normalized cross-correlation (NCC). A dynamic
// program then picks a delay curve over the whole recording: each window
// costs 1 minus its correlation, plus a smoothing weight times the absolute
// delay change against the previous active window, and the per-window drift
// velocity is bounded by Config.MaxDrift.
//
// The package never modifies the input audio: it only reports rational
// sample offsets at window centers and a per-window confidence difference.
package audioalign

import (
	"errors"
	"fmt"
	"math"
)

// Errors returned by Align.
var (
	// ErrNoActiveWindows is returned when every window is silent or has no
	// overlapping samples between the two tracks.
	ErrNoActiveWindows = errors.New("audioalign: no non-silent overlapping windows")

	// ErrInvalidConfig is returned, possibly wrapped, for a bad Config.
	ErrInvalidConfig = errors.New("audioalign: invalid configuration")
)

// Rational is an exact rational sample offset. Den is always positive.
// Integer delays use Den == 1; sub-sample delays are quantized to
// Config.SubsampleQ (and reduced by their greatest common divisor).
type Rational struct {
	Num int64
	Den int64
}

// Float evaluates the rational offset as a floating-point sample count.
func (r Rational) Float() float64 {
	return float64(r.Num) / float64(r.Den)
}

// String implements fmt.Stringer.
func (r Rational) String() string {
	if r.Den == 1 {
		return fmt.Sprintf("%d", r.Num)
	}
	return fmt.Sprintf("%d/%d", r.Num, r.Den)
}

func reduceRational(num, den int64) Rational {
	if den <= 0 {
		panic("audioalign: non-positive rational denominator")
	}
	if num == 0 {
		return Rational{Num: 0, Den: 1}
	}
	sign := int64(1)
	if num < 0 {
		sign = -1
		num = -num
	}
	g := gcd64(num, den)
	return Rational{Num: sign * num / g, Den: den / g}
}

func gcd64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Config controls the alignment analysis. Zero-valued numeric fields are
// replaced by documented defaults when Align runs.
type Config struct {
	// WindowSize is the analysis window length in samples. Default 1024.
	WindowSize int

	// HopSize is the distance between consecutive window centers in
	// samples. Default WindowSize / 4.
	HopSize int

	// MinLag and MaxLag bound the searched delay of the delayed track
	// relative to the reference track, in samples. Both are inclusive.
	// A positive lag means the delayed track lags behind the reference.
	MinLag int
	MaxLag int

	// SilenceRMS skips a window whenever the mean-removed RMS of either
	// track falls below the threshold. The scale matches the input PCM
	// scale. Default 1e-5.
	SilenceRMS float64

	// SmoothWeight is multiplied by the absolute delay difference between
	// adjacent active windows and added to the path cost. Default 0.2.
	SmoothWeight float64

	// MaxDrift bounds |delta lag| per hop between adjacent active windows.
	// For windows separated by h hops the bound is MaxDrift*h samples.
	// Default 1.0.
	MaxDrift float64

	// RefineSubsample enables parabolic sub-sample refinement around each
	// integer NCC peak. When false, every reported offset is an exact
	// integer rational. Default false.
	RefineSubsample bool

	// SubsampleQ is the denominator used to quantize refined sub-sample
	// offsets. Default 8 (eighth-sample resolution).
	SubsampleQ int64

	// MaxPaths caps how many equally optimal paths are reported. If more
	// optimal paths exist, Result.Truncated is set. Default 64.
	MaxPaths int

	// CorrEpsilon treats two NCC values at distinct lags as tied for the
	// ambiguity flag when their difference is at most this value.
	// Default 1e-9.
	CorrEpsilon float64

	// CostEpsilon is the relative tolerance used while merging equal-cost
	// DP transitions and final paths. Default 1e-9.
	CostEpsilon float64
}

func (cfg *Config) setDefaults() {
	if cfg.WindowSize == 0 {
		cfg.WindowSize = 1024
	}
	if cfg.HopSize == 0 {
		cfg.HopSize = cfg.WindowSize / 4
		if cfg.HopSize == 0 {
			cfg.HopSize = 1
		}
	}
	if cfg.SilenceRMS == 0 {
		cfg.SilenceRMS = 1e-5
	}
	if cfg.SmoothWeight == 0 {
		cfg.SmoothWeight = 0.2
	}
	if cfg.MaxDrift == 0 {
		cfg.MaxDrift = 1.0
	}
	if cfg.SubsampleQ == 0 {
		cfg.SubsampleQ = 8
	}
	if cfg.MaxPaths == 0 {
		cfg.MaxPaths = 64
	}
	if cfg.CorrEpsilon == 0 {
		cfg.CorrEpsilon = 1e-9
	}
	if cfg.CostEpsilon == 0 {
		cfg.CostEpsilon = 1e-9
	}
}

func (cfg Config) validate() error {
	switch {
	case cfg.WindowSize <= 0:
		return fmt.Errorf("%w: WindowSize must be positive", ErrInvalidConfig)
	case cfg.HopSize <= 0:
		return fmt.Errorf("%w: HopSize must be positive", ErrInvalidConfig)
	case cfg.MinLag > cfg.MaxLag:
		return fmt.Errorf("%w: MinLag must be <= MaxLag", ErrInvalidConfig)
	case cfg.SilenceRMS < 0:
		return fmt.Errorf("%w: SilenceRMS must be non-negative", ErrInvalidConfig)
	case cfg.SmoothWeight < 0:
		return fmt.Errorf("%w: SmoothWeight must be non-negative", ErrInvalidConfig)
	case cfg.MaxDrift < 0:
		return fmt.Errorf("%w: MaxDrift must be non-negative", ErrInvalidConfig)
	case cfg.SubsampleQ <= 0:
		return fmt.Errorf("%w: SubsampleQ must be positive", ErrInvalidConfig)
	case cfg.MaxPaths <= 0:
		return fmt.Errorf("%w: MaxPaths must be positive", ErrInvalidConfig)
	}
	return nil
}

// Candidate is one NCC-derived delay evaluated on a window.
type Candidate struct {
	// Lag is the rational delay of the delayed track at this candidate.
	Lag Rational

	// Corr is the normalized cross-correlation in [-1, 1].
	Corr float64

	// Cost is 1 - Corr, the local window cost of this candidate.
	Cost float64
}

// Window reports the analysis of one active (non-silent) window.
type Window struct {
	// Index is the zero-based window index over the whole stream,
	// including skipped windows.
	Index int

	// Center is the sample position of the window center.
	Center Rational

	// Candidates are the feasible lags ordered by ascending lag.
	Candidates []Candidate

	// BestLag is the rational lag with the highest raw NCC, before the
	// global dynamic program is applied.
	BestLag Rational

	// BestCorr is the NCC at BestLag.
	BestCorr float64

	// Confidence is the confidence difference: BestCorr minus the NCC of
	// the second-best distinct lag. A missing alternative is treated as
	// correlation -1. Values near zero mean the window alone is ambiguous.
	Confidence float64

	// Ambiguous reports whether the best and second-best lags are tied
	// within Config.CorrEpsilon.
	Ambiguous bool
}

// Path is one globally optimal delay curve. Lags align one-to-one with
// Result.Windows, i.e. Path.Lags[j] is the delay at Result.Windows[j].
type Path struct {
	// Lags holds one rational delay per active window.
	Lags []Rational

	// Cost is the total DP cost of the curve.
	Cost float64
}

// Result is the output of Align. The input audio is never modified.
type Result struct {
	// Windows holds only active windows, in stream order.
	Windows []Window

	// Skipped holds the indices of silent or non-overlapping windows.
	Skipped []int

	// Paths holds all globally optimal delay curves found, up to
	// Config.MaxPaths. Multiple entries express unresolved ambiguity.
	Paths []Path

	// Truncated is true when more optimal paths existed than MaxPaths.
	Truncated bool

	// Config is the resolved configuration (defaults applied).
	Config Config
}

// Align aligns delayed against ref and returns window-center offsets.
// Both inputs are mono PCM samples on the same numeric scale; they may
// have different lengths. The slices are read only and never modified.
func Align(ref, delayed []float64, cfg Config) (*Result, error) {
	if len(ref) == 0 || len(delayed) == 0 {
		return nil, fmt.Errorf("%w: empty input track", ErrInvalidConfig)
	}
	cfg.setDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	res := &Result{Config: cfg}
	type frame struct {
		window Window
		lags   []int
		costs  []float64
		center int
	}
	frames := make([]frame, 0)

	half := cfg.WindowSize / 2
	index := 0
	for start := 0; start+cfg.WindowSize <= len(ref); start += cfg.HopSize {
		center := start + half
		corr, active := windowCorrelations(ref, delayed, start, cfg)
		if !active {
			res.Skipped = append(res.Skipped, index)
			index++
			continue
		}

		lags := make([]int, 0, len(corr))
		for lag := range corr {
			lags = append(lags, lag)
		}
		sortInts(lags)

		cands := make([]Candidate, 0, len(lags))
		byLag := make(map[int]Rational, len(lags))
		bestIdx := 0
		for i, lag := range lags {
			r := rationalForLag(lag, corr, cfg)
			byLag[lag] = r
			cands = append(cands, Candidate{Lag: r, Corr: corr[lag], Cost: 1 - corr[lag]})
			if corr[lag] > corr[lags[bestIdx]] {
				bestIdx = i
			}
		}

		secondCorr := -1.0
		for i, lag := range lags {
			if i != bestIdx && corr[lag] > secondCorr {
				secondCorr = corr[lag]
			}
		}
		bestCorr := corr[lags[bestIdx]]
		win := Window{
			Index:      index,
			Center:     Rational{Num: int64(center), Den: 1},
			Candidates: cands,
			BestLag:    byLag[lags[bestIdx]],
			BestCorr:   bestCorr,
			Confidence: bestCorr - secondCorr,
			Ambiguous:  len(lags) >= 2 && bestCorr-secondCorr <= cfg.CorrEpsilon,
		}
		frames = append(frames, frame{
			window: win,
			lags:   lags,
			costs:  candidateCosts(lags, corr),
			center: center,
		})
		index++
	}

	if len(frames) == 0 {
		return nil, ErrNoActiveWindows
	}

	lagSets := make([][]int, len(frames))
	costSets := make([][]float64, len(frames))
	centers := make([]int, len(frames))
	res.Windows = make([]Window, len(frames))
	for i, f := range frames {
		lagSets[i] = f.lags
		costSets[i] = f.costs
		centers[i] = f.center
		res.Windows[i] = f.window
	}

	rawPaths, truncated, err := solveDP(lagSets, costSets, centers, cfg)
	if err != nil {
		return nil, err
	}
	res.Truncated = truncated
	res.Paths = make([]Path, 0, len(rawPaths))
	for _, raw := range rawPaths {
		p := Path{Lags: make([]Rational, len(raw.lags)), Cost: raw.cost}
		for j, lag := range raw.lags {
			p.Lags[j] = frames[j].window.Candidates[findLag(frames[j].lags, lag)].Lag
		}
		res.Paths = append(res.Paths, p)
	}
	return res, nil
}

func candidateCosts(lags []int, corr map[int]float64) []float64 {
	costs := make([]float64, len(lags))
	for i, lag := range lags {
		costs[i] = 1 - corr[lag]
	}
	return costs
}

func findLag(lags []int, lag int) int {
	for i, v := range lags {
		if v == lag {
			return i
		}
	}
	return -1
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

// rationalForLag maps an integer lag to its reported rational offset,
// applying parabolic sub-sample refinement when enabled.
func rationalForLag(lag int, corr map[int]float64, cfg Config) Rational {
	if !cfg.RefineSubsample {
		return Rational{Num: int64(lag), Den: 1}
	}
	delta := parabolicDelta(lag, corr)
	num := int64(lag)*cfg.SubsampleQ + int64(math.Round(delta*float64(cfg.SubsampleQ)))
	return reduceRational(num, cfg.SubsampleQ)
}
