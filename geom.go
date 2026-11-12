package taskpkg

import (
	"errors"
	"fmt"
	"math"
	"math/big"
)

// Vec3 is a point or vector in three-dimensional space.
type Vec3 struct {
	X, Y, Z float64
}

// Add returns a+b.
func (a Vec3) Add(b Vec3) Vec3 { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }

// Sub returns a-b.
func (a Vec3) Sub(b Vec3) Vec3 { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }

// Scale returns s*a.
func (a Vec3) Scale(s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }

// Dot returns the dot product of a and b.
func (a Vec3) Dot(b Vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

// Cross returns the cross product of a and b.
func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{
		a.Y*b.Z - a.Z*b.Y,
		a.Z*b.X - a.X*b.Z,
		a.X*b.Y - a.Y*b.X,
	}
}

// Length returns the Euclidean length of a.
func (a Vec3) Length() float64 { return math.Sqrt(a.Dot(a)) }

// Unit returns a unit vector in the direction of a.
func (a Vec3) Unit() (Vec3, bool) {
	n := a.Length()
	if n == 0 {
		return Vec3{}, false
	}
	return a.Scale(1 / n), true
}

func (a Vec3) finite() bool {
	return !math.IsNaN(a.X) && !math.IsInf(a.X, 0) &&
		!math.IsNaN(a.Y) && !math.IsInf(a.Y, 0) &&
		!math.IsNaN(a.Z) && !math.IsInf(a.Z, 0)
}

func (a Vec3) almostEqual(b Vec3, eps float64) bool {
	return math.Abs(a.X-b.X) <= eps &&
		math.Abs(a.Y-b.Y) <= eps &&
		math.Abs(a.Z-b.Z) <= eps
}

// Triangle refers to three vertices of a Mesh. Its winding is counter-clockwise
// when viewed from the outer side of the surface.
type Triangle [3]int

// Tri is a small convenience constructor for Triangle.
func Tri(a, b, c int) Triangle { return Triangle{a, b, c} }

func (t Triangle) edges() [3][2]int {
	return [3][2]int{{t[0], t[1]}, {t[1], t[2]}, {t[2], t[0]}}
}

// Edge is an undirected mesh edge. A is always smaller than B.
type Edge struct {
	A, B int
}

func edgeOf(a, b int) Edge {
	if a > b {
		a, b = b, a
	}
	return Edge{a, b}
}

func (e Edge) contains(v int) bool { return e.A == v || e.B == v }

// Plane is the rational plane A*x+B*y+C*z+D = 0. The coefficients are
// interpreted as the exact rational numbers represented by their float64
// values; all orientation predicates therefore remain exact.
type Plane struct {
	A, B, C, D float64
}

// NewPlane validates and returns a plane.
func NewPlane(a, b, c, d float64) (Plane, error) {
	p := Plane{A: a, B: b, C: c, D: d}
	if err := p.validate(); err != nil {
		return Plane{}, err
	}
	return p, nil
}

// PlaneThrough returns the plane with the supplied normal that contains q.
func PlaneThrough(normal Vec3, q Vec3) (Plane, error) {
	if !normal.finite() || !q.finite() {
		return Plane{}, errors.New("taskpkg: plane normal and point must be finite")
	}
	if normal.Dot(normal) == 0 {
		return Plane{}, ErrDegeneratePlane
	}
	return Plane{
		A: normal.X,
		B: normal.Y,
		C: normal.Z,
		D: -normal.Dot(q),
	}, nil
}

// PlaneFromNormalOffset returns a plane with the given (not necessarily unit)
// normal. The offset is measured along the normalized normal.
func PlaneFromNormalOffset(normal Vec3, offset float64) (Plane, error) {
	n, ok := normal.Unit()
	if !ok || !normal.finite() || math.IsNaN(offset) || math.IsInf(offset, 0) {
		return Plane{}, ErrDegeneratePlane
	}
	return Plane{A: n.X, B: n.Y, C: n.Z, D: -offset}, nil
}

// Normal returns the (not necessarily unit) plane normal.
func (p Plane) Normal() Vec3 { return Vec3{p.A, p.B, p.C} }

// Eval evaluates the left-hand side A*x+B*y+C*z+D at v.
func (p Plane) Eval(v Vec3) float64 {
	return p.A*v.X + p.B*v.Y + p.C*v.Z + p.D
}

// UnitNormal returns the unit normal pointing into the retained half-space.
func (p Plane) UnitNormal() (Vec3, bool) {
	return p.Normal().Unit()
}

func (p Plane) validate() error {
	if !p.Normal().finite() || math.IsNaN(p.D) || math.IsInf(p.D, 0) {
		return errors.New("taskpkg: plane coefficients must be finite")
	}
	if p.Normal().Dot(p.Normal()) == 0 {
		return ErrDegeneratePlane
	}
	return nil
}

// ExactPlane is a plane with explicitly rational coefficients.
type ExactPlane struct {
	A, B, C, D *big.Rat
}

// NewExactPlane validates and returns an explicitly rational plane.
func NewExactPlane(a, b, c, d *big.Rat) (*ExactPlane, error) {
	if a == nil || b == nil || c == nil || d == nil {
		return nil, errors.New("taskpkg: nil rational plane coefficient")
	}
	p := &ExactPlane{a, b, c, d}
	aa := new(big.Rat).Mul(a, a)
	bb := new(big.Rat).Mul(b, b)
	cc := new(big.Rat).Mul(c, c)
	aa.Add(aa, bb)
	aa.Add(aa, cc)
	if aa.Sign() == 0 {
		return nil, ErrDegeneratePlane
	}
	return p, nil
}

// Plane converts the exact plane to the float64 representation used by Cut.
// Predicates in Cut remain exact for the converted coefficients.
func (p *ExactPlane) Plane() Plane {
	return Plane{
		A: ratToFloat(p.A),
		B: ratToFloat(p.B),
		C: ratToFloat(p.C),
		D: ratToFloat(p.D),
	}
}

// ErrDegeneratePlane is returned for a plane whose normal is zero.
var ErrDegeneratePlane = errors.New("taskpkg: plane normal is zero")

// Mesh is an oriented triangle mesh.
type Mesh struct {
	Vertices  []Vec3
	Triangles []Triangle
}

// ErrNonManifold is wrapped by validation errors caused by non-manifold or
// non-closed input.
var ErrNonManifold = errors.New("taskpkg: non-manifold or non-closed mesh")

// Validate checks that m is a finite, consistently oriented, closed triangular
// 2-manifold. Every undirected edge must occur exactly twice with opposite
// directions, and the link of every used vertex must be one cycle.
func (m Mesh) Validate() error { return ValidateMesh(m) }

// ValidateMesh checks that m is a finite, consistently oriented, closed
// triangular 2-manifold.
func ValidateMesh(m Mesh) error {
	if len(m.Vertices) == 0 {
		return fmt.Errorf("%w: mesh has no vertices", ErrNonManifold)
	}
	if len(m.Triangles) == 0 {
		return fmt.Errorf("%w: mesh has no triangles", ErrNonManifold)
	}
	for i, v := range m.Vertices {
		if !v.finite() {
			return fmt.Errorf("taskpkg: vertex %d is not finite", i)
		}
	}

	type usage struct {
		plus  int
		minus int
	}
	edges := make(map[Edge]usage, len(m.Triangles)*3/2)
	for ti, t := range m.Triangles {
		for _, v := range t {
			if v < 0 || v >= len(m.Vertices) {
				return fmt.Errorf("%w: triangle %d has out-of-range vertex %d", ErrNonManifold, ti, v)
			}
		}
		if t[0] == t[1] || t[1] == t[2] || t[2] == t[0] {
			return fmt.Errorf("%w: triangle %d repeats a vertex", ErrNonManifold, ti)
		}
		a, b, c := m.Vertices[t[0]], m.Vertices[t[1]], m.Vertices[t[2]]
		if a.almostEqual(b, 0) || b.almostEqual(c, 0) || c.almostEqual(a, 0) {
			return fmt.Errorf("%w: triangle %d has coincident vertices", ErrNonManifold, ti)
		}
		if a.Sub(b).Cross(c.Sub(b)).Length() == 0 {
			return fmt.Errorf("%w: triangle %d is degenerate", ErrNonManifold, ti)
		}
		for _, e := range t.edges() {
			key := edgeOf(e[0], e[1])
			u := edges[key]
			if e[0] < e[1] {
				u.plus++
			} else {
				u.minus++
			}
			edges[key] = u
		}
	}
	for e, u := range edges {
		if u.plus+u.minus != 2 || u.plus != 1 || u.minus != 1 {
			return fmt.Errorf("%w: edge (%d,%d) has %d forward and %d reverse uses",
				ErrNonManifold, e.A, e.B, u.plus, u.minus)
		}
	}

	// A vertex link that is disconnected or not a single cycle indicates
	// non-manifold geometry even when every edge has two incident faces.
	incident := make(map[int]int)
	linkEdges := make(map[int]map[Edge]int)
	for _, t := range m.Triangles {
		for i, v := range t {
			prev := t[(i+2)%3]
			next := t[(i+1)%3]
			incident[v]++
			if linkEdges[v] == nil {
				linkEdges[v] = make(map[Edge]int)
			}
			linkEdges[v][edgeOf(prev, next)]++
		}
	}
	for v, count := range incident {
		if count == 0 {
			continue
		}
		links := linkEdges[v]
		degree := make(map[int]int)
		adj := make(map[int][]int)
		for le, n := range links {
			if n != 1 {
				return fmt.Errorf("%w: vertex %d has a duplicated link edge", ErrNonManifold, v)
			}
			degree[le.A]++
			degree[le.B]++
			adj[le.A] = append(adj[le.A], le.B)
			adj[le.B] = append(adj[le.B], le.A)
		}
		if len(links) != count {
			return fmt.Errorf("%w: vertex %d has an inconsistent link", ErrNonManifold, v)
		}
		start := -1
		for n, d := range degree {
			if d != 2 {
				return fmt.Errorf("%w: vertex %d has link degree %d at neighbor %d",
					ErrNonManifold, v, d, n)
			}
			if start < 0 || n < start {
				start = n
			}
		}
		if start < 0 {
			return fmt.Errorf("%w: vertex %d has an empty link", ErrNonManifold, v)
		}
		seen := make(map[int]bool, len(degree))
		queue := []int{start}
		seen[start] = true
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			for _, w := range adj[n] {
				if !seen[w] {
					seen[w] = true
					queue = append(queue, w)
				}
			}
		}
		if len(seen) != len(degree) {
			return fmt.Errorf("%w: vertex %d has a disconnected link", ErrNonManifold, v)
		}
	}
	return nil
}

// SignedVolume returns the signed enclosed volume of a closed mesh. A positive
// value corresponds to outward-facing triangle winding.
func SignedVolume(m Mesh) (float64, error) {
	if err := m.Validate(); err != nil {
		return 0, err
	}
	var volume float64
	for _, t := range m.Triangles {
		a := m.Vertices[t[0]]
		b := m.Vertices[t[1]]
		c := m.Vertices[t[2]]
		volume += a.Dot(b.Cross(c)) / 6
	}
	return volume, nil
}

// Volume is an alias for SignedVolume.
func Volume(m Mesh) (float64, error) { return SignedVolume(m) }

// Volume returns the signed enclosed volume of m.
func (m Mesh) Volume() (float64, error) { return SignedVolume(m) }

// ---------------------------------------------------------------------------
// Exact internal arithmetic.

type ratVec struct {
	x, y, z *big.Rat
}

type ratPlane struct {
	a, b, c, d *big.Rat
}

func ratFromFloat(f float64) *big.Rat {
	r := new(big.Rat)
	r.SetFloat64(f)
	return r
}

func ratVecFrom(v Vec3) ratVec {
	return ratVec{ratFromFloat(v.X), ratFromFloat(v.Y), ratFromFloat(v.Z)}
}

func ratPlaneFrom(p Plane) ratPlane {
	return ratPlane{
		ratFromFloat(p.A),
		ratFromFloat(p.B),
		ratFromFloat(p.C),
		ratFromFloat(p.D),
	}
}

func ratAdd(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }
func ratSub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }
func ratMul(a, b *big.Rat) *big.Rat { return new(big.Rat).Mul(a, b) }

func (p ratPlane) eval(v ratVec) *big.Rat {
	s := ratMul(v.x, p.a)
	s.Add(s, ratMul(v.y, p.b))
	s.Add(s, ratMul(v.z, p.c))
	s.Add(s, p.d)
	return s
}

func (a ratVec) sub(b ratVec) ratVec {
	return ratVec{ratSub(a.x, b.x), ratSub(a.y, b.y), ratSub(a.z, b.z)}
}

func ratCross(a, b ratVec) ratVec {
	return ratVec{
		ratSub(ratMul(a.y, b.z), ratMul(a.z, b.y)),
		ratSub(ratMul(a.z, b.x), ratMul(a.x, b.z)),
		ratSub(ratMul(a.x, b.y), ratMul(a.y, b.x)),
	}
}

func ratDot(a, b ratVec) *big.Rat {
	s := ratMul(a.x, b.x)
	s.Add(s, ratMul(a.y, b.y))
	s.Add(s, ratMul(a.z, b.z))
	return s
}

func ratToFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

func vecFromRat(v ratVec) Vec3 {
	return Vec3{ratToFloat(v.x), ratToFloat(v.y), ratToFloat(v.z)}
}
