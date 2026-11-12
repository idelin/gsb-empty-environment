package taskpkg_test

import (
	"math/big"
	"testing"

	"example.org/domainreissue/q046"
)

func testPolygons() []taskpkg.Ring {
	return []taskpkg.Ring{
		taskpkg.RingFromInts([2]int{0, 0}, [2]int{6, 0}, [2]int{0, 5}),
		taskpkg.RingFromInts([2]int{2, -1}, [2]int{7, 1}, [2]int{3, 6}),
		taskpkg.RingFromInts(
			[2]int{1, 0}, [2]int{5, 0}, [2]int{5, 1},
			[2]int{3, 1}, [2]int{3, 4}, [2]int{1, 4},
		),
		taskpkg.RingFromInts([2]int{3, -1}, [2]int{7, 3}, [2]int{3, 7}, [2]int{-1, 3}),
		taskpkg.RingFromInts(
			[2]int{0, 1}, [2]int{4, 0}, [2]int{7, 3},
			[2]int{4, 6}, [2]int{0, 5},
		),
		taskpkg.RingFromInts([2]int{0, 0}, [2]int{6, 0}, [2]int{0, 4}, [2]int{6, 4}),
		taskpkg.RingFromInts([2]int{-1, 2}, [2]int{3, -1}, [2]int{8, 2}, [2]int{3, 5}),
	}
}

func TestArbitraryPolygonGridReference(t *testing.T) {
	polygons := testPolygons()
	rules := []taskpkg.FillRule{taskpkg.Nonzero, taskpkg.EvenOdd}
	for i := 0; i < len(polygons); i++ {
		for j := i + 1; j < len(polygons); j++ {
			a := taskpkg.NewRegion(rules[(i+j)%2], polygons[i])
			b := taskpkg.NewRegion(rules[(i+j+1)%2], polygons[j])
			areaA := regionArea(t, a)
			areaB := regionArea(t, b)

			for _, op := range []taskpkg.Op{taskpkg.OpUnion, taskpkg.OpIntersect, taskpkg.OpDifference} {
				res := taskpkg.Boolean(a, b, op)
				for xi := -2; xi <= 10; xi++ {
					for yi := -2; yi <= 10; yi++ {
						p := taskpkg.PointFromFraction(int64(6*xi+1), 6, int64(6*yi+1), 6)
						if a.OnBoundary(p) || b.OnBoundary(p) {
							continue
						}
						want := expectedOp(op, a.ContainsPoint(p), b.ContainsPoint(p))
						got := res.ContainsPoint(p)
						if got != want {
							t.Fatalf("poly %d,%d %s at %s: got %v want %v", i, j, op, p, got, want)
						}
					}
				}
			}

			union := taskpkg.Union(a, b)
			inter := taskpkg.Intersection(a, b)
			diff := taskpkg.Difference(a, b)
			requireRat(t, new(big.Rat).Add(union.Area(), inter.Area()), new(big.Rat).Add(areaA, areaB))
			requireRat(t, new(big.Rat).Add(diff.Area(), inter.Area()), areaA)

			unionBA := taskpkg.Union(b, a)
			interBA := taskpkg.Intersection(b, a)
			requireRat(t, union.Area(), unionBA.Area())
			requireRat(t, inter.Area(), interBA.Area())
			for xi := -2; xi <= 10; xi++ {
				for yi := -2; yi <= 10; yi++ {
					p := taskpkg.PointFromFraction(int64(6*xi+1), 6, int64(6*yi+1), 6)
					if a.OnBoundary(p) || b.OnBoundary(p) {
						continue
					}
					if union.ContainsPoint(p) != unionBA.ContainsPoint(p) {
						t.Fatalf("poly %d,%d union not commutative at %s", i, j, p)
					}
					if inter.ContainsPoint(p) != interBA.ContainsPoint(p) {
						t.Fatalf("poly %d,%d intersection not commutative at %s", i, j, p)
					}
				}
			}
		}
	}
}

func TestMultipleHoleComponents(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 12, 8))
	b := taskpkg.NewRegion(
		taskpkg.Nonzero,
		rect(2, 2, 4, 4),
		rect(7, 2, 10, 5),
		rect(5, 5, 6, 7),
	)
	diff := taskpkg.Difference(a, b)
	requireArea(t, diff, 96-4-9-2, 1)
	if len(diff.Outers) != 1 || len(diff.Holes) != 3 {
		t.Fatalf("shape: %d outers, %d holes", len(diff.Outers), len(diff.Holes))
	}
	if len(diff.Components) != 1 || len(diff.Components[0].Holes) != 3 {
		t.Fatalf("component grouping: %+v", diff.Components)
	}
}

func TestNestedIslandInsideHole(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 10, 10))
	b := taskpkg.NewRegion(taskpkg.Nonzero, rect(2, 2, 8, 8))
	ringHole := taskpkg.Difference(a, b)
	island := taskpkg.NewRegion(taskpkg.Nonzero, rect(4, 4, 6, 6))
	result := taskpkg.Union(ringHole.Region(), island)
	requireArea(t, result, 100-36+4, 1)
	if !result.ContainsPoint(pt(5, 5)) {
		t.Fatal("nested island was lost")
	}
	if result.ContainsPoint(pt(3, 3)) {
		t.Fatal("hole was filled")
	}
	if len(result.Outers) != 2 || len(result.Holes) != 1 {
		t.Fatalf("shape: %d outers, %d holes", len(result.Outers), len(result.Holes))
	}
	outerWithHole := 0
	for _, component := range result.Components {
		if len(component.Holes) == 1 {
			outerWithHole++
		}
	}
	if outerWithHole != 1 {
		t.Fatalf("hole was attached to the wrong outer: %+v", result.Components)
	}
}

func TestFourWayPointContact(t *testing.T) {
	four := taskpkg.NewRegion(
		taskpkg.Nonzero,
		rect(0, 0, 2, 2),
		rect(2, 0, 4, 2),
		rect(0, 2, 2, 4),
		rect(2, 2, 4, 4),
	)
	res := taskpkg.Union(four, emptyRegion())
	requireArea(t, res, 16, 1)
	if len(res.Outers) != 1 {
		t.Fatalf("four tiling quadrants produced %d outers, want 1", len(res.Outers))
	}
}

func TestCrossUnionIsOneOuter(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 2, 6, 4))
	b := taskpkg.NewRegion(taskpkg.Nonzero, rect(2, 0, 4, 6))
	res := taskpkg.Union(a, b)
	requireArea(t, res, 20, 1)
	if len(res.Outers) != 1 {
		t.Fatalf("cross union produced %d outers, want 1", len(res.Outers))
	}
	for _, p := range []taskpkg.Point{
		pt(1, 3),
		pt(3, 1),
		pt(3, 3),
		pt(3, 5),
	} {
		if !res.ContainsPoint(p) {
			t.Fatalf("cross union missing point %s", p)
		}
	}
	if res.ContainsPoint(pt(1, 1)) {
		t.Fatal("cross union filled an outside quadrant")
	}
}

func TestHoleTouchingOuterAtPoint(t *testing.T) {
	outer := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 10, 10))
	diamond := taskpkg.NewRegion(taskpkg.Nonzero, taskpkg.RingFromInts(
		[2]int{3, 7},
		[2]int{5, 10},
		[2]int{7, 7},
		[2]int{5, 4},
	))
	res := taskpkg.Difference(outer, diamond)
	requireArea(t, res, 88, 1)
	if res.ContainsPoint(pt(1, 1)) == false {
		t.Fatal("outer area was lost")
	}
	if res.ContainsPoint(pt(5, 7)) {
		t.Fatal("hole was filled")
	}
	if res.ContainsPoint(pt(5, 10)) {
		t.Fatal("touch point should remain boundary, not area")
	}
}

func TestPentagramNonzeroVersusEvenOdd(t *testing.T) {
	star := taskpkg.RingFromInts(
		[2]int{5, 0},
		[2]int{8, 12},
		[2]int{0, 5},
		[2]int{10, 5},
		[2]int{2, 12},
	)
	nonzero := taskpkg.NewRegion(taskpkg.Nonzero, star)
	evenOdd := taskpkg.NewRegion(taskpkg.EvenOdd, star)

	var deep taskpkg.Point
	found := false
	for i := 0; i <= 20 && !found; i++ {
		for j := 0; j <= 24 && !found; j++ {
			p := taskpkg.PointFromFraction(int64(2*i+1), 2, int64(2*j+1), 2)
			if nonzero.OnBoundary(p) {
				continue
			}
			w := nonzero.WindingNumber(p)
			if w >= 2 || w <= -2 {
				deep = p
				found = true
			}
		}
	}
	if !found {
		t.Fatal("pentagram must have a region of winding magnitude at least 2")
	}
	if !nonzero.ContainsPoint(deep) {
		t.Fatal("nonzero pentagram lost the winding-2 region")
	}
	if evenOdd.ContainsPoint(deep) {
		t.Fatal("even-odd pentagram should exclude the winding-2 region")
	}

	resN := taskpkg.Union(nonzero, emptyRegion())
	resE := taskpkg.Union(evenOdd, emptyRegion())
	if resN.Area().Cmp(resE.Area()) <= 0 {
		t.Fatalf("nonzero area %s should exceed even-odd area %s", resN.Area(), resE.Area())
	}
	if !resN.ContainsPoint(deep) || resE.ContainsPoint(deep) {
		t.Fatal("pentagram fill rule changed during clipping")
	}

	for _, region := range []taskpkg.Region{nonzero, evenOdd} {
		sameArea := regionArea(t, region)
		requireRat(t, taskpkg.Union(region, region).Area(), sameArea)
		requireRat(t, taskpkg.Intersection(region, region).Area(), sameArea)
		if diff := taskpkg.Difference(region, region); !diff.IsEmpty() {
			t.Fatalf("self-difference left %d outers and %d holes", len(diff.Outers), len(diff.Holes))
		}
	}
}
