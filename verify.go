package main

import (
	"fmt"
	"math/rand"
)

// ---------------------------------------------------------------------------
// Brute-force enumeration of every guillotine-cut tree with <= budget leaves.
// Used only to verify the DP solvers on small inputs.
// ---------------------------------------------------------------------------

// enumTrees2 visits every cut tree over r with at most budget leaves,
// reporting (errA, errB, leaves, encoding) for frames a and b.
func enumTrees2(a, b *Image, r rect, budget int, cb func(p pt)) {
	if budget < 1 {
		return
	}
	area := r.area()
	errA, _ := leafErrColor(a.ones(r), area)
	errB, _ := leafErrColor(b.ones(r), area)
	cb(pt{errA: errA, errB: errB, leaves: 1, enc: "L"})
	if budget < 2 {
		return
	}
	for y := r.y0 + 1; y < r.y1; y++ {
		c1 := rect{r.x0, r.y0, r.x1, y}
		c2 := rect{r.x0, y, r.x1, r.y1}
		for b1 := 1; b1 <= budget-1; b1++ {
			enumTrees2(a, b, c1, b1, func(p pt) {
				enumTrees2(a, b, c2, budget-b1, func(q pt) {
					cb(pt{p.errA + q.errA, p.errB + q.errB, p.leaves + q.leaves,
						encCut('H', y, p.enc, q.enc)})
				})
			})
		}
	}
	for x := r.x0 + 1; x < r.x1; x++ {
		c1 := rect{r.x0, r.y0, x, r.y1}
		c2 := rect{x, r.y0, r.x1, r.y1}
		for b1 := 1; b1 <= budget-1; b1++ {
			enumTrees2(a, b, c1, b1, func(p pt) {
				enumTrees2(a, b, c2, budget-b1, func(q pt) {
					cb(pt{p.errA + q.errA, p.errB + q.errB, p.leaves + q.leaves,
						encCut('V', x, p.enc, q.enc)})
				})
			})
		}
	}
}

// verify1 cross-checks solver1 against full enumeration on one image.
func verify1(img *Image, k int) (dp sol1, bf sol1, ok bool) {
	full := rect{0, 0, img.W, img.H}
	dp = newSolver1(img).best(full, k)
	first := true
	enumTrees2(img, img, full, k, func(p pt) {
		cand := sol1{p.errA, p.leaves, p.enc}
		if first || better1(cand, bf) {
			bf, first = cand, false
		}
	})
	ok = dp == bf
	return
}

// verify2 cross-checks solver2 (feasibility, optimal value, tie-broken tree)
// against full enumeration on two same-size images.
func verify2(a, b *Image, k, capA, capB int) (dp pt, dpOK, bfOK bool, bf pt, ok bool) {
	full := rect{0, 0, a.W, a.H}
	dp, dpOK = pickBest2(newSolver2(a, b).front(full, k), k, capA, capB)
	first := true
	enumTrees2(a, b, full, k, func(p pt) {
		if p.leaves > k || p.errA > capA || p.errB > capB {
			return
		}
		if first ||
			p.errA+p.errB < bf.errA+bf.errB ||
			(p.errA+p.errB == bf.errA+bf.errB && p.leaves < bf.leaves) ||
			(p.errA+p.errB == bf.errA+bf.errB && p.leaves == bf.leaves && p.enc < bf.enc) {
			bf, first = p, false
		}
	})
	bfOK = !first
	if dpOK != bfOK {
		return dp, dpOK, bfOK, bf, false
	}
	if dpOK && dp != bf {
		return dp, dpOK, bfOK, bf, false
	}
	return dp, dpOK, bfOK, bf, true
}

// ---------------------------------------------------------------------------
// Randomized self-test: DP vs exhaustive enumeration on small images.
// ---------------------------------------------------------------------------

func randImage(rng *rand.Rand, w, h int) *Image {
	rows := make([]string, h)
	for y := range rows {
		b := make([]byte, w)
		for x := range b {
			b[x] = byte('0' + rng.Intn(2))
		}
		rows[y] = string(b)
	}
	im, err := NewImage(rows)
	if err != nil {
		panic(err)
	}
	return im
}

func selftest(n int, maxDim, maxK int, seed int64) error {
	rng := rand.New(rand.NewSource(seed))
	checked1, checked2, infeasible := 0, 0, 0
	for i := 0; i < n; i++ {
		w := 1 + rng.Intn(maxDim)
		h := 1 + rng.Intn(maxDim)
		k := 1 + rng.Intn(maxK)
		a := randImage(rng, w, h)
		b := randImage(rng, w, h)

		dp1, bf1, ok := verify1(a, k)
		if !ok {
			return fmt.Errorf("case %d: solve1 mismatch %dx%d k=%d: DP=%+v enum=%+v", i, w, h, k, dp1, bf1)
		}
		checked1++

		capA := rng.Intn(w*h + 1)
		capB := rng.Intn(w*h + 1)
		dp2, dpOK, bfOK, bf2, ok := verify2(a, b, k, capA, capB)
		if !ok {
			return fmt.Errorf("case %d: solve2 mismatch %dx%d k=%d caps=(%d,%d): DP=(%+v,%v) enum=(%+v,%v)",
				i, w, h, k, capA, capB, dp2, dpOK, bf2, bfOK)
		}
		checked2++
		if !dpOK {
			infeasible++
		}
	}
	fmt.Printf("selftest OK: %d single-frame and %d two-frame cases verified (%d infeasible), sizes<= %dx%d, k<=%d\n",
		checked1, checked2, infeasible, maxDim, maxDim, maxK)
	return nil
}
