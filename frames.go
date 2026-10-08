package quant

import "sort"

// FrameSpec describes one frame of a sequence to be quantized jointly.
type FrameSpec struct {
	Pixels []int // gray value per pixel; every frame must have the same length
	Budget int   // max pixels whose representative may change vs the previous frame (ignored for frame 0)
	MaxErr int64 // per-frame squared-error cap; negative means unlimited
}

// FrameResult is the quantization outcome of one frame.
type FrameResult struct {
	Rep   []int // representative assigned to each pixel
	Err   int64 // squared error of this frame
	Moved int   // pixels whose representative changed vs the previous frame (0 for frame 0)
}

// FramesResult is the outcome of SolveFrames / OptimizeFrames.
type FramesResult struct {
	OK      bool          // false when no assignment satisfies every constraint
	Palette []int         // the shared palette, ascending
	Frames  []FrameResult // per-frame mapping and migration usage
	Total   int64         // total squared error over all frames
}

// SolveFrames exactly solves the joint quantization of a frame sequence
// under a fixed shared palette.
//
// Every pixel of every frame must be assigned a palette entry. Frame t>0
// may change the assignment of at most frames[t].Budget pixels relative to
// frame t-1, and the squared error of frame t must not exceed
// frames[t].MaxErr (when non-negative). The total squared error over all
// frames is minimized.
//
// The solver is a dynamic program over per-frame assignment vectors, so it
// is exact but exponential in the number of pixels: it is meant for small
// images and small palettes.
func SolveFrames(frames []FrameSpec, palette []int) FramesResult {
	if len(frames) == 0 {
		return FramesResult{OK: true}
	}
	p := len(frames[0].Pixels)
	for _, f := range frames {
		if len(f.Pixels) != p {
			panic("quant: all frames must have the same number of pixels")
		}
	}
	pal := append([]int(nil), palette...)
	sort.Ints(pal)
	k := len(pal)
	if k == 0 {
		if p == 0 {
			return FramesResult{OK: true, Palette: pal, Frames: emptyFrames(len(frames))}
		}
		return FramesResult{Palette: pal}
	}

	// Number of assignment vectors; guard against accidental blow-up.
	states := 1
	for i := 0; i < p; i++ {
		states *= k
		if states > 1<<22 {
			panic("quant: instance too large for the exact solver")
		}
	}

	// digits[s][i] = palette index of pixel i in assignment s (base k).
	digits := make([][]uint8, states)
	for s := 0; s < states; s++ {
		d := make([]uint8, p)
		for i, v := p-1, s; i >= 0; i-- {
			d[i] = uint8(v % k)
			v /= k
		}
		digits[s] = d
	}

	// Per-frame error of every assignment.
	t := len(frames)
	err := make([][]int64, t)
	ok := make([][]bool, t)
	for f := 0; f < t; f++ {
		err[f] = make([]int64, states)
		ok[f] = make([]bool, states)
		for s := 0; s < states; s++ {
			var e int64
			for i := 0; i < p; i++ {
				d := int64(frames[f].Pixels[i] - pal[digits[s][i]])
				e += d * d
			}
			err[f][s] = e
			ok[f][s] = frames[f].MaxErr < 0 || e <= frames[f].MaxErr
		}
	}

	// dp[s] = best total error up to the current frame ending in
	// assignment s; -1 marks unreachable. Iteration is in increasing
	// state order with strict improvement, which makes ties resolve to
	// the lexicographically smallest assignment sequence.
	const unreachable = int64(-1)
	dp := make([]int64, states)
	parent := make([][]int32, t)
	for f := 0; f < t; f++ {
		parent[f] = make([]int32, states)
		next := make([]int64, states)
		for s := 0; s < states; s++ {
			next[s] = unreachable
		}
		for cur := 0; cur < states; cur++ {
			if !ok[f][cur] {
				continue
			}
			if f == 0 {
				next[cur] = err[0][cur]
				parent[0][cur] = -1
				continue
			}
			for prev := 0; prev < states; prev++ {
				if dp[prev] == unreachable {
					continue
				}
				moved := 0
				for i := 0; i < p; i++ {
					if digits[cur][i] != digits[prev][i] {
						moved++
					}
				}
				if moved > frames[f].Budget {
					continue
				}
				cand := dp[prev] + err[f][cur]
				if next[cur] == unreachable || cand < next[cur] {
					next[cur] = cand
					parent[f][cur] = int32(prev)
				}
			}
		}
		dp = next
	}

	// Best final assignment.
	best := -1
	for s := 0; s < states; s++ {
		if dp[s] != unreachable && (best < 0 || dp[s] < dp[best]) {
			best = s
		}
	}
	if best < 0 {
		return FramesResult{Palette: pal}
	}

	// Backtrack.
	path := make([]int, t)
	path[t-1] = best
	for f := t - 1; f > 0; f-- {
		path[f-1] = int(parent[f][path[f]])
	}
	res := FramesResult{OK: true, Palette: pal, Total: dp[best]}
	for f := 0; f < t; f++ {
		fr := FrameResult{Rep: make([]int, p), Err: err[f][path[f]]}
		for i := 0; i < p; i++ {
			fr.Rep[i] = pal[digits[path[f]][i]]
		}
		if f > 0 {
			for i := 0; i < p; i++ {
				if digits[path[f]][i] != digits[path[f-1]][i] {
					fr.Moved++
				}
			}
		}
		res.Frames = append(res.Frames, fr)
	}
	return res
}

// OptimizeFrames is SolveFrames with the palette itself optimized: every
// k-element subset of domain is tried and the one with the smallest total
// error wins; ties resolve to the lexicographically smallest palette.
// When domain is nil the distinct gray values occurring in the frames are
// used. The finite domain keeps the search exact.
func OptimizeFrames(frames []FrameSpec, domain []int, k int) FramesResult {
	if k < 1 {
		panic("quant: k must be >= 1")
	}
	if domain == nil {
		seen := map[int]bool{}
		for _, f := range frames {
			for _, g := range f.Pixels {
				if !seen[g] {
					seen[g] = true
					domain = append(domain, g)
				}
			}
		}
	}
	dom := append([]int(nil), domain...)
	sort.Ints(dom)
	// Deduplicate.
	out := dom[:0]
	for i, v := range dom {
		if i == 0 || v != dom[i-1] {
			out = append(out, v)
		}
	}
	dom = out
	if k > len(dom) {
		return FramesResult{}
	}

	var best FramesResult
	idx := make([]int, k)
	var rec func(pos, lo int)
	rec = func(pos, lo int) {
		if pos == k {
			pal := make([]int, k)
			for i, j := range idx {
				pal[i] = dom[j]
			}
			r := SolveFrames(frames, pal)
			if r.OK && (!best.OK || r.Total < best.Total) {
				best = r
			}
			return
		}
		for j := lo; j <= len(dom)-(k-pos); j++ {
			idx[pos] = j
			rec(pos+1, j+1)
		}
	}
	rec(0, 0)
	return best
}

func emptyFrames(t int) []FrameResult {
	fr := make([]FrameResult, t)
	for i := range fr {
		fr[i].Rep = []int{}
	}
	return fr
}
