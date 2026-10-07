package cutsolve

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// DualResult is the outcome of the shared-template two-frame problem.
type DualResult struct {
	Feasible bool
	Tree     *Tree // shared cut tree (nil when infeasible)
	Err0     int   // frame-0 miscolored pixels
	Err1     int   // frame-1 miscolored pixels
	Leaves   int
	Evidence string // infeasibility proof when Feasible is false
}

// dKey is a (frame-0 error, frame-1 error) pair.
type dKey [2]int

// frontier maps an achievable error pair to the minimal-encoding shared tree
// attaining it, kept Pareto-pruned.
type frontier map[dKey]*Tree

// SolveDual solves the cross-frame shared-template problem: both frames (same
// dimensions) must use ONE common cut tree; each leaf picks its own majority
// color per frame. Subject to at most k leaves and per-frame error caps
// (err0 <= cap0, err1 <= cap1), it minimizes
//  1. total error err0+err1,
//  2. then the leaf count,
//  3. then the pre-order cut encoding.
//
// The DP keeps, per (rectangle, exact leaf count), the Pareto frontier of
// (err0, err1) pairs with the minimal-encoding tree per pair. Dominated
// children can never belong to an optimal answer: replacing one strictly
// lowers one frame's error without raising the other, and equal-sum pairs
// keep the minimal encoding. Frames are never optimized independently — a
// single shared tree is chosen for both.
func SolveDual(im0, im1 Image, k, cap0, cap1 int) DualResult {
	if im0.W != im1.W || im0.H != im1.H {
		return DualResult{Evidence: fmt.Sprintf("infeasible: frame sizes differ (%dx%d vs %dx%d)",
			im0.W, im0.H, im1.W, im1.H)}
	}
	root := Rect{0, 0, im0.W, im0.H}
	if k < 1 {
		k = 1
	}
	dp := make(map[Rect][]frontier, 1024)
	for _, r := range allRects(im0.W, im0.H) {
		maxN := min(k, r.Area())
		fs := make([]frontier, maxN+1)
		_, e0 := im0.LeafStats(r)
		_, e1 := im1.LeafStats(r)
		fs[1] = frontier{dKey{e0, e1}: Leaf()}
		cuts := r.Cuts()
		for n := 2; n <= maxN; n++ {
			cand := frontier{}
			for _, c := range cuts {
				fa, fb := dp[c.A], dp[c.B]
				for n1 := 1; n1 < n && n1 < len(fa); n1++ {
					n2 := n - n1
					if n2 < 1 || n2 >= len(fb) {
						continue
					}
					for ka, ta := range fa[n1] {
						for kb, tb := range fb[n2] {
							key := dKey{ka[0] + kb[0], ka[1] + kb[1]}
							t := Split(c.Dir, c.Pos, ta, tb)
							if ex, ok := cand[key]; !ok || Compare(t, ex) < 0 {
								cand[key] = t
							}
						}
					}
				}
			}
			fs[n] = pruneFrontier(cand)
		}
		dp[r] = fs
	}

	// Final selection: feasible points only, ordered by
	// (total error, leaf count, encoding).
	best := DualResult{Feasible: true, Err0: math.MaxInt, Err1: math.MaxInt}
	bestTotal := math.MaxInt
	found := false
	for n := 1; n < len(dp[root]); n++ {
		for key, t := range dp[root][n] {
			if key[0] > cap0 || key[1] > cap1 {
				continue
			}
			total := key[0] + key[1]
			if !found || total < bestTotal ||
				(total == bestTotal && n < best.Leaves) ||
				(total == bestTotal && n == best.Leaves && Compare(t, best.Tree) < 0) {
				found = true
				bestTotal = total
				best = DualResult{Feasible: true, Tree: t, Err0: key[0], Err1: key[1], Leaves: n}
			}
		}
	}
	if found {
		return best
	}
	return DualResult{Feasible: false, Evidence: infeasibilityEvidence(dp[root], k, cap0, cap1)}
}

// pruneFrontier drops dominated pairs: (a,b) dominates (a',b') iff a<=a' and
// b<=b' with at least one strict. Keeping only min-encoding trees per pair
// (done by callers) plus Pareto pruning preserves every optimal final answer.
func pruneFrontier(f frontier) frontier {
	keys := sortedKeys(f)
	out := frontier{}
	bestE1 := math.MaxInt
	for _, k := range keys {
		if k[1] < bestE1 {
			out[k] = f[k]
			bestE1 = k[1]
		}
	}
	return out
}

func sortedKeys(f frontier) []dKey {
	keys := make([]dKey, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	return keys
}

// infeasibilityEvidence proves no shared tree meets the caps: it lists, per
// leaf count, the full Pareto frontier of achievable (err0, err1) pairs, plus
// the best achievable error of each frame independently.
func infeasibilityEvidence(rootDP []frontier, k, cap0, cap1 int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "infeasible: no shared cut tree with <= %d leaves satisfies both frame caps (f0 <= %d, f1 <= %d)\n",
		k, cap0, cap1)
	min0, min1 := math.MaxInt, math.MaxInt
	for n := 1; n < len(rootDP); n++ {
		fmt.Fprintf(&sb, "  leaves=%d Pareto frontier (f0,f1):", n)
		for _, key := range sortedKeys(rootDP[n]) {
			fmt.Fprintf(&sb, " (%d,%d)", key[0], key[1])
			min0 = min(min0, key[0])
			min1 = min(min1, key[1])
		}
		sb.WriteByte('\n')
	}
	fmt.Fprintf(&sb, "  best achievable with <= %d leaves: min f0 error = %d, min f1 error = %d\n", k, min0, min1)
	return sb.String()
}
