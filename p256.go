package ipp

import (
	"bytes"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
)

// P256Adapter is a test/host adapter for NIST P-256. It does not audit or
// replace review of production group parameters and generator derivation.
type P256Adapter struct {
	curve    elliptic.Curve
	q        *big.Int
	identity Point
}

func NewP256Adapter() *P256Adapter {
	curve := elliptic.P256()
	return &P256Adapter{
		curve:    curve,
		q:        new(big.Int).Set(curve.Params().N),
		identity: make(Point, 33),
	}
}

func (a *P256Adapter) Order() *big.Int {
	return new(big.Int).Set(a.q)
}

func (a *P256Adapter) Identity() Point {
	return clonePoint(a.identity)
}

func (a *P256Adapter) isIdentity(p Point) bool {
	return bytes.Equal(p, a.identity)
}

func (a *P256Adapter) ParsePoint(raw []byte) (Point, error) {
	if len(raw) == 33 && raw[0] == 0x00 {
		for _, b := range raw[1:] {
			if b != 0 {
				return nil, fmt.Errorf("%w: 0x00 prefix is reserved for identity", ErrNonCanonicalPoint)
			}
		}
		return clonePoint(a.identity), nil
	}
	if len(raw) != 33 {
		return nil, fmt.Errorf("%w: expected 33-byte compressed point, got %d", ErrNonCanonicalPoint, len(raw))
	}
	if raw[0] != 0x02 && raw[0] != 0x03 {
		return nil, fmt.Errorf("%w: bad compressed point prefix 0x%02x", ErrNonCanonicalPoint, raw[0])
	}
	x, y := elliptic.UnmarshalCompressed(a.curve, raw)
	if x == nil || y == nil || !a.curve.IsOnCurve(x, y) {
		return nil, fmt.Errorf("%w: point is not on P-256", ErrInvalidPoint)
	}
	canonical := elliptic.MarshalCompressed(a.curve, x, y)
	if !bytes.Equal(canonical, raw) {
		return nil, fmt.Errorf("%w: encoding is not canonical", ErrNonCanonicalPoint)
	}
	return clonePoint(raw), nil
}

func (a *P256Adapter) decode(p Point) (*big.Int, *big.Int, error) {
	if a.isIdentity(p) {
		return big.NewInt(0), big.NewInt(0), nil
	}
	x, y := elliptic.UnmarshalCompressed(a.curve, p)
	if x == nil || y == nil || !a.curve.IsOnCurve(x, y) {
		return nil, nil, fmt.Errorf("%w: point is not on P-256", ErrInvalidPoint)
	}
	return x, y, nil
}

func (a *P256Adapter) encodeXY(x, y *big.Int) Point {
	if x == nil || y == nil || x.Sign() == 0 && y.Sign() == 0 {
		return a.Identity()
	}
	return Point(elliptic.MarshalCompressed(a.curve, x, y))
}

func (a *P256Adapter) AddPoints(first, second Point) (Point, error) {
	if a.isIdentity(first) {
		return clonePoint(second), nil
	}
	if a.isIdentity(second) {
		return clonePoint(first), nil
	}
	x1, y1, err := a.decode(first)
	if err != nil {
		return nil, err
	}
	x2, y2, err := a.decode(second)
	if err != nil {
		return nil, err
	}
	x, y := a.curve.Add(x1, y1, x2, y2)
	return a.encodeXY(x, y), nil
}

func (a *P256Adapter) MultiplyPoint(scalar *big.Int, p Point) (Point, error) {
	if err := checkScalar(scalar, a.q); err != nil {
		return nil, err
	}
	if scalar.Sign() == 0 || a.isIdentity(p) {
		return a.Identity(), nil
	}
	x, y, err := a.decode(p)
	if err != nil {
		return nil, err
	}
	var scalarBytes [32]byte
	scalar.FillBytes(scalarBytes[:])
	resultX, resultY := a.curve.ScalarMult(x, y, scalarBytes[:])
	return a.encodeXY(resultX, resultY), nil
}

func (a *P256Adapter) ScalarBaseMult(scalar *big.Int) (Point, error) {
	if err := checkScalar(scalar, a.q); err != nil {
		return nil, err
	}
	if scalar.Sign() == 0 {
		return a.Identity(), nil
	}
	var scalarBytes [32]byte
	scalar.FillBytes(scalarBytes[:])
	x, y := a.curve.ScalarBaseMult(scalarBytes[:])
	return a.encodeXY(x, y), nil
}

// DeterministicP256TestGenerators derives known-discrete-log generators and is
// suitable only for tests. Production deployments need an independent,
// reviewed nothing-up-my-sleeve generator ceremony.
func DeterministicP256TestGenerators(n int, seed []byte) (Generators, error) {
	ad := NewP256Adapter()
	if !ValidN(n) {
		return Generators{}, fmt.Errorf("%w: %d", ErrN, n)
	}
	gens := Generators{G: make([]Point, n), H: make([]Point, n)}
	var err error
	for i := 0; i < n; i++ {
		gens.G[i], err = deterministicBasePoint(ad, "G", seed, i)
		if err != nil {
			return Generators{}, err
		}
		gens.H[i], err = deterministicBasePoint(ad, "H", seed, i)
		if err != nil {
			return Generators{}, err
		}
	}
	gens.U, err = deterministicBasePoint(ad, "U", seed, 0)
	if err != nil {
		return Generators{}, err
	}
	return gens, nil
}

func deterministicBasePoint(ad *P256Adapter, label string, seed []byte, index int) (Point, error) {
	q := ad.Order()
	for counter := uint64(0); counter < 1000; counter++ {
		h := sha256.New()
		_, _ = h.Write([]byte(label))
		_, _ = h.Write(seed)
		var indexBytes [4]byte
		binary.BigEndian.PutUint32(indexBytes[:], uint32(index))
		_, _ = h.Write(indexBytes[:])
		var counterBytes [8]byte
		binary.BigEndian.PutUint64(counterBytes[:], counter)
		_, _ = h.Write(counterBytes[:])
		scalar := new(big.Int).SetBytes(h.Sum(nil))
		scalar.Mod(scalar, new(big.Int).Sub(q, big.NewInt(1)))
		scalar.Add(scalar, big.NewInt(1))
		p, err := ad.ScalarBaseMult(scalar)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(p, ad.Identity()) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("%w: could not derive %s[%d]", ErrAdapter, label, index)
}
