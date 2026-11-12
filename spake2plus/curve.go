package spake2plus

import (
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"math/big"
)

var p256 = elliptic.P256()

var (
	p256Order = p256.Params().N
	p256Prime = p256.Params().P
	p256B     = p256.Params().B
	// p256Prime mod 4 == 3, so modular square roots are x^((p+1)/4).
	p256SqrtExp = new(big.Int).Rsh(new(big.Int).Add(p256.Params().P, big.NewInt(1)), 2)
)

// point is an affine P-256 point. The point at infinity is represented as
// (0, 0), matching the crypto/elliptic convention; a nil x coordinate is
// also treated as infinity.
type point struct {
	x, y *big.Int
}

var infinity = point{}

func isInfinity(p point) bool {
	return p.x == nil || (p.x.Sign() == 0 && p.y.Sign() == 0)
}

func basePoint() point {
	params := p256.Params()
	return point{new(big.Int).Set(params.Gx), new(big.Int).Set(params.Gy)}
}

// Fixed generator points from RFC 9383 Section 4 (P-256), SEC 1 compressed.
var (
	pointM   = mustDecodePoint("02886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f")
	pointN   = mustDecodePoint("03d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49")
	encodedM = encodePoint(pointM)
	encodedN = encodePoint(pointN)
)

func mustDecodePoint(hexStr string) point {
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		panic("spake2plus: bad constant: " + err.Error())
	}
	p, err := decodePoint(raw)
	if err != nil {
		panic("spake2plus: bad constant: " + err.Error())
	}
	return p
}

func paddedScalar(k *big.Int) []byte {
	return k.FillBytes(make([]byte, ScalarLength))
}

func randomScalar() (*big.Int, error) {
	for {
		k, err := rand.Int(rand.Reader, p256Order)
		if err != nil {
			return nil, err
		}
		if k.Sign() != 0 {
			return k, nil
		}
	}
}

func scalarBaseMult(k *big.Int) point {
	x, y := p256.ScalarBaseMult(paddedScalar(k))
	return point{x, y}
}

func scalarMult(p point, k *big.Int) point {
	if isInfinity(p) {
		return infinity
	}
	x, y := p256.ScalarMult(p.x, p.y, paddedScalar(k))
	return point{x, y}
}

func addPoints(p, q point) point {
	if isInfinity(p) {
		return q
	}
	if isInfinity(q) {
		return p
	}
	x, y := p256.Add(p.x, p.y, q.x, q.y)
	return point{x, y}
}

func negPoint(p point) point {
	if isInfinity(p) {
		return infinity
	}
	return point{new(big.Int).Set(p.x), new(big.Int).Sub(p256Prime, p.y)}
}

func subPoints(p, q point) point {
	return addPoints(p, negPoint(q))
}

// encodePoint returns the SEC 1 uncompressed encoding of p (0x04 || x || y),
// the canonical element encoding of the RFC 9383 P-256 ciphersuites. It
// returns nil for the point at infinity, which has no SEC 1 encoding and is
// never put on the wire by this package.
func encodePoint(p point) []byte {
	if isInfinity(p) {
		return nil
	}
	buf := make([]byte, ShareLength)
	buf[0] = 4
	p.x.FillBytes(buf[1 : 1+ScalarLength])
	p.y.FillBytes(buf[1+ScalarLength:])
	return buf
}

// decodePoint parses a SEC 1 compressed or uncompressed P-256 point. The
// point at infinity (0x00) and any off-curve or malformed input is rejected,
// so the result is always a valid non-identity curve point.
func decodePoint(b []byte) (point, error) {
	switch {
	case len(b) == ScalarLength+1 && (b[0] == 2 || b[0] == 3):
		x := new(big.Int).SetBytes(b[1:])
		if x.Cmp(p256Prime) >= 0 {
			return infinity, errInvalidPointEncoding
		}
		rhs := new(big.Int).Mul(x, x)
		rhs.Mul(rhs, x)
		threeX := new(big.Int).Add(x, x)
		threeX.Add(threeX, x)
		rhs.Sub(rhs, threeX)
		rhs.Add(rhs, p256B)
		rhs.Mod(rhs, p256Prime)
		y := new(big.Int).Exp(rhs, p256SqrtExp, p256Prime)
		check := new(big.Int).Mul(y, y)
		check.Mod(check, p256Prime)
		if check.Cmp(rhs) != 0 {
			return infinity, errInvalidPointEncoding
		}
		if byte(y.Bit(0)) != b[0]&1 {
			y.Sub(p256Prime, y)
		}
		return point{x, y}, nil
	case len(b) == 2*ScalarLength+1 && b[0] == 4:
		x := new(big.Int).SetBytes(b[1 : 1+ScalarLength])
		y := new(big.Int).SetBytes(b[1+ScalarLength:])
		if x.Cmp(p256Prime) >= 0 || y.Cmp(p256Prime) >= 0 || !p256.IsOnCurve(x, y) {
			return infinity, errInvalidPointEncoding
		}
		return point{x, y}, nil
	default:
		return infinity, errInvalidPointEncoding
	}
}

func wipeScalar(s *big.Int) {
	if s == nil {
		return
	}
	words := s.Bits()
	for i := range words {
		words[i] = 0
	}
}
