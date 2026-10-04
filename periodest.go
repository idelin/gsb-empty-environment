// Package periodest estimates the period of a discrete signal that may
// contain unknown samples. It searches a finite range of candidate periods
// for the pattern that best explains the known samples, allowing a bounded
// number of anomalies.
package periodest

import (
	"errors"
	"fmt"
)

// Unknown marks a sample whose value was not observed. Known samples must be
// non-negative integers. Unknown samples are skipped during estimation and
// are never treated as the value 0.
const Unknown = -1

// Config controls the period search.
type Config struct {
	// MinPeriod is the smallest candidate period; must be >= 1.
	MinPeriod int
	// MaxPeriod is the largest candidate period; must be >= MinPeriod.
	MaxPeriod int
	// MaxAnomalies caps how many known samples may disagree with the
	// inferred pattern for a candidate to be acceptable. A negative value
	// means no cap.
	MaxAnomalies int
}

// Result describes the best explanation found for a signal.
type Result struct {
	// Period is the chosen period: the one with the fewest anomalies,
	// ties broken towards the shortest period.
	Period int
	// Anomalies is the number of known samples that disagree with Pattern.
	Anomalies int
	// Pattern has length Period. Pattern[r] is the majority value among
	// known samples at indices congruent to r modulo Period, or Unknown
	// when no known sample informs that phase.
	Pattern []int
	// UnknownPositions lists, in ascending order, the phases of Pattern
	// that remain unknown because no known sample exists at any index
	// congruent to the phase modulo Period.
	UnknownPositions []int
}

// ErrNoSolution is returned when no period in [MinPeriod, MaxPeriod] fits
// within the anomaly allowance of Config.MaxAnomalies.
var ErrNoSolution = errors.New("periodest: no period within the anomaly allowance")

// Estimate finds the period and pattern that best explain the known samples
// of signal. Candidates are ranked by fewest anomalies first, then by
// shortest period, which resolves period-multiple ambiguity in favour of
// the fundamental period.
func Estimate(signal []int, cfg Config) (Result, error) {
	if cfg.MinPeriod < 1 {
		return Result{}, fmt.Errorf("periodest: MinPeriod must be >= 1, got %d", cfg.MinPeriod)
	}
	if cfg.MaxPeriod < cfg.MinPeriod {
		return Result{}, fmt.Errorf("periodest: MaxPeriod (%d) must be >= MinPeriod (%d)", cfg.MaxPeriod, cfg.MinPeriod)
	}
	for i, v := range signal {
		if v < 0 && v != Unknown {
			return Result{}, fmt.Errorf("periodest: sample %d has invalid value %d (use Unknown = %d for unobserved samples)", i, v, Unknown)
		}
	}

	var best Result
	found := false
	for p := cfg.MinPeriod; p <= cfg.MaxPeriod; p++ {
		anomalies, pattern, unknown := explain(signal, p)
		if cfg.MaxAnomalies >= 0 && anomalies > cfg.MaxAnomalies {
			continue
		}
		// Strictly fewer anomalies takes the lead; on ties the smaller
		// period keeps it, since periods are scanned in ascending order.
		if !found || anomalies < best.Anomalies {
			best = Result{Period: p, Anomalies: anomalies, Pattern: pattern, UnknownPositions: unknown}
			found = true
		}
	}
	if !found {
		return Result{}, ErrNoSolution
	}
	return best, nil
}

// explain fits the best length-p pattern to the known samples of signal and
// reports the anomaly count together with the phases left unknown.
func explain(signal []int, p int) (anomalies int, pattern []int, unknown []int) {
	counts := make([]map[int]int, p)
	for i, v := range signal {
		if v == Unknown {
			continue // unknown samples carry no evidence; never read as 0
		}
		r := i % p
		if counts[r] == nil {
			counts[r] = make(map[int]int)
		}
		counts[r][v]++
	}

	pattern = make([]int, p)
	unknown = make([]int, 0)
	for r := 0; r < p; r++ {
		if len(counts[r]) == 0 {
			pattern[r] = Unknown
			unknown = append(unknown, r)
			continue
		}
		// Majority value wins; ties resolve to the smallest value so the
		// result is deterministic regardless of map iteration order.
		bestVal, bestCount := -1, -1
		for v, c := range counts[r] {
			if c > bestCount || (c == bestCount && v < bestVal) {
				bestVal, bestCount = v, c
			}
		}
		pattern[r] = bestVal
		for v, c := range counts[r] {
			if v != bestVal {
				anomalies += c
			}
		}
	}
	return anomalies, pattern, unknown
}
