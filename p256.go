package threshold

import (
	"crypto/elliptic"
	"io"
	"math/big"
)

// P256Group returns the real NIST P-256/secp256r1 group. It is safe for
// concurrent use because it has no mutable protocol state.
func P256Group() Group {
	return p256Group{curve: elliptic.P256()}
}

type p256Group struct {
	curve elliptic.Curve
}

type p256Point struct {
	x, y *big.Int
}

func (g p256Group) Name() string { return "P-256" }

func (g p256Group) Order() *big.Int {
	return new(big.Int).Set(g.curve.Params().N)
}

func (g p256Group) Generator() Point {
	params := g.curve.Params()
	return p256Point{x: new(big.Int).Set(params.Gx), y: new(big.Int).Set(params.Gy)}
}

func (g p256Group) normalize(s *big.Int) *big.Int {
	if s == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Mod(new(big.Int).Set(s), g.curve.Params().N)
}

func (g p256Group) ScalarBaseMult(s *big.Int) Point {
	s = g.normalize(s)
	if s.Sign() == 0 {
		return p256Point{}
	}
	x, y := g.curve.ScalarBaseMult(s.Bytes())
	return p256Point{x: x, y: y}
}

func (g p256Group) ScalarMult(p Point, s *big.Int) Point {
	pp, ok := p.(p256Point)
	if !ok || pp.x == nil || pp.y == nil {
		return p256Point{}
	}
	s = g.normalize(s)
	if s.Sign() == 0 {
		return p256Point{}
	}
	x, y := g.curve.ScalarMult(pp.x, pp.y, s.Bytes())
	return p256Point{x: x, y: y}
}

func (g p256Group) Add(a, b Point) Point {
	pa, oka := a.(p256Point)
	pb, okb := b.(p256Point)
	if !oka || pa.x == nil || pa.y == nil {
		if !okb {
			return p256Point{}
		}
		return pb
	}
	if !okb || pb.x == nil || pb.y == nil {
		return pa
	}
	x, y := g.curve.Add(pa.x, pa.y, pb.x, pb.y)
	return p256Point{x: x, y: y}
}

func (g p256Group) Neg(a Point) Point {
	p, ok := a.(p256Point)
	if !ok || p.x == nil || p.y == nil {
		return p256Point{}
	}
	prime := g.curve.Params().P
	return p256Point{x: new(big.Int).Set(p.x), y: new(big.Int).Sub(prime, p.y)}
}

func (g p256Group) Equal(a, b Point) bool {
	pa, oka := a.(p256Point)
	pb, okb := b.(p256Point)
	if !oka || !okb {
		return false
	}
	aInf := pa.x == nil || pa.y == nil
	bInf := pb.x == nil || pb.y == nil
	if aInf || bInf {
		return aInf && bInf
	}
	return pa.x.Cmp(pb.x) == 0 && pa.y.Cmp(pb.y) == 0
}

func (g p256Group) Encode(p Point) []byte {
	pp, ok := p.(p256Point)
	if !ok || pp.x == nil || pp.y == nil {
		return []byte{0x00}
	}
	return elliptic.Marshal(g.curve, pp.x, pp.y)
}

func (g p256Group) Decode(b []byte) (Point, error) {
	if len(b) == 1 && b[0] == 0x00 {
		return p256Point{}, nil
	}
	x, y := elliptic.Unmarshal(g.curve, b)
	if x == nil || y == nil {
		return nil, ErrInvalidCommitment
	}
	return p256Point{x: x, y: y}, nil
}

func (g p256Group) RandomScalar(r io.Reader) (*big.Int, error) {
	return randomNonzeroScalar(g.curve.Params().N, r)
}

func (g p256Group) HashToScalar(domain string, chunks ...[]byte) *big.Int {
	return hashToScalarModN(g.curve.Params().N, domain, chunks...)
}
