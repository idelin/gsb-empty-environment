// Package smallprime is a small-order algebraic reference adapter: the
// group is the additive group of Z/pZ for a small prime p, with scalars
// in Z/pZ and "scalar multiplication" being modular multiplication. It
// offers no cryptographic hardness whatsoever; it exists to cross-check
// the proof algebra against an independent, trivially inspectable group.
package smallprime

import (
	"encoding/binary"
	"errors"
	"math/big"

	netsec "example.org/netsec/q0044"
)

const (
	// P is the group order (and scalar field modulus). It is the largest
	// prime below 2^16, so 2-byte encodings in [P, 2^16) are non-canonical.
	P          = 65521
	scalarSize = 2
	pointSize  = 2
)

var order = big.NewInt(P)

// Group is the small-prime additive-group adapter.
type Group struct{}

// New returns the small-prime reference adapter.
func New() Group { return Group{} }

func (Group) Order() *big.Int { return new(big.Int).Set(order) }
func (Group) ScalarSize() int { return scalarSize }
func (Group) PointSize() int  { return pointSize }

func (Group) Identity() netsec.Point {
	return Point(0)
}

func (g Group) ScalarFromBytes(b []byte) (netsec.Scalar, error) {
	if len(b) != scalarSize {
		return nil, errors.New("smallprime: bad scalar length")
	}
	v := binary.BigEndian.Uint16(b)
	if uint64(v) >= P {
		return nil, errors.New("smallprime: non-canonical scalar")
	}
	return Scalar(v), nil
}

func (g Group) ScalarFromBig(i *big.Int) netsec.Scalar {
	v := new(big.Int).Mod(new(big.Int).Set(i), order)
	return Scalar(v.Uint64())
}

func (g Group) PointFromBytes(b []byte) (netsec.Point, error) {
	if len(b) != pointSize {
		return nil, errors.New("smallprime: bad point length")
	}
	v := binary.BigEndian.Uint16(b)
	if uint64(v) >= P {
		return nil, errors.New("smallprime: non-canonical point")
	}
	return Point(v), nil
}

// PointFromUint64 reduces v mod p and returns it as a group element. It is
// a convenience for deriving deterministic test generators.
func (Group) PointFromUint64(v uint64) netsec.Point {
	return Point(v % P)
}

// Scalar is an element of Z/pZ.
type Scalar uint64

func coerceScalar(s netsec.Scalar) Scalar {
	v, ok := s.(Scalar)
	if !ok {
		panic("smallprime: foreign scalar type")
	}
	return v
}

func (s Scalar) Add(o netsec.Scalar) netsec.Scalar {
	return Scalar((uint64(s) + uint64(coerceScalar(o))) % P)
}

func (s Scalar) Sub(o netsec.Scalar) netsec.Scalar {
	return Scalar((uint64(s) + P - uint64(coerceScalar(o))) % P)
}

func (s Scalar) Mul(o netsec.Scalar) netsec.Scalar {
	return Scalar((uint64(s) * uint64(coerceScalar(o))) % P)
}

func (s Scalar) Neg() netsec.Scalar {
	return Scalar((P - uint64(s)) % P)
}

func (s Scalar) Inv() netsec.Scalar {
	v := new(big.Int).ModInverse(big.NewInt(int64(s)), order)
	if v == nil {
		panic("smallprime: inverse of zero")
	}
	return Scalar(v.Uint64())
}

func (s Scalar) IsZero() bool { return s == 0 }

func (s Scalar) Equal(o netsec.Scalar) bool { return s == coerceScalar(o) }

func (s Scalar) Bytes() []byte {
	out := make([]byte, scalarSize)
	binary.BigEndian.PutUint16(out, uint16(s))
	return out
}

// Point is an element of the additive group of Z/pZ; zero is the identity.
type Point uint64

func coercePoint(p netsec.Point) Point {
	v, ok := p.(Point)
	if !ok {
		panic("smallprime: foreign point type")
	}
	return v
}

func (p Point) Add(o netsec.Point) netsec.Point {
	return Point((uint64(p) + uint64(coercePoint(o))) % P)
}

func (p Point) Neg() netsec.Point {
	return Point((P - uint64(p)) % P)
}

func (p Point) ScalarMul(s netsec.Scalar) netsec.Point {
	return Point((uint64(p) * uint64(coerceScalar(s))) % P)
}

func (p Point) IsIdentity() bool { return p == 0 }

func (p Point) Equal(o netsec.Point) bool { return p == coercePoint(o) }

func (p Point) Bytes() []byte {
	out := make([]byte, pointSize)
	binary.BigEndian.PutUint16(out, uint16(p))
	return out
}
