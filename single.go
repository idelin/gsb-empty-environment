package cutsolve

import "math"

// SingleResult is the optimum for one frame under a leaf budget.
type SingleResult struct {
	Tree   *Tree
	Err    int // miscolored pixels
	Leaves int
}

// sBest is the best exact-leaf-count solution for one rectangle.
type sBest struct {
	err  int
	tree *Tree
}

// SolveSingle finds, among all recursive integer-line cut trees of the image
// with at most k leaves, the tree that
//  1. minimizes miscolored pixels (majority fill, ties to 0),
//  2. then uses the fewest leaves,
//  3. then has the smallest pre-order cut encoding (see Tree.Encoding).
//
// Dynamic programming over (rectangle, exact leaf count): children of a cut
// are strictly smaller rectangles, so area-ordered evaluation is exact. For a
// fixed cut and leaf split, combining the per-child optima is valid because
// errors add and the encoding order decomposes lexicographically over
// (cut, left, right).
func SolveSingle(im Image, k int) SingleResult {
	root := Rect{0, 0, im.W, im.H}
	if k < 1 {
		k = 1
	}
	dp := make(map[Rect][]sBest, 1024)
	for _, r := range allRects(im.W, im.H) {
		maxN := min(k, r.Area())
		tb := make([]sBest, maxN+1)
		_, e := im.LeafStats(r)
		tb[1] = sBest{e, Leaf()}
		cuts := r.Cuts()
		for n := 2; n <= maxN; n++ {
			best := sBest{err: math.MaxInt}
			for _, c := range cuts {
				ta, tb2 := dp[c.A], dp[c.B]
				for n1 := 1; n1 < n && n1 < len(ta); n1++ {
					n2 := n - n1
					if n2 < 1 || n2 >= len(tb2) {
						continue
					}
					ca, cb := ta[n1], tb2[n2]
					err := ca.err + cb.err
					if err > best.err {
						continue
					}
					cand := Split(c.Dir, c.Pos, ca.tree, cb.tree)
					if err < best.err || Compare(cand, best.tree) < 0 {
						best = sBest{err, cand}
					}
				}
			}
			tb[n] = best
		}
		dp[r] = tb
	}
	tb := dp[root]
	res := SingleResult{Err: math.MaxInt}
	for n := 1; n < len(tb); n++ {
		// n ascends, so the first tree reaching a given error already has the
		// fewest leaves; dp[r][n] already holds the minimal encoding for (r, n).
		if tb[n].err < res.Err {
			res = SingleResult{Tree: tb[n].tree, Err: tb[n].err, Leaves: n}
		}
	}
	return res
}
