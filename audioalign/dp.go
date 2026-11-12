package audioalign

import (
	"errors"
	"math"
)

// ErrNoFeasiblePath is returned when MaxDrift rules out every transition
// between consecutive active windows.
var ErrNoFeasiblePath = errors.New("audioalign: no delay curve satisfies the drift velocity limit")

const infCost = math.MaxFloat64 / 4

type rawPath struct {
	lags []int
	cost float64
}

// solveDP runs the Viterbi-style dynamic program.
//
// The local cost of candidate i at window j is costSets[j][i] = 1 - corr.
// A transition from previous lag p to current lag l adds
// SmoothWeight * |l - p| and is allowed only when
// |l - p| <= MaxDrift * hopDist. Transitions and final states whose costs
// are equal within Config.CostEpsilon are all retained, so every optimal
// path can be reported.
func solveDP(lagSets [][]int, costSets [][]float64, centers []int, cfg Config) ([]rawPath, bool, error) {
	n := len(lagSets)

	// best[j][i] is the minimal total cost ending on candidate i of window j.
	best := make([][]float64, n)
	// preds[j][i] lists candidate indices in window j-1 that attain best[j][i].
	preds := make([][][]int, n)

	best[0] = append(best[0], costSets[0]...)
	preds[0] = make([][]int, len(lagSets[0]))

	for j := 1; j < n; j++ {
		best[j] = make([]float64, len(lagSets[j]))
		preds[j] = make([][]int, len(lagSets[j]))
		hopDist := (centers[j] - centers[j-1]) / cfg.HopSize
		bound := cfg.MaxDrift * float64(hopDist)

		for i, lag := range lagSets[j] {
			minCost := infCost
			var tied []int
			for k, prevLag := range lagSets[j-1] {
				delta := math.Abs(float64(lag - prevLag))
				if delta > bound {
					continue
				}
				c := best[j-1][k] + cfg.SmoothWeight*delta + costSets[j][i]
				switch {
				case minCost == infCost || (c < minCost && !costsTie(c, minCost, cfg.CostEpsilon)):
					minCost = c
					tied = []int{k}
				case minCost != infCost && costsTie(c, minCost, cfg.CostEpsilon):
					tied = append(tied, k)
				}
			}
			if tied == nil {
				best[j][i] = infCost
				continue
			}
			best[j][i] = minCost
			preds[j][i] = tied
		}
	}

	finalCost := infCost
	var finalIdx []int
	for i, c := range best[n-1] {
		if c >= infCost {
			continue
		}
		switch {
		case finalCost == infCost || (c < finalCost && !costsTie(c, finalCost, cfg.CostEpsilon)):
			finalCost = c
			finalIdx = []int{i}
		case finalCost != infCost && costsTie(c, finalCost, cfg.CostEpsilon):
			finalIdx = append(finalIdx, i)
		}
	}
	if finalIdx == nil {
		// Unreachable: Align guarantees active windows, but a tight drift
		// limit can make every transition infeasible.
		return nil, false, ErrNoFeasiblePath
	}

	enumerator := pathEnumerator{
		lagSets: lagSets,
		preds:   preds,
		max:     cfg.MaxPaths,
	}
	lags, truncated := enumerator.enumerate(finalIdx)
	paths := make([]rawPath, len(lags))
	for i, l := range lags {
		paths[i] = rawPath{lags: l, cost: finalCost}
	}
	return paths, truncated, nil
}

// costsTie compares costs with a relative tolerance scaled by the values
// themselves, with an absolute floor so tiny costs still compare sanely.
func costsTie(a, b, relEps float64) bool {
	scale := math.Max(1, math.Min(math.Abs(a), math.Abs(b)))
	return math.Abs(a-b) <= relEps*scale
}

type pathEnumerator struct {
	lagSets   [][]int
	preds     [][][]int
	max       int
	collected [][]int
	truncated bool
}

// enumerate returns up to cfg.MaxPaths full lag sequences, expanding the
// tied final indices and every tied predecessor. Traversal order follows
// ascending lag so the result is deterministic.
func (e *pathEnumerator) enumerate(finalIdx []int) ([][]int, bool) {
	n := len(e.lagSets)
	for _, idx := range finalIdx {
		if len(e.collected) >= e.max {
			e.truncated = true
			break
		}
		seq := make([]int, n)
		e.dfs(n-1, idx, seq)
	}
	return e.collected, e.truncated
}

func (e *pathEnumerator) dfs(j, i int, seq []int) {
	if e.truncated {
		return
	}
	seq[j] = e.lagSets[j][i]
	if j == 0 {
		if len(e.collected) >= e.max {
			e.truncated = true
			return
		}
		dup := make([]int, len(seq))
		copy(dup, seq)
		e.collected = append(e.collected, dup)
		return
	}
	for _, p := range e.preds[j][i] {
		if e.truncated {
			return
		}
		e.dfs(j-1, p, seq)
	}
}
