package netsec

import (
	"crypto/ecdh"
	"crypto/elliptic"
	"errors"
	"math/big"
)

// PointSize is the byte length of one field element (x or y) for P-256.
const PointSize = 32

// UncompressedPointSize is the length of an uncompressed P-256 point:
// the 0x04 prefix plus the x and y coordinates.
const UncompressedPointSize = 1 + 2*PointSize

// ErrInvalidPoint is returned when a public point is malformed or does not
// satisfy the curve equation. A length-only check is never sufficient.
var ErrInvalidPoint = errors.New("netsec: invalid or off-curve public point")

var p256Params = elliptic.P256().Params()

// IsOnCurve reports whether the affine point (x, y) is a valid P-256 point.
// It enforces the short-Weierstrass curve equation
//
//	y^2 == x^3 - 3*x + b (mod p)
//
// together with the field-range constraints. The point at infinity is not a
// valid ECDH public key and is rejected.
func IsOnCurve(x, y *big.Int) bool {
	if x == nil || y == nil {
		return false
	}
	if x.Sign() < 0 || y.Sign() < 0 || x.Cmp(p256Params.P) >= 0 || y.Cmp(p256Params.P) >= 0 {
		return false
	}
	return p256Params.IsOnCurve(x, y)
}

// ValidatePoint parses an uncompressed P-256 point (0x04 || x || y) and
// verifies that it lies on the curve. A correctly sized but off-curve point
// (for example one whose y coordinate was tampered with) fails here.
func ValidatePoint(point []byte) error {
	if len(point) != UncompressedPointSize {
		return ErrInvalidPoint
	}
	if point[0] != 0x04 {
		return ErrInvalidPoint
	}
	x := new(big.Int).SetBytes(point[1 : 1+PointSize])
	y := new(big.Int).SetBytes(point[1+PointSize:])
	if !IsOnCurve(x, y) {
		return ErrInvalidPoint
	}
	return nil
}

// parsePoint performs the explicit curve-equation check and then converts the
// point into the standard library representation, which independently rejects
// off-curve points during parsing and the ECDH operation.
func parsePoint(point []byte) (*ecdh.PublicKey, error) {
	if err := ValidatePoint(point); err != nil {
		return nil, err
	}
	pub, err := ecdh.P256().NewPublicKey(point)
	if err != nil {
		return nil, ErrInvalidPoint
	}
	return pub, nil
}
