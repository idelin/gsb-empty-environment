package taskpkg

import (
	"errors"
	"math/big"
)

// Rat is the exact rational scalar used by every geometric predicate in this
// package. No floating point arithmetic is ever used to decide orientation,
// containment or intersection; inputs are converted once into rationals.
type Rat = big.Rat

// Point3 is an exact rational point in three-dimensional space.
type Point3 struct {
	X, Y, Z *Rat
}

// Plane is the explicit far plane given by n·(x-p) == 0 with n != 0.
// Shadow geometry is extruded along light rays until it meets this plane.
type Plane struct {
	Normal [3]*Rat
	Point  Point3
}

// Triangle indexes three vertices of a mesh.
type Triangle [3]int

// RationalMesh is an exact, oriented triangle mesh.
type RationalMesh struct {
	Vertices []Point3
	Faces    []Triangle
}

// FloatMesh is the ordinary floating-point representation exchanged with the
// offscreen rendering host.
type FloatMesh struct {
	Positions [][3]float64
	Faces     []Triangle
}

// NewRat builds a *big.Rat from an integer numerator/denominator.
func NewRat(num, den int64) *Rat { return big.NewRat(num, den) }

// RatFromInt returns a rational equal to n.
func RatFromInt(n int64) *Rat { return big.NewRat(n, 1) }

// RatFromFloat64 converts a finite float64 exactly into a rational by its
// shortest-round-trip decimal representation. It panics on NaN/Inf because
// such values cannot name a geometric point.
func RatFromFloat64(f float64) *Rat {
	r, ok := new(big.Rat).SetString(big.NewFloat(f).Text('f', -1))
	if !ok {
		panic("taskpkg: non-finite float cannot become a rational")
	}
	return r
}

// Point3FromInts builds an exact point from integer coordinates.
func Point3FromInts(x, y, z int64) Point3 {
	return Point3{X: RatFromInt(x), Y: RatFromInt(y), Z: RatFromInt(z)}
}

// Point3FromFloat64 builds an exact point from finite float64 coordinates.
func Point3FromFloat64(x, y, z float64) Point3 {
	return Point3{X: RatFromFloat64(x), Y: RatFromFloat64(y), Z: RatFromFloat64(z)}
}

// PlaneFromInts builds a far plane from an integer point and normal.
func PlaneFromInts(px, py, pz, nx, ny, nz int64) (Plane, error) {
	pl := Plane{
		Normal: [3]*Rat{RatFromInt(nx), RatFromInt(ny), RatFromInt(nz)},
		Point:  Point3FromInts(px, py, pz),
	}
	if pl.Normal[0].Sign() == 0 && pl.Normal[1].Sign() == 0 && pl.Normal[2].Sign() == 0 {
		return Plane{}, errors.New("taskpkg: plane normal must be non-zero")
	}
	return pl, nil
}

// PlaneFromFloat64 builds a far plane from finite float64 point and normal.
func PlaneFromFloat64(px, py, pz, nx, ny, nz float64) (Plane, error) {
	pl := Plane{
		Normal: [3]*Rat{RatFromFloat64(nx), RatFromFloat64(ny), RatFromFloat64(nz)},
		Point:  Point3FromFloat64(px, py, pz),
	}
	if pl.Normal[0].Sign() == 0 && pl.Normal[1].Sign() == 0 && pl.Normal[2].Sign() == 0 {
		return Plane{}, errors.New("taskpkg: plane normal must be non-zero")
	}
	return pl, nil
}

// ToFloat64 renders an exact mesh into the host-facing float64 representation.
func (m *RationalMesh) ToFloat64() FloatMesh {
	out := FloatMesh{Faces: append([]Triangle(nil), m.Faces...)}
	out.Positions = make([][3]float64, len(m.Vertices))
	for i, v := range m.Vertices {
		fx, _ := v.X.Float64()
		fy, _ := v.Y.Float64()
		fz, _ := v.Z.Float64()
		out.Positions[i] = [3]float64{fx, fy, fz}
	}
	return out
}

func ratCopy(r *Rat) *Rat { return new(Rat).Set(r) }

func pointCopy(p Point3) Point3 {
	return Point3{X: ratCopy(p.X), Y: ratCopy(p.Y), Z: ratCopy(p.Z)}
}

func sub(a, b Point3) [3]*Rat {
	return [3]*Rat{
		new(Rat).Sub(a.X, b.X),
		new(Rat).Sub(a.Y, b.Y),
		new(Rat).Sub(a.Z, b.Z),
	}
}

func add(a Point3, d [3]*Rat, t *Rat) Point3 {
	return Point3{
		X: new(Rat).Add(a.X, new(Rat).Mul(t, d[0])),
		Y: new(Rat).Add(a.Y, new(Rat).Mul(t, d[1])),
		Z: new(Rat).Add(a.Z, new(Rat).Mul(t, d[2])),
	}
}

func cross(u, v [3]*Rat) [3]*Rat {
	return [3]*Rat{
		new(Rat).Sub(new(Rat).Mul(u[1], v[2]), new(Rat).Mul(u[2], v[1])),
		new(Rat).Sub(new(Rat).Mul(u[2], v[0]), new(Rat).Mul(u[0], v[2])),
		new(Rat).Sub(new(Rat).Mul(u[0], v[1]), new(Rat).Mul(u[1], v[0])),
	}
}

func dot(u, v [3]*Rat) *Rat {
	r := new(Rat).Mul(u[0], v[0])
	r.Add(r, new(Rat).Mul(u[1], v[1]))
	r.Add(r, new(Rat).Mul(u[2], v[2]))
	return r
}

func scaleRat(v [3]*Rat, s *Rat) [3]*Rat {
	return [3]*Rat{
		new(Rat).Mul(v[0], s),
		new(Rat).Mul(v[1], s),
		new(Rat).Mul(v[2], s),
	}
}

// det3 returns the determinant of the matrix whose columns (or rows; the
// determinant is invariant under transpose) are u, v, w.
func det3(u, v, w [3]*Rat) *Rat { return dot(cross(u, v), w) }
