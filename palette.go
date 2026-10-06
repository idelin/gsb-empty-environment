// Package quant provides exact solvers for integer gray-level palette
// quantization and for multi-frame quantization under per-frame
// recoloring budgets.
package quant

import (
	"errors"
	"fmt"
	"sort"
)

// Level associates an integer gray value with a pixel-count weight.
// Zero weights are allowed: such levels cost nothing but still belong
// to a group and receive a representative.
type Level struct {
	Value  int
	Weight int64
}

// Group is a maximal contiguous range of gray values (inclusive) that
// shares one integer representative.
type Group struct {
	Lo     int   // smallest gray value in the group
	Hi     int   // largest gray value in the group
	Rep    int   // integer representative minimizing the group cost
	Weight int64 // total weight of the group
	Cost   int64 // weighted squared error contributed by the group
}

// PaletteResult is the outcome of Quantize.
type PaletteResult struct {
	Groups  []Group     // contiguous groups ordered by gray value
	Palette []int       // representative of each group, same order
	Rep     map[int]int // gray value -> representative
	Cost    int64       // total weighted squared error
}

// Quantize picks k integer representatives for the given gray levels so
// that the total weighted squared error is minimized. Levels are first
// merged by value (duplicate colors are summed) and sorted; the optimal
// solution always partitions the sorted distinct values into k
// contiguous groups, so all compositions of cuts are enumerated and the
// best integer centroid of each candidate group is computed exactly.
//
// Tie-breaking rules (making the output deterministic):
//   - a group's representative is the smaller integer when floor and
//     ceil of the weighted mean cost the same;
//   - a group whose total weight is zero uses its smallest value;
//   - among equally optimal partitions the lexicographically smallest
//     cut tuple (equivalently, the one found first in increasing cut
//     order) wins.
//
// If k exceeds the number of distinct values it is clamped to it.
func Quantize(levels []Level, k int) (*PaletteResult, error) {
	vals, ws, err := normalize(levels)
	if err != nil {
		return nil, err
	}
	n := len(vals)
	if k <= 0 {
		return nil, fmt.Errorf("quant: k must be positive, got %d", k)
	}
	if k > n {
		k = n
	}

	// Prefix sums of weight, weight*value and weight*value^2.
	W := make([]int64, n+1)
	S1 := make([]int64, n+1)
	S2 := make([]int64, n+1)
	for i := 0; i < n; i++ {
		v := int64(vals[i])
		W[i+1] = W[i] + ws[i]
		S1[i+1] = S1[i] + ws[i]*v
		S2[i+1] = S2[i] + ws[i]*v*v
	}

	// groupCost returns the optimal integer representative and the
	// minimal cost for the half-open index range [lo, hi).
	groupCost := func(lo, hi int) (int, int64) {
		w := W[hi] - W[lo]
		s1 := S1[hi] - S1[lo]
		s2 := S2[hi] - S2[lo]
		if w == 0 {
			return vals[lo], 0
		}
		cost := func(r int64) int64 { return s2 - 2*r*s1 + r*r*w }
		// The weighted mean lies in [r0, r0+1]; by convexity of the
		// squared error the best integer representative is one of them.
		r0 := floorDiv(s1, w)
		c0, c1 := cost(r0), cost(r0+1)
		if c1 < c0 {
			return int(r0 + 1), c1
		}
		return int(r0), c0 // tie -> smaller representative
	}

	// Enumerate every composition of n distinct values into k contiguous
	// groups. Cuts are visited in lexicographic order and only strictly
	// better costs replace the incumbent, so the first optimum found is
	// the deterministic answer.
	const inf = int64(1<<62 - 1)
	bestCost := inf
	found := false
	var bestCuts []int
	cuts := make([]int, 0, k-1)
	var rec func(start, left int, acc int64)
	rec = func(start, left int, acc int64) {
		if left == 0 {
			_, c := groupCost(start, n)
			if total := acc + c; total < bestCost {
				bestCost = total
				found = true
				bestCuts = append([]int(nil), cuts...)
			}
			return
		}
		for c := start + 1; c <= n-left; c++ {
			_, gc := groupCost(start, c)
			if acc+gc >= bestCost {
				continue
			}
			cuts = append(cuts, c)
			rec(c, left-1, acc+gc)
			cuts = cuts[:len(cuts)-1]
		}
	}
	rec(0, k-1, 0)
	if !found {
		return nil, errors.New("quant: no feasible partition")
	}

	res := &PaletteResult{Rep: make(map[int]int, n)}
	bounds := append(append([]int(nil), bestCuts...), n)
	start := 0
	for _, b := range bounds {
		rep, c := groupCost(start, b)
		g := Group{
			Lo:     vals[start],
			Hi:     vals[b-1],
			Rep:    rep,
			Weight: W[b] - W[start],
			Cost:   c,
		}
		res.Groups = append(res.Groups, g)
		res.Palette = append(res.Palette, rep)
		for i := start; i < b; i++ {
			res.Rep[vals[i]] = rep
		}
		res.Cost += c
		start = b
	}
	return res, nil
}

// normalize merges duplicate values (summing their weights) and returns
// the distinct values in increasing order together with their weights.
func normalize(levels []Level) ([]int, []int64, error) {
	if len(levels) == 0 {
		return nil, nil, errors.New("quant: no gray levels provided")
	}
	merged := make(map[int]int64, len(levels))
	for _, l := range levels {
		if l.Weight < 0 {
			return nil, nil, fmt.Errorf("quant: negative weight %d for value %d", l.Weight, l.Value)
		}
		merged[l.Value] += l.Weight
	}
	vals := make([]int, 0, len(merged))
	for v := range merged {
		vals = append(vals, v)
	}
	sort.Ints(vals)
	ws := make([]int64, len(vals))
	for i, v := range vals {
		ws[i] = merged[v]
	}
	return vals, ws, nil
}

// floorDiv returns floor(a/b) for b > 0, also correct for negative a.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// CombinedHistogram builds the merged gray-level histogram of several
// frames, suitable as Quantize input to derive a shared palette.
func CombinedHistogram(frames [][]int) []Level {
	count := make(map[int]int64)
	for _, f := range frames {
		for _, g := range f {
			count[g]++
		}
	}
	levels := make([]Level, 0, len(count))
	for v, w := range count {
		levels = append(levels, Level{Value: v, Weight: w})
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].Value < levels[j].Value })
	return levels
}
