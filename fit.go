package main

import (
	"errors"
	"math"
	"sort"
)

// FitInput describes one beat-grid fitting problem.
//
// A grid is the set of times { Start + k*Period : k integer }.
// Taps are matched to grid points by an order-preserving alignment:
//   - a matched pair (tap t, grid point g) costs |t - g|;
//   - a grid point between the first and last matched grid point that is
//     left unmatched (a missed beat) costs MissPenalty;
//   - a tap left unmatched (an extra tap) costs ExtraPenalty.
// Grid points outside the span of matched beats are free.
type FitInput struct {
	Taps            []int64 // tap timestamps (need not be sorted)
	Periods         []int64 // candidate integer periods (> 0)
	Starts          []int64 // candidate integer start offsets
	SearchAllStarts bool    // if true, ignore Starts and try every start in [0, Period)
	MissPenalty     int64   // penalty per unmatched interior grid point
	ExtraPenalty    int64   // penalty per unmatched tap
}

// MatchPair is one matched tap/grid-point pair.
type MatchPair struct {
	TapIndex  int   `json:"tapIndex"`
	TapTime   int64 `json:"tapTime"`
	GridK     int64 `json:"gridK"` // k such that GridTime = Start + GridK*Period
	GridTime  int64 `json:"gridTime"`
	Deviation int64 `json:"deviation"` // |TapTime - GridTime|
}

// ExtraTap is a tap matched to no grid point.
type ExtraTap struct {
	TapIndex int   `json:"tapIndex"`
	TapTime  int64 `json:"tapTime"`
}

// GridResult is the optimal alignment for one grid.
type GridResult struct {
	Period      int64       `json:"period"`
	Start       int64       `json:"start"` // normalized into [0, Period)
	Cost        int64       `json:"cost"`
	Matches     []MatchPair `json:"matches"`
	MissedBeats []int64     `json:"missedBeats"` // unmatched grid times inside the matched span
	ExtraTaps   []ExtraTap  `json:"extraTaps"`
}

// PeriodTie records one period that attains the global minimum cost.
type PeriodTie struct {
	Period int64   `json:"period"`
	Cost   int64   `json:"cost"`
	Starts []int64 `json:"starts"` // all normalized starts attaining Cost for this period
}

// Solution is the full answer: one best grid with its alignment, plus every
// period tied at the minimum cost.
type Solution struct {
	BestCost    int64       `json:"bestCost"`
	Best        GridResult  `json:"best"`
	TiedPeriods []PeriodTie `json:"tiedPeriods"`
}

// Solve searches every candidate (period, start) grid and returns the
// minimum-cost alignment together with all tied periods.
func Solve(in FitInput) (Solution, error) {
	var sol Solution
	if in.MissPenalty < 0 || in.ExtraPenalty < 0 {
		return sol, errors.New("beatfit: penalties must be >= 0")
	}
	periods := uniqueSorted(in.Periods)
	if len(periods) == 0 {
		return sol, errors.New("beatfit: at least one candidate period is required")
	}
	for _, p := range periods {
		if p <= 0 {
			return sol, errors.New("beatfit: periods must be positive integers")
		}
	}

	taps := append([]int64(nil), in.Taps...)
	sort.Slice(taps, func(i, j int) bool { return taps[i] < taps[j] })

	sol.BestCost = math.MaxInt64
	for _, p := range periods {
		starts := in.Starts
		if in.SearchAllStarts {
			starts = make([]int64, p)
			for s := range starts {
				starts[s] = int64(s)
			}
		}
		if len(starts) == 0 {
			starts = []int64{0}
		}

		seen := make(map[int64]bool)
		var bestForP GridResult
		var tieStarts []int64
		first := true
		for _, s := range starts {
			sn := ((s % p) + p) % p // grids depend only on start mod period
			if seen[sn] {
				continue
			}
			seen[sn] = true

			var res GridResult
			if len(taps) == 0 {
				res = GridResult{Period: p, Start: sn, Cost: 0}
			} else {
				res = fitGrid(taps, p, sn, in.MissPenalty, in.ExtraPenalty)
			}
			switch {
			case first || res.Cost < bestForP.Cost:
				bestForP = res
				tieStarts = []int64{sn}
				first = false
			case res.Cost == bestForP.Cost:
				tieStarts = append(tieStarts, sn)
			}
		}
		sort.Slice(tieStarts, func(i, j int) bool { return tieStarts[i] < tieStarts[j] })

		switch {
		case bestForP.Cost < sol.BestCost:
			sol.BestCost = bestForP.Cost
			sol.Best = bestForP
			sol.TiedPeriods = []PeriodTie{{Period: p, Cost: bestForP.Cost, Starts: tieStarts}}
		case bestForP.Cost == sol.BestCost:
			sol.TiedPeriods = append(sol.TiedPeriods, PeriodTie{Period: p, Cost: bestForP.Cost, Starts: tieStarts})
		}
	}
	return sol, nil
}

// fitGrid finds the optimal order-preserving alignment between taps and the
// grid { start + k*period } via sequence-alignment dynamic programming.
//
// D[i][j] = min cost of aligning taps[:i] with the first j in-window grid
// points. D[0][j] = 0 makes grid points before the first match free, and
// taking min over D[n][j] makes grid points after the last match free.
func fitGrid(taps []int64, period, start, miss, extra int64) GridResult {
	n := len(taps)

	// A matched pair with deviation > extra+miss is never optimal: unmatching
	// the tap costs extra and the grid point adds at most miss. So only grid
	// points within that distance of the tap range can be matched.
	maxDev := extra + miss
	kmin := ceilDiv(taps[0]-maxDev-start, period)
	kmax := floorDiv(taps[n-1]+maxDev-start, period)
	m := int(kmax - kmin + 1)
	gridAt := func(j int) int64 { return start + (kmin+int64(j))*period }

	const (
		pDiag = 0 // match tap i-1 with grid point j-1
		pUp   = 1 // tap i-1 unmatched (extra)
		pLeft = 2 // grid point j-1 unmatched (missed beat)
	)
	cols := m + 1
	idx := func(i, j int) int { return i*cols + j }
	dist := make([]int64, (n+1)*cols)
	parent := make([]uint8, (n+1)*cols)

	for j := 0; j <= m; j++ {
		dist[idx(0, j)] = 0
		parent[idx(0, j)] = pLeft
	}
	for i := 1; i <= n; i++ {
		dist[idx(i, 0)] = int64(i) * extra
		parent[idx(i, 0)] = pUp
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			best := dist[idx(i-1, j-1)] + abs64(taps[i-1]-gridAt(j-1))
			p := uint8(pDiag)
			if c := dist[idx(i-1, j)] + extra; c < best {
				best, p = c, pUp
			}
			if c := dist[idx(i, j-1)] + miss; c < best {
				best, p = c, pLeft
			}
			dist[idx(i, j)] = best
			parent[idx(i, j)] = p
		}
	}

	bestJ := 0
	for j := 1; j <= m; j++ {
		if dist[idx(n, j)] < dist[idx(n, bestJ)] {
			bestJ = j
		}
	}

	res := GridResult{Period: period, Start: start, Cost: dist[idx(n, bestJ)]}
	for i, j := n, bestJ; i > 0; {
		switch parent[idx(i, j)] {
		case pDiag:
			g := gridAt(j - 1)
			res.Matches = append(res.Matches, MatchPair{
				TapIndex:  i - 1,
				TapTime:   taps[i-1],
				GridK:     kmin + int64(j-1),
				GridTime:  g,
				Deviation: abs64(taps[i-1] - g),
			})
			i--
			j--
		case pUp:
			res.ExtraTaps = append(res.ExtraTaps, ExtraTap{TapIndex: i - 1, TapTime: taps[i-1]})
			i--
		default: // pLeft
			res.MissedBeats = append(res.MissedBeats, gridAt(j-1))
			j--
		}
	}
	reverseMatches(res.Matches)
	reverseExtras(res.ExtraTaps)
	reverseInt64s(res.MissedBeats)
	return res
}

func uniqueSorted(xs []int64) []int64 {
	seen := make(map[int64]bool, len(xs))
	out := make([]int64, 0, len(xs))
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// floorDiv and ceilDiv assume b > 0.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func ceilDiv(a, b int64) int64 { return -floorDiv(-a, b) }

func reverseMatches(xs []MatchPair) {
	for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
		xs[i], xs[j] = xs[j], xs[i]
	}
}

func reverseExtras(xs []ExtraTap) {
	for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
		xs[i], xs[j] = xs[j], xs[i]
	}
}

func reverseInt64s(xs []int64) {
	for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 {
		xs[i], xs[j] = xs[j], xs[i]
	}
}
