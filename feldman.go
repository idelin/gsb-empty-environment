// Package netsec provides Feldman commitment verification for the
// distributed key generation (DKG) rounds of a threshold signature
// library.
//
// A DKG run has several participants that take turns acting as dealer.
// Each dealer publishes a Feldman commitment over the NIST P-256 group
// and hands one share to every receiver. This package verifies
// commitments and shares, runs the bounded complaint/disclosure window,
// disqualifies misbehaving dealers and derives the aggregated public key
// and aggregated shares once the window closes.
package netsec

import (
	"crypto/elliptic"
	"crypto/sha256"
	"errors"
	"math/big"
)

// Curve is the group used by every commitment and share.
var Curve = elliptic.P256()

// Point is a P-256 group element. The zero point (both coordinates nil
// or zero) represents the identity element (point at infinity).
type Point struct {
	X, Y *big.Int
}

// IsIdentity reports whether p is the point at infinity.
func (p Point) IsIdentity() bool {
	return p.X == nil || p.Y == nil || (p.X.Sign() == 0 && p.Y.Sign() == 0)
}

func (p Point) equal(other Point) bool {
	if p.IsIdentity() || other.IsIdentity() {
		return p.IsIdentity() && other.IsIdentity()
	}
	return p.X.Cmp(other.X) == 0 && p.Y.Cmp(other.Y) == 0
}

func validCurvePoint(p Point) bool {
	if p.X == nil || p.Y == nil {
		return false
	}
	if p.IsIdentity() {
		return false
	}
	return Curve.IsOnCurve(p.X, p.Y)
}

// inScalarRange reports whether s is a valid non-zero scalar:
// 1 <= s < N where N is the P-256 order.
func inScalarRange(s *big.Int) bool {
	if s == nil || s.Sign() == 0 {
		return false
	}
	return s.Cmp(Curve.Params().N) < 0
}

// scalarBytes renders s as a fixed-width big-endian byte string, so
// leading-zero scalars keep their value through the elliptic APIs.
func scalarBytes(s *big.Int) []byte {
	raw := s.Bytes()
	size := (Curve.Params().BitSize + 7) / 8
	if len(raw) >= size {
		return raw
	}
	out := make([]byte, size)
	copy(out[size-len(raw):], raw)
	return out
}

// Commitment is a Feldman commitment C = [g^a0, ..., g^a(t-1)] of
// threshold t, i.e. t P-256 points.
type Commitment struct {
	Points []Point
}

// Threshold returns the commitment size t.
func (c Commitment) Threshold() int {
	return len(c.Points)
}

// Constant returns the constant-term commitment g^a0.
func (c Commitment) Constant() Point {
	return c.Points[0]
}

// Validate checks the structural validity of a commitment:
// it must hold at least one point, every point must be a non-identity
// point on P-256, and no two points may be equal.
func (c Commitment) Validate() error {
	if len(c.Points) == 0 {
		return ErrEmptyCommitment
	}
	for i := range c.Points {
		if !validCurvePoint(c.Points[i]) {
			return ErrInvalidCommitmentPoint
		}
		for j := 0; j < i; j++ {
			if c.Points[i].equal(c.Points[j]) {
				return ErrDuplicateCommitmentPoint
			}
		}
	}
	return nil
}

// Hash is the binding digest of a commitment. It is the value that
// receivers quote in complaints and that is compared across receivers to
// detect a dealer broadcasting different commitments.
func (c Commitment) Hash() [32]byte {
	h := sha256.New()
	h.Write([]byte("netsec/feldman-v1/commitment"))
	for _, p := range c.Points {
		var raw []byte
		if !p.IsIdentity() {
			raw = elliptic.MarshalCompressed(Curve, p.X, p.Y)
		} else {
			// Fixed-length zero encoding; structurally invalid
			// commitments never reach aggregation anyway.
			raw = make([]byte, 33)
		}
		h.Write(raw)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// evalCommitment computes the product over i of C_i^(x^i), the value a
// valid share g^s must equal.
func evalCommitment(c Commitment, x *big.Int) Point {
	// Horner: result = C[t-1]; result = result^x * C[i].
	acc := c.Points[len(c.Points)-1]
	for i := len(c.Points) - 2; i >= 0; i-- {
		rx, ry := Curve.ScalarMult(acc.X, acc.Y, x.Bytes())
		acc = Point{X: rx, Y: ry}
		acc.X, acc.Y = Curve.Add(acc.X, acc.Y, c.Points[i].X, c.Points[i].Y)
	}
	return acc
}

// VerifyShare checks the Feldman relation g^s == product_i C_i^(x^i).
// x must be a non-zero, in-range, receiver scalar and s must be an
// in-range share scalar; anything else is rejected rather than checked.
func VerifyShare(c Commitment, x, s *big.Int) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if x == nil || x.Sign() == 0 {
		return ErrZeroIndex
	}
	if x.Cmp(Curve.Params().N) >= 0 {
		return ErrScalarOutOfRange
	}
	if !inScalarRange(s) {
		return ErrScalarOutOfRange
	}
	wantX, wantY := Curve.ScalarBaseMult(scalarBytes(s))
	got := evalCommitment(c, x)
	if got.X.Cmp(wantX) != 0 || got.Y.Cmp(wantY) != 0 {
		return ErrShareMismatch
	}
	return nil
}

// EvaluationError unwraps the concrete reason why a share fails against a
// commitment; it returns nil when the share is valid.
func EvaluationError(c Commitment, x, s *big.Int) error {
	return VerifyShare(c, x, s)
}

var (
	// ErrEmptyCommitment is returned for a commitment without points.
	ErrEmptyCommitment = errors.New("netsec: commitment has no points")
	// ErrInvalidCommitmentPoint is returned when a commitment point is
	// the identity or not on P-256.
	ErrInvalidCommitmentPoint = errors.New("netsec: invalid commitment point")
	// ErrDuplicateCommitmentPoint is returned when a commitment repeats
	// a point.
	ErrDuplicateCommitmentPoint = errors.New("netsec: duplicate point in commitment")
	// ErrZeroIndex is returned when the receiver index x is zero.
	ErrZeroIndex = errors.New("netsec: receiver index x is zero")
	// ErrScalarOutOfRange is returned for nil, zero or >= N scalars.
	ErrScalarOutOfRange = errors.New("netsec: scalar out of range [1,N-1]")
	// ErrShareMismatch is returned when g^s does not equal the
	// commitment evaluated at x.
	ErrShareMismatch = errors.New("netsec: share does not match commitment")
)
