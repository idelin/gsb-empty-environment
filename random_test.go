package taskpkg_test

import (
	"math/big"
	"math/rand"
	"testing"

	"example.org/domainreissue/q046"
)

func randomRing(rng *rand.Rand) taskpkg.Ring {
	n := 3 + rng.Intn(6)
	coords := make([][2]int, n)
	for i := range coords {
		coords[i] = [2]int{rng.Intn(13) - 4, rng.Intn(13) - 4}
	}
	return taskpkg.RingFromInts(coords...)
}

func TestRandomIntegerRings(t *testing.T) {
	rng := rand.New(rand.NewSource(46))
	rules := []taskpkg.FillRule{taskpkg.Nonzero, taskpkg.EvenOdd}
	for trial := 0; trial < 80; trial++ {
		a := taskpkg.NewRegion(rules[rng.Intn(2)], randomRing(rng))
		b := taskpkg.NewRegion(rules[rng.Intn(2)], randomRing(rng))
		areaA := regionArea(t, a)
		areaB := regionArea(t, b)

		for _, op := range []taskpkg.Op{taskpkg.OpUnion, taskpkg.OpIntersect, taskpkg.OpDifference} {
			res := taskpkg.Boolean(a, b, op)
			for xi := -3; xi <= 9; xi++ {
				for yi := -3; yi <= 9; yi++ {
					p := taskpkg.PointFromFraction(int64(6*xi+1), 6, int64(6*yi+1), 6)
					if a.OnBoundary(p) || b.OnBoundary(p) {
						continue
					}
					want := expectedOp(op, a.ContainsPoint(p), b.ContainsPoint(p))
					if got := res.ContainsPoint(p); got != want {
						t.Fatalf("trial %d %s at %s: got %v want %v\nA=%+v\nB=%+v", trial, op, p, got, want, a.Rings, b.Rings)
					}
				}
			}
		}

		union := taskpkg.Union(a, b)
		inter := taskpkg.Intersection(a, b)
		diff := taskpkg.Difference(a, b)
		if union.Area().Cmp(new(big.Rat).Add(areaA, areaB)) > 0 {
			t.Fatalf("trial %d union area exceeds operands", trial)
		}
		requireRat(t, new(big.Rat).Add(union.Area(), inter.Area()), new(big.Rat).Add(areaA, areaB))
		requireRat(t, new(big.Rat).Add(diff.Area(), inter.Area()), areaA)
	}
}
