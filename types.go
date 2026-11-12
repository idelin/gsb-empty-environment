package taskpkg

import (
	"fmt"
	"math/big"
)

// FillRule selects how a closed ring or a collection of rings defines area.
type FillRule uint8

const (
	// FillNonzero keeps points whose winding number is not zero.
	FillNonzero FillRule = iota
	// FillEvenOdd keeps points crossed an odd number of times by a ray.
	FillEvenOdd
)

const (
	Nonzero = FillNonzero
	NonZero = FillNonzero
	EvenOdd = FillEvenOdd
	OddEven = FillEvenOdd

	FillRuleNonzero = FillNonzero
	FillRuleEvenOdd = FillEvenOdd
)

func (r FillRule) String() string {
	if r == FillEvenOdd {
		return "even-odd"
	}
	return "nonzero"
}

// Op is a set operation on two regions.
type Op uint8

const (
	OpIntersect Op = iota
	OpUnion
	OpDifference
)

const (
	IntersectOp    = OpIntersect
	UnionOp        = OpUnion
	DifferenceOp   = OpDifference
	OpIntersection = OpIntersect
)

func (op Op) String() string {
	switch op {
	case OpUnion:
		return "union"
	case OpDifference:
		return "difference"
	default:
		return "intersection"
	}
}

func (op Op) eval(a, b bool) bool {
	switch op {
	case OpUnion:
		return a || b
	case OpDifference:
		return a && !b
	default:
		return a && b
	}
}

// Point is an exact point. The rationals are treated as immutable by this
// package; methods never modify a Point supplied by the caller.
type Point struct {
	X *big.Rat
	Y *big.Rat
}

// NewPoint makes an exact point and copies both rationals.
func NewPoint(x, y *big.Rat) Point {
	if x == nil || y == nil {
		panic("taskpkg: nil rational coordinate")
	}
	return Point{X: cloneRat(x), Y: cloneRat(y)}
}

// PointFromRat is an alias for NewPoint.
func PointFromRat(x, y *big.Rat) Point {
	return NewPoint(x, y)
}

// RatFromInts returns n/d in canonical form.
func RatFromInts(n, d int64) *big.Rat {
	if d == 0 {
		panic("taskpkg: zero rational denominator")
	}
	return big.NewRat(n, d)
}

type signedInteger interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

// PointFromInt makes a point with integer coordinates.
func PointFromInt[T signedInteger](x, y T) Point {
	return Point{X: big.NewRat(int64(x), 1), Y: big.NewRat(int64(y), 1)}
}

// NewPointInt is an alias for PointFromInt.
func NewPointInt[T signedInteger](x, y T) Point {
	return PointFromInt(x, y)
}

// PointFromFraction makes a point from exact fractions x=xn/xd and y=yn/yd.
func PointFromFraction(xn, xd, yn, yd int64) Point {
	if xd == 0 || yd == 0 {
		panic("taskpkg: zero rational denominator")
	}
	return Point{X: big.NewRat(xn, xd), Y: big.NewRat(yn, yd)}
}

func (p Point) String() string {
	return fmt.Sprintf("(%s,%s)", p.X.RatString(), p.Y.RatString())
}

// Equal compares coordinates exactly.
func (p Point) Equal(q Point) bool {
	return p.X.Cmp(q.X) == 0 && p.Y.Cmp(q.Y) == 0
}

// Cmp orders points first by x and then by y.
func (p Point) Cmp(q Point) int {
	if c := p.X.Cmp(q.X); c != 0 {
		return c
	}
	return p.Y.Cmp(q.Y)
}

func pointKey(p Point) string {
	return p.X.RatString() + "\x00" + p.Y.RatString()
}

func clonePoint(p Point) Point {
	return Point{X: cloneRat(p.X), Y: cloneRat(p.Y)}
}

func validPoint(p Point) bool {
	return p.X != nil && p.Y != nil
}

// Ring is a closed sequence of points. The closing edge from the last point
// to the first point is implicit; repeating the first point at the end is
// allowed and produces a zero-length edge that is ignored.
type Ring struct {
	Points []Point
	Label  string
}

// NewRing copies a point slice into a closed ring.
func NewRing(points []Point) Ring {
	out := make([]Point, len(points))
	for i, p := range points {
		out[i] = clonePoint(p)
	}
	return Ring{Points: out}
}

// RingFromPoints is an alias for NewRing.
func RingFromPoints(points []Point) Ring {
	return NewRing(points)
}

// MakeRing is a variadic convenience constructor.
func MakeRing(points ...Point) Ring {
	return NewRing(points)
}

// RingFromInts builds a ring whose vertices are integer pairs.
func RingFromInts[T signedInteger](coords ...[2]T) Ring {
	points := make([]Point, len(coords))
	for i, c := range coords {
		points[i] = PointFromInt(c[0], c[1])
	}
	return NewRing(points)
}

// RingFromXY builds a ring from parallel x and y coordinate slices.
func RingFromXY(xs, ys []int64) Ring {
	n := len(xs)
	if len(ys) < n {
		n = len(ys)
	}
	points := make([]Point, n)
	for i := 0; i < n; i++ {
		points[i] = PointFromInt(xs[i], ys[i])
	}
	return NewRing(points)
}

// ClosedPoints returns the vertices followed by a copy of the first vertex.
func (r Ring) ClosedPoints() []Point {
	if len(r.Points) == 0 {
		return nil
	}
	out := make([]Point, len(r.Points)+1)
	for i, p := range r.Points {
		out[i] = clonePoint(p)
	}
	out[len(r.Points)] = clonePoint(r.Points[0])
	return out
}

// SignedArea returns the algebraic shoelace area of the ring.
func (r Ring) SignedArea() *big.Rat {
	return signedAreaOfPoints(r.Points)
}

// Area returns the absolute shoelace area of the ring.
func (r Ring) Area() *big.Rat {
	return ratAbs(r.SignedArea())
}

// Region is an input operand. FillRule interprets all rings together.
type Region struct {
	Rings    []Ring
	FillRule FillRule
}

// NewRegion copies rings into an operand.
func NewRegion(rule FillRule, rings ...Ring) Region {
	out := make([]Ring, len(rings))
	for i, r := range rings {
		out[i] = NewRing(r.Points)
		out[i].Label = r.Label
	}
	return Region{Rings: out, FillRule: rule}
}

// RegionFromRings is a non-variadic convenience constructor.
func RegionFromRings(rule FillRule, rings []Ring) Region {
	return NewRegion(rule, rings...)
}

// WindingNumber returns the nonzero-rule winding number at p.
func (r Region) WindingNumber(p Point) int {
	total := 0
	for _, ring := range r.Rings {
		total += windingPoints(ring.Points, p)
	}
	return total
}

// ContainsPoint reports whether p is strictly inside the operand. Points on
// an input edge return false.
func (r Region) ContainsPoint(p Point) bool {
	if regionBoundaryContains(r, p) {
		return false
	}
	w := r.WindingNumber(p)
	if r.FillRule == FillEvenOdd {
		return w%2 != 0
	}
	return w != 0
}

// OnBoundary reports whether p lies on an input ring edge.
func (r Region) OnBoundary(p Point) bool {
	return regionBoundaryContains(r, p)
}

// EdgeRef identifies an original input edge. Direction is +1 when the source
// edge runs in the same direction as the output contour edge, and -1 when it
// runs in the opposite direction.
type EdgeRef struct {
	Operand   int
	Ring      int
	Edge      int
	Label     string
	Direction int
}

// ContourEdge is one directed edge of an output contour. Its sources retain
// the original operand, ring, and segment indices.
type ContourEdge struct {
	From    Point
	To      Point
	Sources []EdgeRef
}

// Contour is a closed output boundary. Points contains one vertex per edge;
// the edge from the last point to the first point is implicit and is also
// present in Edges.
type Contour struct {
	Points []Point
	Edges  []ContourEdge
}

// SignedArea is positive for outer boundaries and negative for holes.
func (c Contour) SignedArea() *big.Rat {
	return signedAreaOfPoints(c.Points)
}

// Area returns the absolute area enclosed by the contour.
func (c Contour) Area() *big.Rat {
	return ratAbs(c.SignedArea())
}

// ClosedPoints returns the contour vertices followed by the first vertex.
func (c Contour) ClosedPoints() []Point {
	if len(c.Points) == 0 {
		return nil
	}
	out := make([]Point, len(c.Points)+1)
	for i, p := range c.Points {
		out[i] = clonePoint(p)
	}
	out[len(c.Points)] = clonePoint(c.Points[0])
	return out
}

// ContainsPoint reports containment in the geometric polygon of the contour,
// ignoring its role as an outer boundary or hole.
func (c Contour) ContainsPoint(p Point) bool {
	if contourBoundaryContains(c, p) {
		return false
	}
	return windingPoints(c.Points, p) != 0
}

// OnBoundary reports whether p lies on an edge of the contour.
func (c Contour) OnBoundary(p Point) bool {
	return contourBoundaryContains(c, p)
}

// Component is one outer boundary with the holes directly inside it.
type Component struct {
	Outer Contour
	Holes []Contour
}

// Result is a canonical boundary representation of a Boolean result. Outer
// contours run counter-clockwise and holes run clockwise, so the sum of all
// signed contour areas is the net area.
type Result struct {
	Outers     []Contour
	Holes      []Contour
	Components []Component
}

// Output is an alias for Result.
type Output = Result

// Contours returns all outer and hole contours in deterministic order.
func (r Result) Contours() []Contour {
	out := make([]Contour, 0, len(r.Outers)+len(r.Holes))
	out = append(out, r.Outers...)
	out = append(out, r.Holes...)
	return out
}

// Rings returns the result contours as closed input rings using the nonzero
// rule. The returned slice is the Rings field of Result.Region().
func (r Result) Rings() []Ring {
	rings := make([]Ring, 0, len(r.Outers)+len(r.Holes))
	for _, c := range r.Contours() {
		rings = append(rings, NewRing(c.Points))
	}
	return rings
}

// SignedArea returns the net area with outer contours positive and holes
// negative.
func (r Result) SignedArea() *big.Rat {
	sum := newRat()
	for _, c := range r.Outers {
		sum.Add(sum, c.SignedArea())
	}
	for _, c := range r.Holes {
		sum.Add(sum, c.SignedArea())
	}
	return sum
}

// Area returns the net area of the result.
func (r Result) Area() *big.Rat {
	return r.SignedArea()
}

// IsEmpty reports whether the result has no boundary.
func (r Result) IsEmpty() bool {
	return len(r.Outers) == 0 && len(r.Holes) == 0
}

// ContainsPoint reports whether p is strictly inside the result. The result is
// evaluated with the nonzero rule over its oriented outer and hole contours.
func (r Result) ContainsPoint(p Point) bool {
	if resultBoundaryContains(r, p) {
		return false
	}
	w := 0
	for _, c := range r.Outers {
		w += windingPoints(c.Points, p)
	}
	for _, c := range r.Holes {
		w += windingPoints(c.Points, p)
	}
	return w != 0
}

// OnBoundary reports whether p lies on any result contour edge.
func (r Result) OnBoundary(p Point) bool {
	return resultBoundaryContains(r, p)
}

// Region converts the result back to an input operand using the nonzero rule.
// This is useful for chaining Boolean operations.
func (r Result) Region() Region {
	return Region{Rings: r.Rings(), FillRule: FillNonzero}
}

// AsRegion is an alias for Region.
func (r Result) AsRegion() Region {
	return r.Region()
}

func signedAreaOfPoints(points []Point) *big.Rat {
	n := len(points)
	if n < 3 {
		return newRat()
	}
	sum := newRat()
	for i := 0; i < n; i++ {
		a := points[i]
		b := points[(i+1)%n]
		term := ratMul(a.X, b.Y)
		term.Sub(term, ratMul(a.Y, b.X))
		sum.Add(sum, term)
	}
	return ratHalf(sum)
}

func windingPoints(points []Point, p Point) int {
	n := len(points)
	if n < 2 {
		return 0
	}
	w := 0
	for i := 0; i < n; i++ {
		a := points[i]
		b := points[(i+1)%n]
		if a.Equal(b) {
			continue
		}
		if a.Y.Cmp(p.Y) <= 0 {
			if b.Y.Cmp(p.Y) > 0 && orient(a, b, p) > 0 {
				w++
			}
		} else if b.Y.Cmp(p.Y) <= 0 && orient(a, b, p) < 0 {
			w--
		}
	}
	return w
}

// orient returns +1 if p is left of a->b, -1 if right, and 0 if collinear.
func orient(a, b, p Point) int {
	dx := ratSub(b.X, a.X)
	dy := ratSub(b.Y, a.Y)
	px := ratSub(p.X, a.X)
	py := ratSub(p.Y, a.Y)
	return ratSign(crossRat(dx, dy, px, py))
}

func pointOnSegment(a, b, p Point) bool {
	if orient(a, b, p) != 0 {
		return false
	}
	if p.X.Cmp(minRat(a.X, b.X)) < 0 || p.X.Cmp(maxRat(a.X, b.X)) > 0 {
		return false
	}
	if p.Y.Cmp(minRat(a.Y, b.Y)) < 0 || p.Y.Cmp(maxRat(a.Y, b.Y)) > 0 {
		return false
	}
	return true
}

func pointOnPoints(points []Point, p Point) bool {
	n := len(points)
	if n < 2 {
		return false
	}
	for i := 0; i < n; i++ {
		if pointOnSegment(points[i], points[(i+1)%n], p) {
			return true
		}
	}
	return false
}

func regionBoundaryContains(r Region, p Point) bool {
	for _, ring := range r.Rings {
		if pointOnPoints(ring.Points, p) {
			return true
		}
	}
	return false
}

func contourBoundaryContains(c Contour, p Point) bool {
	return pointOnPoints(c.Points, p)
}

func resultBoundaryContains(r Result, p Point) bool {
	for _, c := range r.Contours() {
		if pointOnPoints(c.Points, p) {
			return true
		}
	}
	return false
}

func minRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}

func maxRat(a, b *big.Rat) *big.Rat {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}
