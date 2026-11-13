package netsec

import (
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
)

const (
	// ScalarSize is the fixed-width encoding size of a P-256 scalar.
	ScalarSize = 32
	// PointSize is the size of an uncompressed SEC1 P-256 point.
	PointSize = 65

	// MaxMembers is the largest supported manifest size.
	MaxMembers = 16
	// MaxThreshold is the largest supported decryption threshold.
	MaxThreshold = 8
)

var (
	p256       = elliptic.P256()
	curveOrder = p256.Params().N
	fieldPrime = p256.Params().P

	// ErrInvalidInput identifies malformed or out-of-range input.
	ErrInvalidInput = errors.New("netsec: invalid input")
	// ErrInvalidPoint identifies a non-canonical or invalid point encoding.
	ErrInvalidPoint = errors.New("netsec: invalid point")
	// ErrInvalidScalar identifies a non-canonical scalar encoding.
	ErrInvalidScalar = errors.New("netsec: invalid scalar")
	// ErrInvalidManifest identifies a manifest whose aggregate key is inconsistent.
	ErrInvalidManifest = errors.New("netsec: invalid manifest")
)

// Scalar is a canonical fixed-width scalar in [0, n), where n is the P-256
// group order. The zero value is a valid encoding of zero.
type Scalar [ScalarSize]byte

// Point is a canonical uncompressed SEC1 encoding of a non-identity P-256
// point. The zero value is not a valid point.
type Point [PointSize]byte

// ParseScalar parses exactly 32 big-endian bytes and rejects values at or above
// the P-256 group order.
func ParseScalar(encoded []byte) (Scalar, error) {
	var out Scalar
	if len(encoded) != ScalarSize {
		return out, fmt.Errorf("%w: scalar must be %d bytes", ErrInvalidScalar, ScalarSize)
	}
	value := new(big.Int).SetBytes(encoded)
	if value.Cmp(curveOrder) >= 0 {
		return out, fmt.Errorf("%w: scalar is not below the group order", ErrInvalidScalar)
	}
	copy(out[:], encoded)
	return out, nil
}

// ScalarFromBig converts an integer to a canonical Scalar.
func ScalarFromBig(value *big.Int) (Scalar, error) {
	var out Scalar
	if value == nil || value.Sign() < 0 || value.Cmp(curveOrder) >= 0 {
		return out, fmt.Errorf("%w: value is outside [0, n)", ErrInvalidScalar)
	}
	value.FillBytes(out[:])
	return out, nil
}

// Bytes returns a copy of the scalar's fixed-width big-endian encoding.
func (s Scalar) Bytes() []byte {
	out := make([]byte, ScalarSize)
	copy(out, s[:])
	return out
}

// Big returns the scalar as a new big.Int.
func (s Scalar) Big() *big.Int {
	return new(big.Int).SetBytes(s[:])
}

// IsZero reports whether the scalar is zero.
func (s Scalar) IsZero() bool {
	return s == Scalar{}
}

func (s Scalar) valid() bool {
	return s.Big().Cmp(curveOrder) < 0
}

// ParsePoint parses an uncompressed SEC1 P-256 point and verifies that it is
// canonical, on the curve, and not the point at infinity.
func ParsePoint(encoded []byte) (Point, error) {
	var out Point
	if len(encoded) != PointSize {
		return out, fmt.Errorf("%w: point must be %d bytes", ErrInvalidPoint, PointSize)
	}
	if encoded[0] != 4 {
		return out, fmt.Errorf("%w: point must use uncompressed SEC1 encoding", ErrInvalidPoint)
	}

	x := new(big.Int).SetBytes(encoded[1:33])
	y := new(big.Int).SetBytes(encoded[33:65])
	if x.Cmp(fieldPrime) >= 0 || y.Cmp(fieldPrime) >= 0 {
		return out, fmt.Errorf("%w: coordinate is outside the base field", ErrInvalidPoint)
	}
	if x.Sign() == 0 && y.Sign() == 0 {
		return out, fmt.Errorf("%w: point at infinity is not allowed", ErrInvalidPoint)
	}
	if !p256.IsOnCurve(x, y) {
		return out, fmt.Errorf("%w: point is not on P-256", ErrInvalidPoint)
	}

	copy(out[:], encoded)
	return out, nil
}

// Bytes returns a copy of the point's uncompressed SEC1 encoding.
func (p Point) Bytes() []byte {
	out := make([]byte, PointSize)
	copy(out, p[:])
	return out
}

// Equal reports whether two canonical point encodings are identical.
func (p Point) Equal(other Point) bool {
	return p == other
}

func (p Point) valid() error {
	_, err := ParsePoint(p[:])
	return err
}

// BasePoint returns the canonical P-256 generator.
func BasePoint() Point {
	var out Point
	out[0] = 4
	p256.Params().Gx.FillBytes(out[1:33])
	p256.Params().Gy.FillBytes(out[33:65])
	return out
}

type curvePoint struct {
	x *big.Int
	y *big.Int
}

func identityPoint() curvePoint {
	return curvePoint{x: new(big.Int), y: new(big.Int)}
}

func (p curvePoint) isIdentity() bool {
	return p.x == nil || p.y == nil || (p.x.Sign() == 0 && p.y.Sign() == 0)
}

func pointFromCanonical(p Point) (curvePoint, error) {
	if err := p.valid(); err != nil {
		return curvePoint{}, err
	}
	return curvePoint{
		x: new(big.Int).SetBytes(p[1:33]),
		y: new(big.Int).SetBytes(p[33:65]),
	}, nil
}

func pointToCanonical(p curvePoint) (Point, error) {
	var out Point
	if p.isIdentity() {
		return out, fmt.Errorf("%w: point at infinity is not allowed", ErrInvalidPoint)
	}
	if p.x == nil || p.y == nil || p.x.Sign() < 0 || p.y.Sign() < 0 ||
		p.x.BitLen() > 256 || p.y.BitLen() > 256 {
		return out, fmt.Errorf("%w: invalid affine coordinates", ErrInvalidPoint)
	}
	out[0] = 4
	p.x.FillBytes(out[1:33])
	p.y.FillBytes(out[33:65])
	return ParsePoint(out[:])
}

func addCurvePoints(a, b curvePoint) curvePoint {
	if a.isIdentity() {
		return cloneCurvePoint(b)
	}
	if b.isIdentity() {
		return cloneCurvePoint(a)
	}
	x, y := p256.Add(a.x, a.y, b.x, b.y)
	return curvePoint{x: x, y: y}
}

func cloneCurvePoint(p curvePoint) curvePoint {
	if p.x == nil || p.y == nil {
		return identityPoint()
	}
	return curvePoint{x: new(big.Int).Set(p.x), y: new(big.Int).Set(p.y)}
}

func scalarMultCurve(k *big.Int, p curvePoint) curvePoint {
	if p.isIdentity() || k == nil {
		return identityPoint()
	}
	scalar := new(big.Int).Mod(new(big.Int).Set(k), curveOrder)
	if scalar.Sign() == 0 {
		return identityPoint()
	}
	buf := scalar.FillBytes(make([]byte, ScalarSize))
	x, y := p256.ScalarMult(p.x, p.y, buf)
	return curvePoint{x: x, y: y}
}

func scalarBaseMultCurve(k *big.Int) curvePoint {
	if k == nil {
		return identityPoint()
	}
	scalar := new(big.Int).Mod(new(big.Int).Set(k), curveOrder)
	if scalar.Sign() == 0 {
		return identityPoint()
	}
	buf := scalar.FillBytes(make([]byte, ScalarSize))
	x, y := p256.ScalarBaseMult(buf)
	return curvePoint{x: x, y: y}
}

func scalarMultPoint(k *big.Int, p Point) (Point, error) {
	internal, err := pointFromCanonical(p)
	if err != nil {
		return Point{}, err
	}
	return pointToCanonical(scalarMultCurve(k, internal))
}

// ScalarBaseMult returns kG. The zero scalar and resulting identity are
// rejected.
func ScalarBaseMult(scalar Scalar) (Point, error) {
	if !scalar.valid() {
		return Point{}, fmt.Errorf("%w: scalar is not canonical", ErrInvalidScalar)
	}
	return pointToCanonical(scalarBaseMultCurve(scalar.Big()))
}

// ScalarMult returns kP. The zero scalar, identity input, and identity result
// are rejected.
func ScalarMult(scalar Scalar, point Point) (Point, error) {
	if !scalar.valid() {
		return Point{}, fmt.Errorf("%w: scalar is not canonical", ErrInvalidScalar)
	}
	internal, err := pointFromCanonical(point)
	if err != nil {
		return Point{}, err
	}
	return pointToCanonical(scalarMultCurve(scalar.Big(), internal))
}

// AddPoints returns P + Q. Identity inputs or an identity result are rejected.
func AddPoints(left, right Point) (Point, error) {
	return addPointValues(left, right)
}

func addPointValues(a, b Point) (Point, error) {
	left, err := pointFromCanonical(a)
	if err != nil {
		return Point{}, err
	}
	right, err := pointFromCanonical(b)
	if err != nil {
		return Point{}, err
	}
	return pointToCanonical(addCurvePoints(left, right))
}

func randomSource(source io.Reader) io.Reader {
	if source == nil {
		return rand.Reader
	}
	return source
}

func randomNonZeroScalar(source io.Reader) (*big.Int, error) {
	upper := new(big.Int).Sub(curveOrder, big.NewInt(1))
	value, err := rand.Int(randomSource(source), upper)
	if err != nil {
		return nil, fmt.Errorf("netsec: sample scalar: %w", err)
	}
	return value.Add(value, big.NewInt(1)), nil
}

func randomScalar(source io.Reader) (*big.Int, error) {
	value, err := rand.Int(randomSource(source), curveOrder)
	if err != nil {
		return nil, fmt.Errorf("netsec: sample scalar: %w", err)
	}
	return value, nil
}

func modOrder(value *big.Int) *big.Int {
	out := new(big.Int).Mod(new(big.Int).Set(value), curveOrder)
	if out.Sign() < 0 {
		out.Add(out, curveOrder)
	}
	return out
}

func invertScalar(value *big.Int) (*big.Int, error) {
	reduced := modOrder(value)
	if reduced.Sign() == 0 {
		return nil, fmt.Errorf("%w: zero has no multiplicative inverse", ErrInvalidScalar)
	}
	inverse := new(big.Int).ModInverse(reduced, curveOrder)
	if inverse == nil {
		return nil, fmt.Errorf("%w: value has no multiplicative inverse", ErrInvalidScalar)
	}
	return inverse, nil
}
