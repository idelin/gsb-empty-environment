package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
)

const (
	// HpDomainLabel is the fixed domain separator used by Hp.
	HpDomainLabel = "netsec/q0047/hp"

	// MaxRingSize is the largest number of members accepted by this package.
	MaxRingSize = 64

	compressedPointSize = 33
	scalarSize          = 32

	// ScalarSize is the canonical scalar width.
	ScalarSize = scalarSize
	// CompressedPointSize is the canonical compressed point width.
	CompressedPointSize = compressedPointSize
)

// P256 returns the NIST P-256 curve used by this package.
func P256() elliptic.Curve {
	return curveP256
}

var (
	errInvalidPoint  = errors.New("invalid P-256 point")
	errIdentityPoint = errors.New("point at infinity is not allowed")
	errInvalidScalar = errors.New("non-canonical scalar")
	errDuplicateRing = errors.New("ring contains duplicate public keys")
	errInvalidRing   = errors.New("invalid ring")
	errHpFailed      = errors.New("could not derive Hp point")
	curveP256        = elliptic.P256()
	groupOrder       = new(big.Int).Set(curveP256.Params().N)
	fieldPrime       = new(big.Int).Set(curveP256.Params().P)
	curveB           = new(big.Int).Set(curveP256.Params().B)
	sqrtExponent     = new(big.Int).Rsh(new(big.Int).Add(fieldPrime, big.NewInt(1)), 2)
)

// Exported sentinel errors for callers that need to distinguish rejection
// reasons with errors.Is.
var (
	ErrInvalidPoint     = errInvalidPoint
	ErrIdentityPoint    = errIdentityPoint
	ErrInvalidScalar    = errInvalidScalar
	ErrDuplicateRing    = errDuplicateRing
	ErrInvalidRing      = errInvalidRing
	ErrHpFailed         = errHpFailed
	ErrInvalidSignature = errors.New("invalid ring signature")
)

// point is an affine point. A nil X or Y denotes the point at infinity.
type point struct {
	x *big.Int
	y *big.Int
}

// Point is the exported view of an affine P-256 point. Coordinates are
// available through XCoord and YCoord; Compressed returns the SEC1 encoding.
type Point = point

func (p *point) isInfinity() bool {
	return p == nil || p.x == nil || p.y == nil
}

// IsInfinity reports whether p is the point at infinity.
func (p *point) IsInfinity() bool {
	return p.isInfinity()
}

// XCoord returns the x coordinate, or nil for the point at infinity.
func (p *point) XCoord() *big.Int {
	if p.isInfinity() {
		return nil
	}
	return new(big.Int).Set(p.x)
}

// YCoord returns the y coordinate, or nil for the point at infinity.
func (p *point) YCoord() *big.Int {
	if p.isInfinity() {
		return nil
	}
	return new(big.Int).Set(p.y)
}

// Compressed returns the canonical SEC1 compressed encoding, or nil for the
// point at infinity.
func (p *point) Compressed() []byte {
	return marshalCompressed(p)
}

// ECDSAPublic converts p to an *ecdsa.PublicKey on P-256.
func (p *point) ECDSAPublic() (*ecdsa.PublicKey, error) {
	if p.isInfinity() {
		return nil, errIdentityPoint
	}
	return &ecdsa.PublicKey{Curve: curveP256, X: new(big.Int).Set(p.x), Y: new(big.Int).Set(p.y)}, nil
}

func infinity() *point {
	return &point{}
}

func copyPoint(p *point) *point {
	if p.isInfinity() {
		return infinity()
	}
	return &point{new(big.Int).Set(p.x), new(big.Int).Set(p.y)}
}

func pointEqual(a, b *point) bool {
	ai, bi := a.isInfinity(), b.isInfinity()
	if ai || bi {
		return ai && bi
	}
	return a.x.Cmp(b.x) == 0 && a.y.Cmp(b.y) == 0
}

func pointNeg(p *point) *point {
	if p.isInfinity() {
		return infinity()
	}
	return &point{new(big.Int).Set(p.x), new(big.Int).Sub(fieldPrime, p.y)}
}

func pointAdd(a, b *point) *point {
	if a.isInfinity() {
		return copyPoint(b)
	}
	if b.isInfinity() {
		return copyPoint(a)
	}
	x, y := curveP256.Add(a.x, a.y, b.x, b.y)
	if x == nil || y == nil {
		return infinity()
	}
	return &point{x, y}
}

func pointDouble(p *point) *point {
	if p.isInfinity() {
		return infinity()
	}
	x, y := curveP256.Double(p.x, p.y)
	if x == nil || y == nil {
		return infinity()
	}
	return &point{x, y}
}

func scalarMultBase(k *big.Int) *point {
	if k.Sign() == 0 {
		return infinity()
	}
	x, y := curveP256.ScalarBaseMult(k.Bytes())
	if x == nil || y == nil {
		return infinity()
	}
	return &point{x, y}
}

func scalarMult(p *point, k *big.Int) *point {
	if p.isInfinity() || k.Sign() == 0 {
		return infinity()
	}
	x, y := curveP256.ScalarMult(p.x, p.y, k.Bytes())
	if x == nil || y == nil {
		return infinity()
	}
	return &point{x, y}
}

func pointMulAdd(k *big.Int, p *point, l *big.Int) *point {
	return pointAdd(scalarMultBase(k), scalarMult(p, l))
}

// marshalCompressed returns the SEC1 compressed encoding of p.
func marshalCompressed(p *point) []byte {
	if p.isInfinity() {
		return nil
	}
	out := make([]byte, compressedPointSize)
	out[0] = 0x02
	if p.y.Bit(0) != 0 {
		out[0] = 0x03
	}
	xb := p.x.Bytes()
	copy(out[compressedPointSize-len(xb):], xb)
	return out
}

// encodeHashPoint encodes a point for the challenge transcript. The point at
// infinity is the single byte 0x00; all other points are compressed SEC1.
func encodeHashPoint(p *point) []byte {
	if p.isInfinity() {
		return []byte{0x00}
	}
	return marshalCompressed(p)
}

func isCanonicalCompressed(data []byte) bool {
	return len(data) == compressedPointSize && (data[0] == 0x02 || data[0] == 0x03)
}

func sqrtModP(v *big.Int) (*big.Int, bool) {
	if v.Sign() == 0 {
		return new(big.Int), true
	}
	y := new(big.Int).Exp(v, sqrtExponent, fieldPrime)
	check := new(big.Int).Mul(y, y)
	check.Mod(check, fieldPrime)
	if check.Cmp(v) != 0 {
		return nil, false
	}
	return y, true
}

// parseCompressedPoint parses only canonical SEC1 compressed encodings and
// rejects non-canonical x coordinates and points not on the curve.
func parseCompressedPoint(data []byte) (*point, error) {
	if len(data) == 1 && data[0] == 0x00 {
		return nil, errIdentityPoint
	}
	if !isCanonicalCompressed(data) {
		return nil, fmt.Errorf("%w: expected 33-byte compressed encoding", errInvalidPoint)
	}
	x := new(big.Int).SetBytes(data[1:])
	if x.Sign() < 0 || x.Cmp(fieldPrime) >= 0 {
		return nil, fmt.Errorf("%w: x coordinate is not canonical", errInvalidPoint)
	}

	// rhs = x^3 - 3x + b (mod p).
	x3 := new(big.Int).Mul(x, x)
	x3.Mul(x3, x)
	threeX := new(big.Int).Lsh(x, 1)
	threeX.Add(threeX, x)
	rhs := new(big.Int).Sub(x3, threeX)
	rhs.Add(rhs, curveB)
	rhs.Mod(rhs, fieldPrime)

	y, ok := sqrtModP(rhs)
	if !ok {
		return nil, fmt.Errorf("%w: x coordinate is not on the curve", errInvalidPoint)
	}
	wantedOdd := data[0] == 0x03
	if (y.Bit(0) != 0) != wantedOdd {
		y.Sub(fieldPrime, y)
	}
	if !curveP256.IsOnCurve(x, y) {
		return nil, fmt.Errorf("%w: point is not on P-256", errInvalidPoint)
	}
	return &point{x, y}, nil
}

func parseKeyImage(data []byte) (*point, error) {
	p, err := parseCompressedPoint(data)
	if err != nil {
		return nil, err
	}
	if p.isInfinity() {
		return nil, errIdentityPoint
	}
	return p, nil
}

// ParsePublicKey decodes a canonical compressed P-256 public key.
func ParsePublicKey(data []byte) (*ecdsa.PublicKey, error) {
	p, err := parseCompressedPoint(data)
	if err != nil {
		return nil, err
	}
	return p.ECDSAPublic()
}

// DecodePublicKey is an alias for ParsePublicKey.
func DecodePublicKey(data []byte) (*ecdsa.PublicKey, error) {
	return ParsePublicKey(data)
}

func canonicalScalar(data []byte) (*big.Int, error) {
	if len(data) != scalarSize {
		return nil, fmt.Errorf("%w: scalar must be 32 bytes", errInvalidScalar)
	}
	k := new(big.Int).SetBytes(data)
	if k.Cmp(groupOrder) >= 0 {
		return nil, fmt.Errorf("%w: scalar is not less than the group order", errInvalidScalar)
	}
	return k, nil
}

func scalarBytes(k *big.Int) []byte {
	out := make([]byte, scalarSize)
	b := k.Bytes()
	copy(out[scalarSize-len(b):], b)
	return out
}

func validScalar(k *big.Int) bool {
	return k != nil && k.Sign() >= 0 && k.Cmp(groupOrder) < 0
}

func scalarSub(a, b *big.Int) *big.Int {
	out := new(big.Int).Sub(a, b)
	out.Mod(out, groupOrder)
	if out.Sign() < 0 {
		out.Add(out, groupOrder)
	}
	return out
}

// Hp derives the point used for key-image construction. The SHA-256 input is
// domain label || scope || compressed public key || 4-byte big-endian counter.
// The digest is interpreted as an x coordinate; candidates whose x is not
// canonical or not on the curve are skipped. The even-y point is returned.
func Hp(publicKey, scope []byte) (*Point, error) {
	if _, err := parseCompressedPoint(publicKey); err != nil {
		return nil, err
	}
	label := []byte(HpDomainLabel)
	input := make([]byte, 0, len(label)+len(scope)+len(publicKey)+4)
	input = append(input, label...)
	input = append(input, scope...)
	input = append(input, publicKey...)

	var counter [4]byte
	for i := 0; i <= 0xff; i++ {
		binary.BigEndian.PutUint32(counter[:], uint32(i))
		h := sha256.New()
		h.Write(input)
		h.Write(counter[:])
		digest := h.Sum(nil)

		x := new(big.Int).SetBytes(digest)
		if x.Cmp(fieldPrime) >= 0 {
			continue
		}
		x3 := new(big.Int).Mul(x, x)
		x3.Mul(x3, x)
		threeX := new(big.Int).Lsh(x, 1)
		threeX.Add(threeX, x)
		rhs := new(big.Int).Sub(x3, threeX)
		rhs.Add(rhs, curveB)
		rhs.Mod(rhs, fieldPrime)
		y, ok := sqrtModP(rhs)
		if !ok {
			continue
		}
		if y.Bit(0) != 0 {
			y.Sub(fieldPrime, y)
		}
		if !curveP256.IsOnCurve(x, y) {
			continue
		}
		return &point{x, y}, nil
	}
	return nil, errHpFailed
}

// HpBytes is a convenience wrapper around Hp returning the compressed point.
func HpBytes(publicKey, scope []byte) ([]byte, error) {
	p, err := Hp(publicKey, scope)
	if err != nil {
		return nil, err
	}
	return marshalCompressed(p), nil
}

// CompressPublicKey converts a P-256 public key to canonical compressed form.
func CompressPublicKey(pub any) ([]byte, error) {
	return PublicKeyBytes(pub)
}

func appendLengthPrefixed(dst, value []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	dst = append(dst, length[:]...)
	dst = append(dst, value...)
	return dst
}

func encodeRing(ring [][]byte) []byte {
	out := make([]byte, 0, len(ring)*compressedPointSize)
	for _, member := range ring {
		out = append(out, member...)
	}
	return out
}

// challenge hashes the length-prefixed transcript and reduces the digest
// modulo the P-256 group order.
func challenge(scope []byte, ring [][]byte, message []byte, l, r *point) *big.Int {
	transcript := make([]byte, 0, len(scope)+len(message)+len(ring)*compressedPointSize+80)
	transcript = appendLengthPrefixed(transcript, scope)
	transcript = appendLengthPrefixed(transcript, encodeRing(ring))
	transcript = appendLengthPrefixed(transcript, message)
	transcript = appendLengthPrefixed(transcript, encodeHashPoint(l))
	transcript = appendLengthPrefixed(transcript, encodeHashPoint(r))

	digest := sha256.Sum256(transcript)
	c := new(big.Int).SetBytes(digest[:])
	c.Mod(c, groupOrder)
	return c
}

// GroupOrder returns a copy of the P-256 group order.
func GroupOrder() *big.Int {
	return new(big.Int).Set(groupOrder)
}
