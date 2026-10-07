package cutsolve

import "math"

// EnumTrees enumerates, exactly once each, every recursive integer-line cut
// tree of r having at most maxLeaves leaves. Uniqueness holds because every
// non-leaf tree has exactly one root cut.
func EnumTrees(r Rect, maxLeaves int, yield func(t *Tree, leaves int)) {
	yield(Leaf(), 1)
	if maxLeaves < 2 {
		return
	}
	for _, c := range r.Cuts() {
		EnumTrees(c.A, maxLeaves-1, func(t1 *Tree, n1 int) {
			EnumTrees(c.B, maxLeaves-n1, func(t2 *Tree, n2 int) {
				yield(Split(c.Dir, c.Pos, t1, t2), n1+n2)
			})
		})
	}
}

// BruteSingle independently recomputes the single-frame optimum by exhaustively
// enumerating all cut trees with at most k leaves. Used to verify SolveSingle.
func BruteSingle(im Image, k int) SingleResult {
	root := Rect{0, 0, im.W, im.H}
	best := SingleResult{Err: math.MaxInt}
	EnumTrees(root, min(k, root.Area()), func(t *Tree, n int) {
		err := t.FrameError(im, root)
		if err < best.Err ||
			(err == best.Err && n < best.Leaves) ||
			(err == best.Err && n == best.Leaves && Compare(t, best.Tree) < 0) {
			best = SingleResult{Tree: t, Err: err, Leaves: n}
		}
	})
	return best
}

// BruteDual independently recomputes the shared-template two-frame optimum by
// exhaustively enumerating all shared cut trees with at most k leaves. Used to
// verify SolveDual, including infeasibility.
func BruteDual(im0, im1 Image, k, cap0, cap1 int) DualResult {
	root := Rect{0, 0, im0.W, im0.H}
	best := DualResult{Feasible: true}
	bestTotal := math.MaxInt
	found := false
	EnumTrees(root, min(k, root.Area()), func(t *Tree, n int) {
		e0 := t.FrameError(im0, root)
		e1 := t.FrameError(im1, root)
		if e0 > cap0 || e1 > cap1 {
			return
		}
		total := e0 + e1
		if !found || total < bestTotal ||
			(total == bestTotal && n < best.Leaves) ||
			(total == bestTotal && n == best.Leaves && Compare(t, best.Tree) < 0) {
			found = true
			bestTotal = total
			best = DualResult{Feasible: true, Tree: t, Err0: e0, Err1: e1, Leaves: n}
		}
	})
	if !found {
		return DualResult{Feasible: false}
	}
	return best
}
