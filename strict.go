// Package netsec provides strict Ed25519 signature verification and
// revocation key derivation for the license service.
//
// The strict verifier rejects non-canonical scalars (S >= L), non-canonical
// point encodings, small-order points (for both R and the public key), and
// the identity public key. This prevents signature malleability: without
// these checks a single message can have multiple distinct valid signatures,
// which would let revoked licenses re-enter under a different signature if
// the revocation table were keyed by signature bytes.
//
// Revocation keys are therefore derived from the message digest and the
// public key only, never from the signature bytes.
package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

var (
	ErrBadSignatureLength = errors.New("netsec: bad signature length")
	ErrBadPublicKeyLength = errors.New("netsec: bad public key length")
	ErrNonCanonicalS      = errors.New("netsec: signature scalar S is not canonical (S >= L)")
	ErrNonCanonicalPoint  = errors.New("netsec: point encoding is not canonical")
	ErrNotOnCurve         = errors.New("netsec: point is not on the edwards25519 curve")
	ErrSmallOrderPoint    = errors.New("netsec: point has small order")
	ErrIdentityPublicKey  = errors.New("netsec: public key is the identity point")
	ErrInvalidSignature   = errors.New("netsec: invalid signature")
)

// groupOrder L = 2^252 + 27742317777372353535851937790883648493.
var groupOrder = func() *big.Int {
	l := new(big.Int).Lsh(big.NewInt(1), 252)
	c, _ := new(big.Int).SetString("27742317777372353535851937790883648493", 10)
	return l.Add(l, c)
}()

// fieldPrime p = 2^255 - 19.
var fieldPrime = func() *big.Int {
	p := new(big.Int).Lsh(big.NewInt(1), 255)
	return p.Sub(p, big.NewInt(19))
}()

// curveParamD = -121665/121666 mod p.
var curveParamD, _ = new(big.Int).SetString("37095705934669439343138083508754565189542113879843219016388785533085940283555", 10)

// sqrtM1 = 2^((p-1)/4) mod p, a square root of -1.
var sqrtM1, _ = new(big.Int).SetString("19681161376707505956807079304988542015446066515923890162744021073123829784752", 10)

var bigOne = big.NewInt(1)

// VerifyStrict verifies an Ed25519 signature with strict canonicality and
// small-order checks before delegating the equation check to the standard
// library. It returns nil only if the signature is valid and every
// strictness requirement holds.
func VerifyStrict(publicKey, message, signature []byte) error {
	if len(signature) != ed25519.SignatureSize {
		return ErrBadSignatureLength
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return ErrBadPublicKeyLength
	}

	// S must be a canonical scalar: S < L.
	s := leToBig(signature[32:])
	if s.Cmp(groupOrder) >= 0 {
		return ErrNonCanonicalS
	}

	// R must be a canonically encoded curve point and not of small order.
	r, err := decodePointStrict(signature[:32])
	if err != nil {
		return fmt.Errorf("netsec: bad R: %w", err)
	}
	if isSmallOrder(r) {
		return fmt.Errorf("netsec: bad R: %w", ErrSmallOrderPoint)
	}

	// The public key must be a canonically encoded curve point, not of
	// small order, and not the identity.
	a, err := decodePointStrict(publicKey)
	if err != nil {
		return fmt.Errorf("netsec: bad public key: %w", err)
	}
	if isIdentity(a) {
		return ErrIdentityPublicKey
	}
	if isSmallOrder(a) {
		return fmt.Errorf("netsec: bad public key: %w", ErrSmallOrderPoint)
	}

	if !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return ErrInvalidSignature
	}
	return nil
}

// RevocationKey derives the revocation-table key for a license. It depends
// only on the message digest and the public key, so every valid signature
// for the same message and key maps to a single revocation entry regardless
// of signature malleability.
func RevocationKey(publicKey, message []byte) [32]byte {
	digest := sha256.Sum256(message)
	h := sha256.New()
	h.Write(digest[:])
	h.Write(publicKey)
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

// point is an edwards25519 point in affine coordinates.
type point struct {
	x, y *big.Int
}

func leToBig(b []byte) *big.Int {
	rev := make([]byte, len(b))
	for i, c := range b {
		rev[len(b)-1-i] = c
	}
	return new(big.Int).SetBytes(rev)
}

// decodePointStrict decodes a compressed edwards25519 point, rejecting
// non-canonical encodings (y >= p) and encodings that do not correspond to
// a curve point.
func decodePointStrict(enc []byte) (*point, error) {
	if len(enc) != 32 {
		return nil, ErrNonCanonicalPoint
	}
	sign := enc[31] >> 7
	yBytes := make([]byte, 32)
	copy(yBytes, enc)
	yBytes[31] &= 0x7f
	y := leToBig(yBytes)
	if y.Cmp(fieldPrime) >= 0 {
		return nil, ErrNonCanonicalPoint
	}

	// x^2 = (y^2 - 1) / (d*y^2 + 1) mod p.
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, fieldPrime)
	num := new(big.Int).Sub(y2, bigOne)
	num.Mod(num, fieldPrime)
	den := new(big.Int).Mul(curveParamD, y2)
	den.Add(den, bigOne)
	den.Mod(den, fieldPrime)
	denInv := new(big.Int).ModInverse(den, fieldPrime)
	if denInv == nil {
		return nil, ErrNotOnCurve
	}
	x2 := new(big.Int).Mul(num, denInv)
	x2.Mod(x2, fieldPrime)

	x := modSqrt(x2)
	if x == nil {
		return nil, ErrNotOnCurve
	}
	if x.Sign() == 0 && sign == 1 {
		// x = 0 with the sign bit set is not a valid encoding.
		return nil, ErrNotOnCurve
	}
	if byte(x.Bit(0)) != sign {
		x.Sub(fieldPrime, x)
	}
	return &point{x: x, y: y}, nil
}

// modSqrt computes a square root of a mod p for p == 5 (mod 8), or nil if a
// is not a quadratic residue.
func modSqrt(a *big.Int) *big.Int {
	// x = a^((p+3)/8)
	exp := new(big.Int).Add(fieldPrime, big.NewInt(3))
	exp.Rsh(exp, 3)
	x := new(big.Int).Exp(a, exp, fieldPrime)
	check := new(big.Int).Mul(x, x)
	check.Mod(check, fieldPrime)
	if check.Cmp(a) != 0 {
		x.Mul(x, sqrtM1)
		x.Mod(x, fieldPrime)
		check.Mul(x, x)
		check.Mod(check, fieldPrime)
		if check.Cmp(a) != 0 {
			return nil
		}
	}
	return x
}

func isIdentity(p *point) bool {
	return p.x.Sign() == 0 && p.y.Cmp(bigOne) == 0
}

// isSmallOrder reports whether [8]P is the identity, i.e. whether P lies in
// the order-8 cofactor subgroup.
func isSmallOrder(p *point) bool {
	q := p
	for i := 0; i < 3; i++ {
		q = addPoints(q, q)
	}
	return isIdentity(q)
}

// addPoints adds two edwards25519 points using the affine addition formulas
// for a*x^2 + y^2 = 1 + d*x^2*y^2 with a = -1.
func addPoints(p1, p2 *point) *point {
	x1y2 := new(big.Int).Mul(p1.x, p2.y)
	x2y1 := new(big.Int).Mul(p2.x, p1.y)
	y1y2 := new(big.Int).Mul(p1.y, p2.y)
	x1x2 := new(big.Int).Mul(p1.x, p2.x)
	x1x2y1y2 := new(big.Int).Mul(x1x2, y1y2)
	x1x2y1y2.Mod(x1x2y1y2, fieldPrime)
	dxy := new(big.Int).Mul(curveParamD, x1x2y1y2)
	dxy.Mod(dxy, fieldPrime)

	xNum := new(big.Int).Add(x1y2, x2y1)
	xNum.Mod(xNum, fieldPrime)
	xDen := new(big.Int).Add(bigOne, dxy)
	xDen.Mod(xDen, fieldPrime)

	yNum := new(big.Int).Add(y1y2, x1x2)
	yNum.Mod(yNum, fieldPrime)
	yDen := new(big.Int).Sub(bigOne, dxy)
	yDen.Mod(yDen, fieldPrime)

	xDenInv := new(big.Int).ModInverse(xDen, fieldPrime)
	yDenInv := new(big.Int).ModInverse(yDen, fieldPrime)
	if xDenInv == nil || yDenInv == nil {
		// Unreachable for genuine curve points; fail closed.
		return &point{x: new(big.Int), y: new(big.Int).Set(bigOne)}
	}
	x3 := new(big.Int).Mul(xNum, xDenInv)
	x3.Mod(x3, fieldPrime)
	y3 := new(big.Int).Mul(yNum, yDenInv)
	y3.Mod(y3, fieldPrime)
	return &point{x: x3, y: y3}
}
