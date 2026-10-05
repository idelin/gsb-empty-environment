package main

import (
	"fmt"
	"math/big"
)

// pickIndex maps a value r in [0, sum(weights)) to a block index via
// cumulative intervals, skipping blocks that are no longer alive. This is
// the exact same interval mapping the sampler uses, evaluated
// deterministically for enumeration.
func pickIndex(weights []int64, alive []bool, r int64) int {
	var acc int64
	for i, w := range weights {
		if !alive[i] {
			continue
		}
		acc += w
		if r < acc {
			return i
		}
	}
	return -1
}

// EnumReport holds the result of exhaustively enumerating the random domain
// of a small archive.
type EnumReport struct {
	TotalWeight int64
	FirstCounts []int64   // for each block: number of r in [0, W) that select it first
	PairCounts  [][]int64 // for each (i, j): number of (r1, r2) pairs selecting i then j
	FirstOK     bool
	PairsOK     bool
}

// Enumerate exhaustively walks the random domain of the sampler for a small
// archive and checks the selection probabilities exactly:
//
//   - First draw: every r in [0, W) is equally likely (rejection sampling
//     gives a uniform value, no modulo bias), so block i must be selected by
//     exactly w_i values of r, i.e. P(first = i) = w_i / W exactly.
//   - Second draw: conditioned on the first block i, every r2 in
//     [0, W - w_i) is equally likely, so block j != i must be selected by
//     exactly w_j values, i.e. P(second = j | first = i) = w_j / (W - w_i)
//     exactly. Across the domain, the (i, j) pair must occur exactly
//     w_i * w_j times, and (i, i) must never occur (no replacement).
//
// weights must be small: the total work is O(W + sum_i w_i*(W-w_i)).
func Enumerate(weights []*big.Int, maxWork int64) (*EnumReport, error) {
	n := len(weights)
	w := make([]int64, n)
	var total int64
	for i, bw := range weights {
		if !bw.IsInt64() {
			return nil, fmt.Errorf("block %d weight %s does not fit int64; archive too large to enumerate", i, bw)
		}
		wi := bw.Int64()
		if wi < 0 {
			return nil, fmt.Errorf("block %d has negative weight", i)
		}
		total += wi
		if total < 0 {
			return nil, fmt.Errorf("total weight overflows int64; archive too large to enumerate")
		}
		w[i] = wi
	}
	if total <= 0 {
		return nil, fmt.Errorf("total weight must be positive")
	}
	// Work estimate: W for the first pass plus sum_i w_i*(W-w_i) for pairs.
	pairWork := int64(0)
	for _, wi := range w {
		pairWork += wi * (total - wi)
		if pairWork < 0 || pairWork > maxWork {
			return nil, fmt.Errorf("enumeration would require more than %d steps; archive too large", maxWork)
		}
	}

	rep := &EnumReport{
		TotalWeight: total,
		FirstCounts: make([]int64, n),
		PairCounts:  make([][]int64, n),
		FirstOK:     true,
		PairsOK:     true,
	}
	for i := range rep.PairCounts {
		rep.PairCounts[i] = make([]int64, n)
	}

	alive := make([]bool, n)
	for r := int64(0); r < total; r++ {
		for i := range alive {
			alive[i] = true
		}
		i := pickIndex(w, alive, r)
		if i < 0 {
			return nil, fmt.Errorf("internal error: r=%d selected no block", r)
		}
		rep.FirstCounts[i]++
		alive[i] = false
		second := total - w[i]
		for r2 := int64(0); r2 < second; r2++ {
			j := pickIndex(w, alive, r2)
			if j < 0 {
				return nil, fmt.Errorf("internal error: r2=%d selected no block", r2)
			}
			rep.PairCounts[i][j]++
		}
	}

	for i := 0; i < n; i++ {
		if rep.FirstCounts[i] != w[i] {
			rep.FirstOK = false
		}
		for j := 0; j < n; j++ {
			want := w[i] * w[j]
			if i == j {
				want = 0
			}
			if rep.PairCounts[i][j] != want {
				rep.PairsOK = false
			}
		}
	}
	return rep, nil
}
