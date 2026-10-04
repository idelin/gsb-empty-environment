// Package estimator finds the shortest periodic pattern that best explains
// a discrete signal containing unknown samples.
package estimator

import (
	"errors"
	"fmt"
	"sort"
)

// Unknown is the sentinel reported for pattern positions that no known sample
// constrains. Unknown input samples are never treated as zero (or any other
// value); they simply do not vote.
const Unknown = -1

// Signal is a discrete sequence in which some positions may be unknown.
type Signal struct {
	Values []int  // sample values; meaningful only where Known[i] is true
	Known  []bool // Known[i] reports whether Values[i] is observed
}

// NewSignal builds a Signal from values, treating Unknown entries as unknown.
func NewSignal(values []int) Signal {
	s := Signal{Values: make([]int, len(values)), Known: make([]bool, len(values))}
	for i, v := range values {
		s.Values[i] = v
		s.Known[i] = v != Unknown
	}
	return s
}

// Result describes the best periodic explanation of a signal.
type Result struct {
	Period       int   // chosen period, in [PMin, PMax]
	Anomalies    int   // known samples that disagree with the pattern
	Pattern      []int // one period; Unknown where unconstrained
	UnknownPos   []int // indices within the pattern that remain unknown
	AnomalyPos   []int // signal indices of the anomalous known samples
}

// Estimate searches periods in [pMin, pMax] for the pattern that best
// explains the known samples of s. At most maxAnomalies disagreeing known
// samples are tolerated; pass a negative value for no limit.
//
// Candidates are ordered by (fewest anomalies, shortest period). The
// shortest-period tie-break resolves period-multiple ambiguity: if a signal
// is explained by period p it is also explained by 2p, 3p, ..., and the
// fundamental period wins. An all-unknown signal is explained by every
// period with zero anomalies, so the shortest candidate period is returned
// with a fully unknown pattern.
func Estimate(s Signal, pMin, pMax, maxAnomalies int) (Result, error) {
	n := len(s.Values)
	if len(s.Known) != n {
		return Result{}, errors.New("estimator: Values and Known length mismatch")
	}
	if n == 0 {
		return Result{}, errors.New("estimator: empty signal")
	}
	if pMin < 1 {
		return Result{}, fmt.Errorf("estimator: pMin must be >= 1, got %d", pMin)
	}
	if pMax > n {
		pMax = n // periods beyond the signal length are unconstrained
	}
	if pMin > pMax {
		return Result{}, fmt.Errorf("estimator: empty period range [%d, %d]", pMin, pMax)
	}

	best := Result{}
	found := false
	for p := pMin; p <= pMax; p++ {
		cand := explain(s, p)
		if maxAnomalies >= 0 && cand.Anomalies > maxAnomalies {
			continue
		}
		if !found || cand.Anomalies < best.Anomalies {
			best, found = cand, true
		}
		// Equal anomalies keep the earlier (shorter) period.
	}
	if !found {
		return Result{}, fmt.Errorf("estimator: no period in [%d, %d] explains the signal with <= %d anomalies", pMin, pMax, maxAnomalies)
	}
	return best, nil
}

// explain builds the pattern of length p that minimises disagreements with
// the known samples: each residue class votes for its value by majority.
func explain(s Signal, p int) Result {
	counts := make([]map[int]int, p)
	for i := range counts {
		counts[i] = make(map[int]int)
	}
	for i, v := range s.Values {
		if s.Known[i] {
			counts[i%p][v]++
		}
	}

	res := Result{Period: p, Pattern: make([]int, p)}
	for r := 0; r < p; r++ {
		if len(counts[r]) == 0 {
			res.Pattern[r] = Unknown
			res.UnknownPos = append(res.UnknownPos, r)
			continue
		}
		res.Pattern[r] = majority(counts[r])
	}
	for i, v := range s.Values {
		if s.Known[i] && v != res.Pattern[i%p] {
			res.Anomalies++
			res.AnomalyPos = append(res.AnomalyPos, i)
		}
	}
	return res
}

// majority returns the most frequent value; ties break to the smallest value
// for determinism.
func majority(counts map[int]int) int {
	keys := make([]int, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	best, bestN := keys[0], counts[keys[0]]
	for _, k := range keys[1:] {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best
}
