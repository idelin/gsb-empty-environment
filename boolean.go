package taskpkg

import (
	"fmt"
	"math/big"
	"sort"
)

// Boolean applies op to two operands and returns a canonical boundary result.
// Invalid input panics; use BooleanE when errors must be returned.
func Boolean(a, b Region, op Op) Result {
	r, err := BooleanE(a, b, op)
	if err != nil {
		panic(err)
	}
	return r
}

// BooleanE is the error-returning form of Boolean.
func BooleanE(a, b Region, op Op) (Result, error) {
	if op != OpIntersect && op != OpUnion && op != OpDifference {
		return Result{}, fmt.Errorf("taskpkg: unknown Boolean operation %d", op)
	}
	arr, _, err := buildArrangement([]Region{a, b})
	if err != nil {
		return Result{}, err
	}
	return booleanArrangement([]Region{a, b}, arr, op)
}

// Intersection returns A ∩ B.
func Intersection(a, b Region) Result {
	return Boolean(a, b, OpIntersect)
}

// Intersect is an alias for Intersection.
func Intersect(a, b Region) Result {
	return Intersection(a, b)
}

// Union returns A ∪ B.
func Union(a, b Region) Result {
	return Boolean(a, b, OpUnion)
}

// Difference returns A \ B.
func Difference(a, b Region) Result {
	return Boolean(a, b, OpDifference)
}

// IntersectionE is the error-returning form of Intersection.
func IntersectionE(a, b Region) (Result, error) {
	return BooleanE(a, b, OpIntersect)
}

// UnionE is the error-returning form of Union.
func UnionE(a, b Region) (Result, error) {
	return BooleanE(a, b, OpUnion)
}

// DifferenceE is the error-returning form of Difference.
func DifferenceE(a, b Region) (Result, error) {
	return BooleanE(a, b, OpDifference)
}

// Boolean applies a set operation with the receiver as the left operand.
func (r Region) Boolean(other Region, op Op) Result {
	return Boolean(r, other, op)
}

// Intersection returns r ∩ other.
func (r Region) Intersection(other Region) Result {
	return Intersection(r, other)
}

// Intersect is an alias for Intersection.
func (r Region) Intersect(other Region) Result {
	return Intersection(r, other)
}

// Union returns r ∪ other.
func (r Region) Union(other Region) Result {
	return Union(r, other)
}

// Difference returns r \ other.
func (r Region) Difference(other Region) Result {
	return Difference(r, other)
}

// Union applies a union with the result as the left operand.
func (r Result) Union(other Region) Result {
	return Union(r.Region(), other)
}

// Intersection applies an intersection with the result as the left operand.
func (r Result) Intersection(other Region) Result {
	return Intersection(r.Region(), other)
}

// Difference applies a difference with the result as the left operand.
func (r Result) Difference(other Region) Result {
	return Difference(r.Region(), other)
}

type directedHalfEdge struct {
	from    int
	to      int
	sources []EdgeRef
}

func booleanArrangement(regions []Region, arr Arrangement, op Op) (Result, error) {
	vertices := arr.Vertices
	if len(arr.Edges) == 0 {
		return Result{}, nil
	}

	var halves []directedHalfEdge
	for _, edge := range arr.Edges {
		a, b := edge.FromIndex, edge.ToIndex
		dx := ratSub(vertices[b].X, vertices[a].X)
		dy := ratSub(vertices[b].Y, vertices[a].Y)
		midX := ratHalf(ratAdd(vertices[a].X, vertices[b].X))
		midY := ratHalf(ratAdd(vertices[a].Y, vertices[b].Y))
		leftNormalX := ratNeg(dy)
		leftNormalY := dx

		delta := chooseSideDelta(Point{X: midX, Y: midY}, Point{X: leftNormalX, Y: leftNormalY}, arr)
		left := Point{
			X: ratAdd(midX, ratMul(delta, leftNormalX)),
			Y: ratAdd(midY, ratMul(delta, leftNormalY)),
		}
		right := Point{
			X: ratSub(midX, ratMul(delta, leftNormalX)),
			Y: ratSub(midY, ratMul(delta, leftNormalY)),
		}

		leftInside := booleanAt(regions, op, left)
		rightInside := booleanAt(regions, op, right)
		if leftInside == rightInside {
			continue
		}

		from, to := a, b
		flip := false
		if !leftInside {
			from, to = b, a
			flip = true
		}

		sources := make([]EdgeRef, len(edge.Sources))
		for i, source := range edge.Sources {
			sources[i] = source
			if flip {
				sources[i].Direction = -sources[i].Direction
			}
		}
		sortEdgeRefs(sources)
		halves = append(halves, directedHalfEdge{from: from, to: to, sources: sources})
	}

	if len(halves) == 0 {
		return Result{}, nil
	}
	contours, err := assembleContours(vertices, halves)
	if err != nil {
		return Result{}, err
	}
	return classifyContours(contours)
}

func booleanAt(regions []Region, op Op, p Point) bool {
	a := regions[0].ContainsPoint(p)
	b := regions[1].ContainsPoint(p)
	return op.eval(a, b)
}

// chooseSideDelta picks an exact rational offset that stays inside the two
// faces adjacent to an arrangement edge. It never rounds or uses a floating
// point epsilon. The offset is smaller than every nonzero intersection of the
// edge normal line with any other atomic edge.
func chooseSideDelta(mid, normal Point, arr Arrangement) *big.Rat {
	var minAbs *big.Rat
	for _, edge := range arr.Edges {
		a := arr.Vertices[edge.FromIndex]
		b := arr.Vertices[edge.ToIndex]
		for _, t := range normalLineIntersections(mid, normal, a, b) {
			if t.Sign() == 0 {
				continue
			}
			abs := ratAbs(t)
			if minAbs == nil || abs.Cmp(minAbs) < 0 {
				minAbs = abs
			}
		}
	}
	if minAbs == nil {
		return big.NewRat(1, 1)
	}
	return ratHalf(minAbs)
}

func normalLineIntersections(mid, normal, a, b Point) []*big.Rat {
	sx := ratSub(b.X, a.X)
	sy := ratSub(b.Y, a.Y)
	den := crossRat(normal.X, normal.Y, sx, sy)
	qpx := ratSub(a.X, mid.X)
	qpy := ratSub(a.Y, mid.Y)

	if den.Sign() != 0 {
		t := ratQuo(crossRat(qpx, qpy, sx, sy), den)
		u := ratQuo(crossRat(qpx, qpy, normal.X, normal.Y), den)
		if u.Sign() >= 0 && u.Cmp(big.NewRat(1, 1)) <= 0 {
			return []*big.Rat{t}
		}
		return nil
	}

	if crossRat(qpx, qpy, normal.X, normal.Y).Sign() != 0 {
		return nil
	}
	denNormal := ratMul(normal.X, normal.X)
	denNormal.Add(denNormal, ratMul(normal.Y, normal.Y))
	if denNormal.Sign() == 0 {
		return nil
	}
	t0 := ratQuo(ratAdd(ratMul(qpx, normal.X), ratMul(qpy, normal.Y)), denNormal)
	t1 := ratQuo(
		ratAdd(ratMul(ratSub(b.X, mid.X), normal.X), ratMul(ratSub(b.Y, mid.Y), normal.Y)),
		denNormal,
	)
	return []*big.Rat{t0, t1}
}

func assembleContours(vertices []Point, halves []directedHalfEdge) ([]Contour, error) {
	type outgoingRef struct {
		index int
		dx    *big.Rat
		dy    *big.Rat
	}
	outgoing := make(map[int][]outgoingRef)
	for i, h := range halves {
		outgoing[h.from] = append(outgoing[h.from], outgoingRef{
			index: i,
			dx:    ratSub(vertices[h.to].X, vertices[h.from].X),
			dy:    ratSub(vertices[h.to].Y, vertices[h.from].Y),
		})
	}
	for vertex := range outgoing {
		refs := outgoing[vertex]
		sort.Slice(refs, func(i, j int) bool {
			return angleLess(refs[i].dx, refs[i].dy, refs[j].dx, refs[j].dy)
		})
		outgoing[vertex] = refs
	}

	next := make([]int, len(halves))
	for i, h := range halves {
		refs := outgoing[h.to]
		if len(refs) == 0 {
			return nil, fmt.Errorf("taskpkg: open boundary at vertex %d", h.to)
		}
		revX := ratSub(vertices[h.from].X, vertices[h.to].X)
		revY := ratSub(vertices[h.from].Y, vertices[h.to].Y)
		lo := sort.Search(len(refs), func(k int) bool {
			return !angleBeforeStrict(refs[k].dx, refs[k].dy, revX, revY)
		})
		pred := (lo - 1 + len(refs)) % len(refs)
		next[i] = refs[pred].index
	}

	seen := make([]bool, len(halves))
	var contours []Contour
	for start := 0; start < len(halves); start++ {
		if seen[start] {
			continue
		}
		var contour Contour
		cur := start
		for {
			if cur < 0 || cur >= len(halves) {
				return nil, fmt.Errorf("taskpkg: invalid boundary link")
			}
			if seen[cur] {
				if cur == start {
					break
				}
				return nil, fmt.Errorf("taskpkg: boundary links do not form disjoint cycles")
			}
			seen[cur] = true
			h := halves[cur]
			contour.Points = append(contour.Points, clonePoint(vertices[h.from]))
			contour.Edges = append(contour.Edges, ContourEdge{
				From:    clonePoint(vertices[h.from]),
				To:      clonePoint(vertices[h.to]),
				Sources: cloneEdgeRefs(h.sources),
			})
			cur = next[cur]
			if cur == start {
				break
			}
		}
		if len(contour.Points) < 3 {
			continue
		}
		if contour.SignedArea().Sign() == 0 {
			continue
		}
		contours = append(contours, contour)
	}
	return contours, nil
}

func classifyContours(contours []Contour) (Result, error) {
	var outers, holes []Contour
	for _, c := range contours {
		if c.SignedArea().Sign() > 0 {
			outers = append(outers, c)
		} else {
			holes = append(holes, c)
		}
	}
	sortContours(outers)
	sortContours(holes)

	var result Result
	result.Outers = cloneContours(outers)
	result.Holes = cloneContours(holes)
	result.Components = make([]Component, 0, len(outers))

	holeToOuter := make([]int, len(holes))
	for i := range holeToOuter {
		holeToOuter[i] = -1
	}
	for hi, hole := range holes {
		if len(hole.Edges) == 0 {
			return Result{}, fmt.Errorf("taskpkg: hole has no probe point")
		}
		for _, edge := range hole.Edges {
			probe := Point{
				X: ratHalf(ratAdd(edge.From.X, edge.To.X)),
				Y: ratHalf(ratAdd(edge.From.Y, edge.To.Y)),
			}
			best := -1
			var bestArea *big.Rat
			for oi, outer := range outers {
				if !pointInContour(outer, probe) {
					continue
				}
				area := outer.Area()
				if best < 0 || area.Cmp(bestArea) < 0 {
					best = oi
					bestArea = area
				}
			}
			if best >= 0 {
				holeToOuter[hi] = best
				break
			}
		}
		if holeToOuter[hi] < 0 {
			return Result{}, fmt.Errorf("taskpkg: hole is not inside an outer contour")
		}
	}

	for oi, outer := range outers {
		component := Component{Outer: cloneContour(outer)}
		for hi, owner := range holeToOuter {
			if owner == oi {
				component.Holes = append(component.Holes, cloneContour(holes[hi]))
			}
		}
		result.Components = append(result.Components, component)
	}
	return result, nil
}

func pointInContour(c Contour, p Point) bool {
	return !contourBoundaryContains(c, p) && windingPoints(c.Points, p) != 0
}

func cloneContour(c Contour) Contour {
	out := Contour{Points: clonePoints(c.Points)}
	out.Edges = make([]ContourEdge, len(c.Edges))
	for i, e := range c.Edges {
		out.Edges[i] = ContourEdge{
			From:    clonePoint(e.From),
			To:      clonePoint(e.To),
			Sources: cloneEdgeRefs(e.Sources),
		}
	}
	return out
}

func cloneContours(cs []Contour) []Contour {
	out := make([]Contour, len(cs))
	for i, c := range cs {
		out[i] = cloneContour(c)
	}
	return out
}

func cloneEdgeRefs(refs []EdgeRef) []EdgeRef {
	return append([]EdgeRef(nil), refs...)
}

func sortContours(cs []Contour) {
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		n := len(a.Points)
		if len(b.Points) < n {
			n = len(b.Points)
		}
		for k := 0; k < n; k++ {
			if c := a.Points[k].Cmp(b.Points[k]); c != 0 {
				return c < 0
			}
		}
		return len(a.Points) < len(b.Points)
	})
}

func angleHalf(dx, dy *big.Rat) int {
	if dy.Sign() > 0 || (dy.Sign() == 0 && dx.Sign() >= 0) {
		return 0
	}
	return 1
}

// angleBefore compares angles in [0,2π) without floating point arithmetic.
// Same-angle vectors are ordered deterministically by squared length.
func angleBefore(ax, ay, bx, by *big.Rat) bool {
	ha, hb := angleHalf(ax, ay), angleHalf(bx, by)
	if ha != hb {
		return ha < hb
	}
	cross := crossRat(ax, ay, bx, by)
	if cross.Sign() != 0 {
		return cross.Sign() > 0
	}
	na := ratMul(ax, ax)
	na.Add(na, ratMul(ay, ay))
	nb := ratMul(bx, bx)
	nb.Add(nb, ratMul(by, by))
	return na.Cmp(nb) < 0
}

func angleLess(ax, ay, bx, by *big.Rat) bool {
	ha, hb := angleHalf(ax, ay), angleHalf(bx, by)
	if ha != hb {
		return ha < hb
	}
	cross := crossRat(ax, ay, bx, by)
	if cross.Sign() != 0 {
		return cross.Sign() > 0
	}
	return angleBefore(ax, ay, bx, by)
}

// angleBeforeStrict is the angle-only comparison used by binary search. It
// deliberately treats vectors on the same ray as equal.
func angleBeforeStrict(ax, ay, bx, by *big.Rat) bool {
	ha, hb := angleHalf(ax, ay), angleHalf(bx, by)
	if ha != hb {
		return ha < hb
	}
	cross := crossRat(ax, ay, bx, by)
	return cross.Sign() > 0
}
