package quant

import (
	"errors"
	"fmt"
)

// maxStates bounds the exact solver's state space (paletteSize^pixels).
const maxStates = 4096

// FrameResult is the outcome of QuantizeFrames.
type FrameResult struct {
	Assign   [][]int // Assign[f][i] = palette index of pixel i in frame f
	Changed  []int   // Changed[f] (f >= 1) = pixels whose palette index differs from frame f-1
	FrameErr []int64 // FrameErr[f] = squared error of frame f
	Total    int64   // sum of FrameErr over all frames
}

// QuantizeFrames exactly solves the multi-frame quantization problem.
// All frames share the fixed palette and have the same number of
// pixels; pixel i of every frame corresponds to the same image
// position. The solver chooses a palette index per pixel per frame
// minimizing the total squared error subject to:
//
//   - migration budget: for each f >= 1 at most budget[f-1] pixels may
//     change their palette index relative to frame f-1;
//   - frame error limit: every frame's squared error is at most
//     maxFrameErr (pass a negative value for no limit).
//
// The optimum is found by dynamic programming over all paletteSize^N
// per-frame assignment vectors, which is exact but only feasible for
// small images and small palettes; an error is returned when the state
// space exceeds maxStates. Ties are broken deterministically in favour
// of the lexicographically smallest assignment sequence.
func QuantizeFrames(frames [][]int, palette []int, budget []int, maxFrameErr int64) (*FrameResult, error) {
	F := len(frames)
	if F == 0 {
		return nil, errors.New("quant: no frames provided")
	}
	N := len(frames[0])
	if N == 0 {
		return nil, errors.New("quant: frames have no pixels")
	}
	for f := 1; f < F; f++ {
		if len(frames[f]) != N {
			return nil, fmt.Errorf("quant: frame %d has %d pixels, want %d", f, len(frames[f]), N)
		}
	}
	K := len(palette)
	if K == 0 {
		return nil, errors.New("quant: empty palette")
	}
	seen := make(map[int]bool, K)
	for _, p := range palette {
		if seen[p] {
			return nil, fmt.Errorf("quant: duplicate palette entry %d", p)
		}
		seen[p] = true
	}
	if len(budget) != F-1 {
		return nil, fmt.Errorf("quant: budget must have %d entries (one per frame transition), got %d", F-1, len(budget))
	}
	for f, b := range budget {
		if b < 0 {
			return nil, fmt.Errorf("quant: negative budget %d at transition %d", b, f)
		}
	}

	S := 1
	for i := 0; i < N; i++ {
		S *= K
		if S > maxStates {
			return nil, fmt.Errorf("quant: state space %d^%d exceeds exact-solver limit %d", K, N, maxStates)
		}
	}

	// digits[s][i] is the palette index of pixel i in assignment s.
	digits := make([][]uint8, S)
	for s := 0; s < S; s++ {
		d := make([]uint8, N)
		x := s
		for i := N - 1; i >= 0; i-- {
			d[i] = uint8(x % K)
			x /= K
		}
		digits[s] = d
	}

	// cost[f][s] is the squared error of assignment s on frame f.
	cost := make([][]int64, F)
	for f := 0; f < F; f++ {
		cost[f] = make([]int64, S)
		for s := 0; s < S; s++ {
			var c int64
			for i := 0; i < N; i++ {
				d := int64(frames[f][i] - palette[digits[s][i]])
				c += d * d
			}
			cost[f][s] = c
		}
	}

	limit := maxFrameErr
	if limit < 0 {
		limit = int64(1<<62 - 1)
	}
	const inf = int64(1<<62 - 1)

	parent := make([][]int, F)
	for f := range parent {
		parent[f] = make([]int, S)
		for s := range parent[f] {
			parent[f][s] = -1
		}
	}
	prev := make([]int64, S)
	for s := 0; s < S; s++ {
		prev[s] = inf
		if cost[0][s] <= limit {
			prev[s] = cost[0][s]
		}
	}
	for f := 1; f < F; f++ {
		cur := make([]int64, S)
		b := budget[f-1]
		for t := 0; t < S; t++ {
			cur[t] = inf
			if cost[f][t] > limit {
				continue
			}
			for s := 0; s < S; s++ {
				if prev[s] == inf || hamming(digits[s], digits[t]) > b {
					continue
				}
				if c := prev[s] + cost[f][t]; c < cur[t] {
					cur[t] = c
					parent[f][t] = s
				}
			}
		}
		prev = cur
	}

	best := inf
	bestState := -1
	for s := 0; s < S; s++ {
		if prev[s] < best {
			best = prev[s]
			bestState = s
		}
	}
	if bestState < 0 {
		return nil, errors.New("quant: infeasible: no assignment satisfies the migration budget and frame error limit")
	}

	path := make([]int, F)
	path[F-1] = bestState
	for f := F - 1; f >= 1; f-- {
		path[f-1] = parent[f][path[f]]
	}

	res := &FrameResult{
		Assign:   make([][]int, F),
		Changed:  make([]int, F),
		FrameErr: make([]int64, F),
		Total:    best,
	}
	for f := 0; f < F; f++ {
		a := make([]int, N)
		for i, d := range digits[path[f]] {
			a[i] = int(d)
		}
		res.Assign[f] = a
		res.FrameErr[f] = cost[f][path[f]]
		if f >= 1 {
			res.Changed[f] = hamming(digits[path[f-1]], digits[path[f]])
		}
	}
	return res, nil
}

// hamming counts positions where two assignment vectors differ.
func hamming(a, b []uint8) int {
	n := 0
	for i := range a {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}
