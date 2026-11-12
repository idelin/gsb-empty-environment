package taskpkg_test

import (
	"math/big"
	"testing"

	"example.org/domainreissue/q046"
)

func pt(x, y int) taskpkg.Point {
	return taskpkg.PointFromInt(x, y)
}

func rect(x0, y0, x1, y1 int) taskpkg.Ring {
	return taskpkg.RingFromInts(
		[2]int{x0, y0},
		[2]int{x1, y0},
		[2]int{x1, y1},
		[2]int{x0, y1},
	)
}

func rectCW(x0, y0, x1, y1 int) taskpkg.Ring {
	return taskpkg.RingFromInts(
		[2]int{x0, y0},
		[2]int{x0, y1},
		[2]int{x1, y1},
		[2]int{x1, y0},
	)
}

func rat(n, d int64) *big.Rat {
	return taskpkg.RatFromInts(n, d)
}

func requireRat(t *testing.T, got, want *big.Rat) {
	t.Helper()
	if got.Cmp(want) != 0 {
		t.Fatalf("got %s, want %s", got.RatString(), want.RatString())
	}
}

func requireArea(t *testing.T, r taskpkg.Result, n, d int64) {
	t.Helper()
	requireRat(t, r.Area(), rat(n, d))
}

func emptyRegion() taskpkg.Region {
	return taskpkg.Region{FillRule: taskpkg.Nonzero}
}

func regionArea(t *testing.T, r taskpkg.Region) *big.Rat {
	t.Helper()
	return taskpkg.Union(r, emptyRegion()).Area()
}

func expectedOp(op taskpkg.Op, a, b bool) bool {
	switch op {
	case taskpkg.OpUnion:
		return a || b
	case taskpkg.OpDifference:
		return a && !b
	default:
		return a && b
	}
}

func TestRectangleOperations(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 4, 3))
	b := taskpkg.NewRegion(taskpkg.Nonzero, rect(2, 1, 6, 4))

	union := taskpkg.Union(a, b)
	inter := taskpkg.Intersection(a, b)
	diff := taskpkg.Difference(a, b)

	requireArea(t, union, 20, 1)
	requireArea(t, inter, 4, 1)
	requireArea(t, diff, 8, 1)
	if len(union.Outers) != 1 || len(union.Holes) != 0 {
		t.Fatalf("union shape: %d outers, %d holes", len(union.Outers), len(union.Holes))
	}
	if len(inter.Outers) != 1 || len(inter.Holes) != 0 {
		t.Fatalf("intersection shape: %d outers, %d holes", len(inter.Outers), len(inter.Holes))
	}
}

func TestDifferenceCreatesHoleAndProvenance(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 6, 6))
	b := taskpkg.NewRegion(taskpkg.Nonzero, rect(2, 2, 4, 4))
	diff := taskpkg.Difference(a, b)

	requireArea(t, diff, 32, 1)
	if len(diff.Outers) != 1 || len(diff.Holes) != 1 {
		t.Fatalf("shape: %d outers, %d holes", len(diff.Outers), len(diff.Holes))
	}
	if diff.Outers[0].SignedArea().Sign() <= 0 {
		t.Fatal("outer must be counter-clockwise")
	}
	if diff.Holes[0].SignedArea().Sign() >= 0 {
		t.Fatal("hole must be clockwise")
	}
	if len(diff.Components) != 1 || len(diff.Components[0].Holes) != 1 {
		t.Fatalf("component grouping is wrong")
	}
	for _, edge := range diff.Holes[0].Edges {
		if len(edge.Sources) == 0 {
			t.Fatal("hole edge lost provenance")
		}
		foundB := false
		for _, source := range edge.Sources {
			if source.Operand == 1 && source.Ring == 0 {
				foundB = true
			}
		}
		if !foundB {
			t.Fatalf("hole edge sources do not mention operand B: %+v", edge.Sources)
		}
	}
}

func TestFillRuleIsPreserved(t *testing.T) {
	doubleWound := taskpkg.RingFromInts(
		[2]int{0, 0},
		[2]int{4, 0},
		[2]int{4, 4},
		[2]int{0, 4},
		[2]int{0, 0},
		[2]int{4, 0},
		[2]int{4, 4},
		[2]int{0, 4},
	)
	nonzero := taskpkg.NewRegion(taskpkg.Nonzero, doubleWound)
	evenOdd := taskpkg.NewRegion(taskpkg.EvenOdd, doubleWound)
	center := pt(2, 2)

	if w := nonzero.WindingNumber(center); w != 2 {
		t.Fatalf("winding = %d, want 2", w)
	}
	if !nonzero.ContainsPoint(center) {
		t.Fatal("nonzero double-wound ring should contain its center")
	}
	if evenOdd.ContainsPoint(center) {
		t.Fatal("even-odd double-wound ring must not contain its center")
	}

	u1 := taskpkg.Union(nonzero, emptyRegion())
	u2 := taskpkg.Union(evenOdd, emptyRegion())
	requireArea(t, u1, 16, 1)
	requireArea(t, u2, 0, 1)
	if !u1.ContainsPoint(center) {
		t.Fatal("nonzero result lost containment")
	}
	if u2.ContainsPoint(center) {
		t.Fatal("even-odd result gained false area")
	}
}

func TestCrossingPolygonsRespectFillRule(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 3, 3), rect(2, 2, 5, 5))
	e := taskpkg.NewRegion(taskpkg.EvenOdd, rect(0, 0, 3, 3), rect(2, 2, 5, 5))

	center := taskpkg.PointFromFraction(5, 2, 5, 2)
	if !a.ContainsPoint(center) {
		t.Fatal("nonzero overlap should be filled")
	}
	if e.ContainsPoint(center) {
		t.Fatal("even-odd overlap should be empty")
	}

	requireArea(t, taskpkg.Union(a, emptyRegion()), 17, 1)
	requireArea(t, taskpkg.Union(e, emptyRegion()), 16, 1)
	resA := taskpkg.Union(a, emptyRegion())
	resE := taskpkg.Union(e, emptyRegion())
	for i := 0; i <= 5; i++ {
		for j := 0; j <= 5; j++ {
			p := taskpkg.PointFromFraction(int64(2*i+1), 2, int64(2*j+1), 2)
			if resA.ContainsPoint(p) != a.ContainsPoint(p) {
				t.Fatalf("nonzero clipping changed cell (%d,%d)", i, j)
			}
			if resE.ContainsPoint(p) != e.ContainsPoint(p) {
				t.Fatalf("even-odd clipping changed cell (%d,%d)", i, j)
			}
		}
	}

	// A crossing rectangle must preserve the same fill semantics when clipped.
	b := taskpkg.NewRegion(taskpkg.Nonzero, rect(1, 1, 4, 4))
	inter := taskpkg.Intersection(a, b)
	requireArea(t, inter, 7, 1)
	if !inter.ContainsPoint(center) {
		t.Fatal("intersection lost the nonzero overlap")
	}
	interE := taskpkg.Intersection(e, b)
	for i := 0; i <= 5; i++ {
		for j := 0; j <= 5; j++ {
			p := taskpkg.PointFromFraction(int64(2*i+1), 2, int64(2*j+1), 2)
			want := e.ContainsPoint(p) && b.ContainsPoint(p)
			if interE.ContainsPoint(p) != want {
				t.Fatalf("even-odd intersection changed cell (%d,%d)", i, j)
			}
		}
	}
}

func TestCollinearOverlapIsSplitAndMerged(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 4, 2))
	b := taskpkg.NewRegion(taskpkg.Nonzero, rect(2, 0, 6, 2))
	arr := taskpkg.BuildArrangement(a, b)

	var overlap *taskpkg.ArrangedEdge
	for i := range arr.Edges {
		e := &arr.Edges[i]
		if (e.From.Equal(pt(2, 0)) && e.To.Equal(pt(4, 0))) ||
			(e.From.Equal(pt(4, 0)) && e.To.Equal(pt(2, 0))) {
			overlap = e
			break
		}
	}
	if overlap == nil {
		t.Fatal("collinear overlap was not split at x=2 and x=4")
	}
	if overlap.Multiplicity() != 2 {
		t.Fatalf("overlap multiplicity = %d, want 2", overlap.Multiplicity())
	}
	operands := map[int]bool{}
	for _, source := range overlap.Sources {
		operands[source.Operand] = true
	}
	if !operands[0] || !operands[1] {
		t.Fatalf("overlap provenance = %+v", overlap.Sources)
	}

	union := taskpkg.Union(a, b)
	requireArea(t, union, 12, 1)
	if len(union.Outers) != 1 {
		t.Fatalf("union outers = %d, want 1", len(union.Outers))
	}
}

func TestPointAndEdgeContactHaveNoFalseArea(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 2, 2))
	corner := taskpkg.NewRegion(taskpkg.Nonzero, rect(2, 2, 4, 4))
	edge := taskpkg.NewRegion(taskpkg.Nonzero, rect(2, 0, 4, 2))

	cornerInter := taskpkg.Intersection(a, corner)
	requireArea(t, cornerInter, 0, 1)
	if !cornerInter.IsEmpty() {
		t.Fatal("corner-only contact produced a boundary or area")
	}
	cornerUnion := taskpkg.Union(a, corner)
	requireArea(t, cornerUnion, 8, 1)
	if len(cornerUnion.Outers) != 2 {
		t.Fatalf("corner contact merged into %d outers, want 2", len(cornerUnion.Outers))
	}

	edgeInter := taskpkg.Intersection(a, edge)
	requireArea(t, edgeInter, 0, 1)
	if !edgeInter.IsEmpty() {
		t.Fatal("edge-only contact produced a boundary or area")
	}
	edgeUnion := taskpkg.Union(a, edge)
	requireArea(t, edgeUnion, 8, 1)
	if len(edgeUnion.Outers) != 1 {
		t.Fatalf("edge contact union outers = %d, want 1", len(edgeUnion.Outers))
	}
	diff := taskpkg.Difference(a, edge)
	requireArea(t, diff, 4, 1)
}

func TestRationalNearbyIntersectionsAreNotMerged(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 1, 1))
	x0 := taskpkg.PointFromFraction(1, 1000000, 0, 1).X
	b := taskpkg.NewRegion(taskpkg.Nonzero, taskpkg.MakeRing(
		taskpkg.NewPoint(x0, rat(1, 3)),
		taskpkg.NewPoint(rat(1000001, 1000000), rat(1, 3)),
		taskpkg.NewPoint(rat(1000001, 1000000), rat(4, 3)),
		taskpkg.NewPoint(x0, rat(4, 3)),
	))
	inter := taskpkg.Intersection(a, b)
	requireArea(t, inter, 333333, 500000)
	if inter.IsEmpty() || len(inter.Outers) != 1 {
		t.Fatalf("nearby rational overlap was collapsed: %d outers", len(inter.Outers))
	}
}

func TestGridReferenceCommutativityAndAreaIdentities(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 3, 2), rect(1, 1, 4, 3))
	// Concave integer L-shape.
	b := taskpkg.NewRegion(taskpkg.Nonzero, taskpkg.RingFromInts(
		[2]int{2, 0},
		[2]int{5, 0},
		[2]int{5, 1},
		[2]int{3, 1},
		[2]int{3, 3},
		[2]int{2, 3},
	))

	areaA := regionArea(t, a)
	areaB := regionArea(t, b)

	for _, op := range []taskpkg.Op{taskpkg.OpUnion, taskpkg.OpIntersect, taskpkg.OpDifference} {
		res := taskpkg.Boolean(a, b, op)
		for i := 0; i <= 6; i++ {
			for j := 0; j <= 4; j++ {
				center := taskpkg.PointFromFraction(int64(2*i+1), 2, int64(2*j+1), 2)
				want := expectedOp(op, a.ContainsPoint(center), b.ContainsPoint(center))
				if got := res.ContainsPoint(center); got != want {
					t.Fatalf("%s at cell (%d,%d): got %v, want %v", op, i, j, got, want)
				}
			}
		}
	}

	unionAB := taskpkg.Union(a, b)
	unionBA := taskpkg.Union(b, a)
	interAB := taskpkg.Intersection(a, b)
	interBA := taskpkg.Intersection(b, a)
	requireRat(t, unionAB.Area(), unionBA.Area())
	requireRat(t, interAB.Area(), interBA.Area())
	for i := 0; i <= 6; i++ {
		for j := 0; j <= 4; j++ {
			center := taskpkg.PointFromFraction(int64(2*i+1), 2, int64(2*j+1), 2)
			if unionAB.ContainsPoint(center) != unionBA.ContainsPoint(center) {
				t.Fatalf("union is not commutative at cell (%d,%d)", i, j)
			}
			if interAB.ContainsPoint(center) != interBA.ContainsPoint(center) {
				t.Fatalf("intersection is not commutative at cell (%d,%d)", i, j)
			}
		}
	}

	unionArea := unionAB.Area()
	interArea := interAB.Area()
	diffArea := taskpkg.Difference(a, b).Area()
	sum := new(big.Rat).Add(unionArea, interArea)
	want := new(big.Rat).Add(areaA, areaB)
	requireRat(t, sum, want)

	sum = new(big.Rat).Add(diffArea, interArea)
	requireRat(t, sum, areaA)
}

func TestDifferenceAreaIdentityWithHole(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 8, 8))
	b := taskpkg.NewRegion(taskpkg.Nonzero, rect(2, 2, 6, 6))
	inter := taskpkg.Intersection(a, b)
	diff := taskpkg.Difference(a, b)
	areaA := regionArea(t, a)
	sum := new(big.Rat).Add(diff.Area(), inter.Area())
	requireRat(t, sum, areaA)
}

func TestSelfTouchingLobesStaySeparate(t *testing.T) {
	figureEight := taskpkg.NewRegion(taskpkg.Nonzero, taskpkg.RingFromInts(
		[2]int{0, 0},
		[2]int{2, 0},
		[2]int{2, 2},
		[2]int{2, 4},
		[2]int{4, 4},
		[2]int{4, 2},
		[2]int{2, 2},
		[2]int{0, 2},
	))
	res := taskpkg.Union(figureEight, emptyRegion())
	requireArea(t, res, 8, 1)
	if len(res.Outers) != 2 {
		t.Fatalf("point-touching lobes became %d outers, want 2", len(res.Outers))
	}
	if res.ContainsPoint(taskpkg.PointFromFraction(1, 1, 3, 1)) {
		t.Fatal("point contact was spuriously filled")
	}
}

func TestOppositeCollinearOccurrences(t *testing.T) {
	a := taskpkg.NewRegion(taskpkg.Nonzero, rect(0, 0, 4, 3))
	b := taskpkg.NewRegion(taskpkg.Nonzero, rectCW(0, 0, 4, 3))
	arr := taskpkg.BuildArrangement(a, b)
	for _, edge := range arr.Edges {
		if edge.Multiplicity() != 2 || edge.Forward != 1 || edge.Reverse != 1 || edge.Direction != 0 {
			t.Fatalf("opposite overlap not merged: %+v", edge)
		}
	}

	union := taskpkg.Union(a, b)
	requireArea(t, union, 12, 1)
	inter := taskpkg.Intersection(a, b)
	requireArea(t, inter, 12, 1)
	diff := taskpkg.Difference(a, b)
	if !diff.IsEmpty() {
		t.Fatalf("A \\ A produced %d outers and %d holes", len(diff.Outers), len(diff.Holes))
	}
}

func TestRectangleGridExhaustive(t *testing.T) {
	rectangles := [][4]int{
		{0, 0, 2, 2},
		{1, 1, 3, 3},
		{2, 0, 4, 2},
		{0, 2, 2, 4},
		{2, 2, 4, 4},
		{0, 0, 4, 2},
		{1, 1, 2, 2},
		{-2, -1, 0, 1},
		{-1, 0, 2, 1},
		{1, 0, 3, 3},
		{2, 1, 5, 2},
		{-2, 2, 5, 3},
	}
	for i := 0; i < len(rectangles); i++ {
		for j := i + 1; j < len(rectangles); j++ {
			ra := rectangles[i]
			rb := rectangles[j]
			var ringA, ringB taskpkg.Ring
			if (i+j)%2 == 0 {
				ringA = rect(ra[0], ra[1], ra[2], ra[3])
				ringB = rectCW(rb[0], rb[1], rb[2], rb[3])
			} else {
				ringA = rectCW(ra[0], ra[1], ra[2], ra[3])
				ringB = rect(rb[0], rb[1], rb[2], rb[3])
			}
			a := taskpkg.NewRegion(taskpkg.Nonzero, ringA)
			b := taskpkg.NewRegion(taskpkg.Nonzero, ringB)
			areaA := regionArea(t, a)
			areaB := regionArea(t, b)

			for _, op := range []taskpkg.Op{taskpkg.OpUnion, taskpkg.OpIntersect, taskpkg.OpDifference} {
				res := taskpkg.Boolean(a, b, op)
				for x := -5; x <= 11; x++ {
					for y := -3; y <= 9; y++ {
						center := taskpkg.PointFromFraction(int64(2*x+1), 2, int64(2*y+1), 2)
						want := expectedOp(op, a.ContainsPoint(center), b.ContainsPoint(center))
						got := res.ContainsPoint(center)
						if got != want {
							t.Fatalf("rect %d,%d %s at (%d,%d): got %v want %v", i, j, op, x, y, got, want)
						}
					}
				}
			}

			union := taskpkg.Union(a, b)
			inter := taskpkg.Intersection(a, b)
			diff := taskpkg.Difference(a, b)
			requireRat(t, new(big.Rat).Add(union.Area(), inter.Area()), new(big.Rat).Add(areaA, areaB))
			requireRat(t, new(big.Rat).Add(diff.Area(), inter.Area()), areaA)
			requireRat(t, union.Area(), taskpkg.Union(b, a).Area())
			requireRat(t, inter.Area(), taskpkg.Intersection(b, a).Area())
		}
	}
}
