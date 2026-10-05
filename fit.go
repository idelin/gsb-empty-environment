// Package beatfit fits an integer-period beat grid to a noisy tap time
// series that may contain missed beats and extra taps.
//
// For every candidate (period, offset) the package computes an
// order-preserving alignment between the taps and the grid points that
// minimizes:
//
//	sum |tap - gridPoint|  (matched pairs)
//	+ missPenalty  * number of unmatched grid points (missed beats)
//	+ extraPenalty * number of unmatched taps       (extra taps)
//
// The global optimum is found by exhaustive search over the finite
// candidate set, not by voting on adjacent time differences.
package beatfit

import (
	"math"
	"sort"
)

// Candidate is one grid hypothesis: points at Offset + k*Period (k integer).
type Candidate struct {
	Period int     `json:"period"`
	Offset float64 `json:"offset"`
}

// Params holds the alignment penalties.
type Params struct {
	MissPenalty  float64 `json:"missPenalty"`  // cost per grid beat with no tap
	ExtraPenalty float64 `json:"extraPenalty"` // cost per tap matched to no beat
}

// Match pairs one tap with one grid beat.
type Match struct {
	TapIndex  int     `json:"tapIndex"` // index into the sorted tap list
	Beat      int     `json:"beat"`     // grid index k (gridTime = Offset + k*Period)
	TapTime   float64 `json:"tapTime"`
	GridTime  float64 `json:"gridTime"`
	Deviation float64 `json:"deviation"` // TapTime - GridTime
}

// Solution is the optimal alignment for one candidate grid.
type Solution struct {
	Period      int     `json:"period"`
	Offset      float64 `json:"offset"`
	Cost        float64 `json:"cost"`
	Matches     []Match `json:"matches"`
	ExtraTaps   []int   `json:"extraTaps"`   // tap indices with no beat
	MissedBeats []int   `json:"missedBeats"` // beat indices k with no tap
}

// Result is the outcome of a global fit.
type Result struct {
	BestCost  float64    `json:"bestCost"`
	Periods   []int      `json:"periods"`   // every period tied at BestCost
	Solutions []Solution `json:"solutions"` // best solution per tied period
}

// IntegerCandidates enumerates every integer period in [pMin, pMax]
// with every integer offset in [0, period).
func IntegerCandidates(pMin, pMax int) []Candidate {
	var out []Candidate
	for p := pMin; p <= pMax; p++ {
		for s := 0; s < p; s++ {
			out = append(out, Candidate{Period: p, Offset: float64(s)})
		}
	}
	return out
}

// FitAll searches all candidates and returns the best cost, every period
// tied at that cost, and the best solution for each tied period.
func FitAll(times []float64, cands []Candidate, pr Params) Result {
	sorted := sortedCopy(times)
	best := math.Inf(1)
	bestByPeriod := map[int]Solution{}
	var order []int
	for _, c := range cands {
		if c.Period <= 0 {
			continue
		}
		sol := FitCandidate(sorted, c, pr)
		cur, ok := bestByPeriod[c.Period]
		if !ok {
			order = append(order, c.Period)
			bestByPeriod[c.Period] = sol
		} else if sol.Cost < cur.Cost {
			bestByPeriod[c.Period] = sol
		}
		if sol.Cost < best {
			best = sol.Cost
		}
	}
	res := Result{BestCost: best}
	const tol = 1e-9
	for _, p := range order {
		if sol := bestByPeriod[p]; sol.Cost <= best+tol {
			res.Periods = append(res.Periods, p)
			res.Solutions = append(res.Solutions, sol)
		}
	}
	sort.Ints(res.Periods)
	sort.Slice(res.Solutions, func(i, j int) bool {
		return res.Solutions[i].Period < res.Solutions[j].Period
	})
	return res
}

// FitCandidate computes the minimum-cost order-preserving alignment of
// the taps against one grid via dynamic programming (edit-distance style:
// match / skip tap / skip beat). times must be sorted ascending.
func FitCandidate(times []float64, cand Candidate, pr Params) Solution {
	sol := Solution{Period: cand.Period, Offset: cand.Offset}
	n := len(times)
	if n == 0 {
		return sol
	}

	// A matched pair never helps if its deviation exceeds the cost of
	// skipping both sides, so only grid points within that margin of the
	// observed range can appear in an optimal alignment.
	margin := pr.MissPenalty + pr.ExtraPenalty
	p := float64(cand.Period)
	kMin := int(math.Ceil((times[0]-margin-cand.Offset)/p - 1e-12))
	kMax := int(math.Floor((times[n-1]+margin-cand.Offset)/p + 1e-12))
	m := kMax - kMin + 1
	if m <= 0 {
		sol.Cost = float64(n) * pr.ExtraPenalty
		sol.ExtraTaps = make([]int, n)
		for i := range sol.ExtraTaps {
			sol.ExtraTaps[i] = i
		}
		return sol
	}
	grid := make([]float64, m)
	for j := 0; j < m; j++ {
		grid[j] = cand.Offset + float64(kMin+j)*p
	}

	// dp[i][j]: min cost aligning times[:i] with grid[:j].
	const (
		opMatch = iota
		opExtra // tap i-1 is an extra tap
		opMiss  // grid j-1 is a missed beat
	)
	dp := make([][]float64, n+1)
	back := make([][]uint8, n+1)
	for i := range dp {
		dp[i] = make([]float64, m+1)
		back[i] = make([]uint8, m+1)
	}
	for i := 1; i <= n; i++ {
		dp[i][0] = float64(i) * pr.ExtraPenalty
		back[i][0] = opExtra
	}
	for j := 1; j <= m; j++ {
		dp[0][j] = float64(j) * pr.MissPenalty
		back[0][j] = opMiss
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			match := dp[i-1][j-1] + math.Abs(times[i-1]-grid[j-1])
			extra := dp[i-1][j] + pr.ExtraPenalty
			miss := dp[i][j-1] + pr.MissPenalty
			best, op := match, uint8(opMatch)
			if extra < best {
				best, op = extra, opExtra
			}
			if miss < best {
				best, op = miss, opMiss
			}
			dp[i][j] = best
			back[i][j] = op
		}
	}

	sol.Cost = dp[n][m]
	for i, j := n, m; i > 0 || j > 0; {
		switch back[i][j] {
		case opMatch:
			sol.Matches = append(sol.Matches, Match{
				TapIndex:  i - 1,
				Beat:      kMin + j - 1,
				TapTime:   times[i-1],
				GridTime:  grid[j-1],
				Deviation: times[i-1] - grid[j-1],
			})
			i--
			j--
		case opExtra:
			sol.ExtraTaps = append(sol.ExtraTaps, i-1)
			i--
		default: // opMiss
			sol.MissedBeats = append(sol.MissedBeats, kMin+j-1)
			j--
		}
	}
	reverseMatches(sol.Matches)
	reverseInts(sol.ExtraTaps)
	reverseInts(sol.MissedBeats)
	return sol
}

func sortedCopy(times []float64) []float64 {
	out := make([]float64, len(times))
	copy(out, times)
	sort.Float64s(out)
	return out
}

func reverseMatches(s []Match) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func reverseInts(s []int) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
