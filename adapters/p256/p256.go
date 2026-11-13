// Package p256 is a test adapter implementing the netsec.Group contract
// over the NIST P-256 curve (prime order, cofactor 1). It exists for
// conformance testing of the proof system; using it does not replace a
// production review of group parameters and encodings.
package p256

import (
	"crypto/elliptic"
	"errors"
	"math/big"

	netsec "example.org/netsec/q0044"
)

const (
	scalarSize = 32
	pointSize  = 33 // compressed SEC-1, with all-zero reserved for identity
)

var (
	curve = elliptic.P256()
	order = curve.Params().N
	field = curve.Params().P
)

// Group is the P-256 group adapter.
type Group struct{}

// New returns the P-256 test adapter.
func New() Group { return Group{} }

func (Group) Order() *big.Int { return new(big.Int).Set(order) }
func (Group) ScalarSize() int { return scalarSize }
func (Group) PointSize() int  { return pointSize }
func (Group) Identity() netsec.Point {
	return Point{}
}

// Base returns the standard P-256 base point.
func (Group) Base() netsec.Point {
	return Point{x: new(big.Int).Set(curve.Params().Gx), y: new(big.Int).Set(curve.Params().Gy)}
}

func (g Group) ScalarFromBytes(b []byte) (netsec.Scalar, error) {
	if len(b) != scalarSize {
		return nil, errors.New("p256: bad scalar length")
	}
	i := new(big.Int).SetBytes(b)
	if i.Cmp(order) >= 0 {
		return nil, errors.New("p256: non-canonical scalar")
	}
	return Scalar{i: i}, nil
}

func (g Group) ScalarFromBig(i *big.Int) netsec.Scalar {
	return Scalar{i: new(big.Int).Mod(new(big.Int).Set(i), order)}
}

func (g Group) PointFromBytes(b []byte) (netsec.Point, error) {
	if len(b) != pointSize {
		return nil, errors.New("p256: bad point length")
	}
	zero := true
	for _, c := range b {
		if c != 0 {
			zero = false
			break
		}
	}
	if zero {
		return Point{}, nil
	}
	x, y := elliptic.UnmarshalCompressed(curve, b)
	if x == nil {
		return nil, errors.New("p256: non-canonical or invalid point")
	}
	return Point{x: x, y: y}, nil
}

// Scalar is a P-256 scalar field element.
type Scalar struct{ i *big.Int }

func coerceScalar(s netsec.Scalar) Scalar {
	v, ok := s.(Scalar)
	if !ok {
		panic("p256: foreign scalar type")
	}
	return v
}

func (s Scalar) Add(o netsec.Scalar) netsec.Scalar {
	return Scalar{i: new(big.Int).Mod(new(big.Int).Add(s.i, coerceScalar(o).i), order)}
}

func (s Scalar) Sub(o netsec.Scalar) netsec.Scalar {
	return Scalar{i: new(big.Int).Mod(new(big.Int).Sub(s.i, coerceScalar(o).i), order)}
}

func (s Scalar) Mul(o netsec.Scalar) netsec.Scalar {
	return Scalar{i: new(big.Int).Mod(new(big.Int).Mul(s.i, coerceScalar(o).i), order)}
}

func (s Scalar) Neg() netsec.Scalar {
	return Scalar{i: new(big.Int).Mod(new(big.Int).Neg(s.i), order)}
}

func (s Scalar) Inv() netsec.Scalar {
	return Scalar{i: new(big.Int).ModInverse(s.i, order)}
}

func (s Scalar) IsZero() bool { return s.i.Sign() == 0 }

func (s Scalar) Equal(o netsec.Scalar) bool { return s.i.Cmp(coerceScalar(o).i) == 0 }

func (s Scalar) Bytes() []byte {
	out := make([]byte, scalarSize)
	s.i.FillBytes(out)
	return out
}

// Point is a P-256 curve point; the zero value is the identity.
type Point struct {
	x, y *big.Int // nil x denotes the identity element
}

func coercePoint(p netsec.Point) Point {
	v, ok := p.(Point)
	if !ok {
		panic("p256: foreign point type")
	}
	return v
}

func (p Point) IsIdentity() bool { return p.x == nil }

func (p Point) Add(o netsec.Point) netsec.Point {
	q := coercePoint(o)
	if p.IsIdentity() {
		return q
	}
	if q.IsIdentity() {
		return p
	}
	x, y := curve.Add(p.x, p.y, q.x, q.y)
	if x == nil {
		return Point{}
	}
	return Point{x: x, y: y}
}

func (p Point) Neg() netsec.Point {
	if p.IsIdentity() {
		return p
	}
	return Point{x: new(big.Int).Set(p.x), y: new(big.Int).Mod(new(big.Int).Neg(p.y), field)}
}

func (p Point) ScalarMul(s netsec.Scalar) netsec.Point {
	sc := coerceScalar(s)
	if p.IsIdentity() || sc.IsZero() {
		return Point{}
	}
	x, y := curve.ScalarMult(p.x, p.y, sc.Bytes())
	if x == nil {
		return Point{}
	}
	return Point{x: x, y: y}
}

func (p Point) Equal(o netsec.Point) bool {
	q := coercePoint(o)
	if p.IsIdentity() || q.IsIdentity() {
		return p.IsIdentity() && q.IsIdentity()
	}
	return p.x.Cmp(q.x) == 0 && p.y.Cmp(q.y) == 0
}

func (p Point) Bytes() []byte {
	if p.IsIdentity() {
		return make([]byte, pointSize)
	}
	return elliptic.MarshalCompressed(curve, p.x, p.y)
}
