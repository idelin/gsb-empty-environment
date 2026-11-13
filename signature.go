package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// Signature is a linkable ring signature over P-256.
//
// C0 is the 32-byte challenge that starts and ends the verification chain.
// S contains one 32-byte response per ring member, in ring order. I is the
// canonical compressed key image. KeyImage and Responses are kept as aliases
// for callers that prefer descriptive names; the generation and verification
// paths accept either representation.
type Signature struct {
	C0        []byte
	C         []byte
	Challenge []byte
	S         [][]byte
	I         []byte
	KeyImage  []byte
	Responses [][]byte
}

// RingSignature is an alias kept for callers that use the more explicit name.
type RingSignature = Signature

// Ring is a ring in submission order.
type Ring = [][]byte

func (s *Signature) c0Bytes() []byte {
	if s == nil {
		return nil
	}
	if s.C0 == nil {
		if s.Challenge != nil {
			return s.Challenge
		}
		return s.C
	}
	return s.C0
}

func (s *Signature) responses() [][]byte {
	if s == nil {
		return nil
	}
	if s.S != nil {
		return s.S
	}
	return s.Responses
}

func (s *Signature) keyImageBytes() []byte {
	if s == nil {
		return nil
	}
	if s.I != nil {
		return s.I
	}
	return s.KeyImage
}

func (s *Signature) syncAliases() {
	s.S = s.Responses
	s.I = s.KeyImage
}

func newSignature(c0 *big.Int, responses [][]byte, keyImage *point) *Signature {
	image := marshalCompressed(keyImage)
	challenge := scalarBytes(c0)
	return &Signature{
		C0:        challenge,
		C:         challenge,
		Challenge: challenge,
		S:         responses,
		I:         image,
		KeyImage:  image,
		Responses: responses,
	}
}

func (s *Signature) aliasesConsistent() error {
	if s == nil {
		return errors.New("signature is nil")
	}
	if s.I != nil && s.KeyImage != nil && !bytes.Equal(s.I, s.KeyImage) {
		return errors.New("I and KeyImage disagree")
	}
	if s.C0 != nil && s.C != nil && !bytes.Equal(s.C0, s.C) {
		return errors.New("C0 and C disagree")
	}
	if s.C0 != nil && s.Challenge != nil && !bytes.Equal(s.C0, s.Challenge) {
		return errors.New("C0 and Challenge disagree")
	}
	if s.S != nil && s.Responses != nil {
		if len(s.S) != len(s.Responses) {
			return errors.New("S and Responses disagree")
		}
		for i := range s.S {
			if !bytes.Equal(s.S[i], s.Responses[i]) {
				return fmt.Errorf("S and Responses disagree at index %d", i)
			}
		}
	}
	return nil
}

// PublicKeyBytes returns the canonical compressed encoding of a P-256 public
// key. It accepts *ecdsa.PublicKey, ecdsa.PublicKey, or an already compressed
// 33-byte encoding.
func PublicKeyBytes(publicKey any) ([]byte, error) {
	switch key := publicKey.(type) {
	case []byte:
		if _, err := parseCompressedPoint(key); err != nil {
			return nil, err
		}
		return append([]byte(nil), key...), nil
	case *ecdsa.PublicKey:
		return compressECDSAPublicKey(key)
	case ecdsa.PublicKey:
		return compressECDSAPublicKey(&key)
	default:
		return nil, fmt.Errorf("%w: unsupported public key type %T", errInvalidPoint, publicKey)
	}
}

func compressECDSAPublicKey(pub *ecdsa.PublicKey) ([]byte, error) {
	if pub == nil || pub.X == nil || pub.Y == nil {
		return nil, fmt.Errorf("%w: nil public key coordinates", errInvalidPoint)
	}
	if pub.Curve != nil && !isP256(pub.Curve) {
		return nil, fmt.Errorf("%w: public key is not on P-256", errInvalidPoint)
	}
	if !curveP256.IsOnCurve(pub.X, pub.Y) {
		return nil, fmt.Errorf("%w: public key is not on P-256", errInvalidPoint)
	}
	return marshalCompressed(&point{new(big.Int).Set(pub.X), new(big.Int).Set(pub.Y)}), nil
}

func isP256(curve elliptic.Curve) bool {
	if curve == nil {
		return false
	}
	params := curve.Params()
	if params == nil {
		return false
	}
	return params.P.Cmp(fieldPrime) == 0 &&
		params.N.Cmp(groupOrder) == 0 &&
		params.B != nil && params.B.Cmp(curveB) == 0 &&
		params.Gx != nil && params.Gx.Cmp(curveP256.Params().Gx) == 0 &&
		params.Gy != nil && params.Gy.Cmp(curveP256.Params().Gy) == 0
}

// validateRing canonicalizes and validates every member. It preserves the
// caller's submission order and rejects identity, invalid, or duplicate keys.
func validateRing(ring [][]byte) ([][]byte, error) {
	if len(ring) < 1 || len(ring) > MaxRingSize {
		return nil, fmt.Errorf("%w: size must be between 1 and %d", errInvalidRing, MaxRingSize)
	}
	out := make([][]byte, len(ring))
	seen := make(map[string]struct{}, len(ring))
	for i, member := range ring {
		if _, err := parseCompressedPoint(member); err != nil {
			return nil, fmt.Errorf("%w: member %d: %w", errInvalidRing, i, err)
		}
		key := string(member)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: member %d", errDuplicateRing, i)
		}
		seen[key] = struct{}{}
		out[i] = append([]byte(nil), member...)
	}
	return out, nil
}

type scalarSource interface {
	nextScalar(allowZero bool) (*big.Int, error)
}

type readerScalarSource struct {
	reader io.Reader
}

func (s readerScalarSource) nextScalar(allowZero bool) (*big.Int, error) {
	for attempt := 0; attempt < 1024; attempt++ {
		buf := make([]byte, scalarSize)
		if _, err := io.ReadFull(s.reader, buf); err != nil {
			return nil, fmt.Errorf("read random scalar: %w", err)
		}
		k := new(big.Int).SetBytes(buf)
		if k.Cmp(groupOrder) >= 0 {
			continue
		}
		if !allowZero && k.Sign() == 0 {
			continue
		}
		return k, nil
	}
	return nil, fmt.Errorf("%w: random source did not produce an acceptable scalar", errInvalidScalar)
}

func randomSource(reader io.Reader) scalarSource {
	if reader == nil {
		return readerScalarSource{reader: rand.Reader}
	}
	return readerScalarSource{reader: reader}
}

// GenerateRingSignature creates a linkable ring signature. The ring must
// contain between 1 and 64 distinct canonical compressed P-256 keys and stays
// in submission order. Only privateKey for ring[signerIndex] is needed.
//
// rng supplies all random scalars. Each scalar is read as 32 big-endian bytes
// and rejected (and read again) when it is not below the group order. The
// nonce k must be non-zero; the other responses may be zero. Passing nil uses
// crypto/rand.Reader.
func generateRingSignature(ring [][]byte, signerIndex int, privateKey *ecdsa.PrivateKey, message, scope []byte, rng io.Reader) (*Signature, error) {
	return generateRingSignatureSource(ring, signerIndex, privateKey, message, scope, randomSource(rng))
}

func generateRingSignatureSource(ring [][]byte, signerIndex int, privateKey *ecdsa.PrivateKey, message, scope []byte, source scalarSource) (*Signature, error) {
	members, err := validateRing(ring)
	if err != nil {
		return nil, err
	}
	if privateKey == nil || privateKey.D == nil {
		return nil, errors.New("private key is required")
	}
	if !validScalar(privateKey.D) || privateKey.D.Sign() == 0 {
		return nil, fmt.Errorf("%w: private key", errInvalidScalar)
	}
	if signerIndex < 0 {
		signerIndex, err = resolveSignerIndex(members, privateKey.D)
		if err != nil {
			return nil, err
		}
	}
	if signerIndex < 0 || signerIndex >= len(members) {
		return nil, fmt.Errorf("%w: signer index %d out of range", errInvalidRing, signerIndex)
	}
	if privateKey.Curve != nil && !isP256(privateKey.Curve) {
		return nil, errors.New("private key is not on P-256")
	}
	expected := scalarMultBase(privateKey.D)
	actual, err := parseCompressedPoint(members[signerIndex])
	if err != nil {
		return nil, err
	}
	if !pointEqual(expected, actual) {
		return nil, errors.New("private key does not match signer public key")
	}
	ring = members
	return generateWithScalarMembers(members, signerIndex, privateKey.D, message, scope, source)
}

func generateWithScalar(ring [][]byte, signerIndex int, x *big.Int, message, scope []byte, source scalarSource) (*Signature, error) {
	members, err := validateRing(ring)
	if err != nil {
		return nil, err
	}
	return generateWithScalarMembers(members, signerIndex, x, message, scope, source)
}

func generateWithScalarMembers(ring [][]byte, signerIndex int, x *big.Int, message, scope []byte, source scalarSource) (*Signature, error) {
	if !validScalar(x) || x.Sign() == 0 {
		return nil, fmt.Errorf("%w: private key", errInvalidScalar)
	}
	if signerIndex < 0 {
		var err error
		signerIndex, err = resolveSignerIndex(ring, x)
		if err != nil {
			return nil, err
		}
	}
	if signerIndex < 0 || signerIndex >= len(ring) {
		return nil, fmt.Errorf("%w: signer index %d out of range", errInvalidRing, signerIndex)
	}
	claimed, err := parseCompressedPoint(ring[signerIndex])
	if err != nil {
		return nil, err
	}
	if !pointEqual(scalarMultBase(x), claimed) {
		return nil, errors.New("private key does not match signer public key")
	}
	if source == nil {
		return nil, errors.New("random scalar source is required")
	}
	hp, err := Hp(ring[signerIndex], scope)
	if err != nil {
		return nil, err
	}
	image := scalarMult(hp, x)
	if image.isInfinity() {
		return nil, errIdentityPoint
	}

	nonce, err := source.nextScalar(false)
	if err != nil {
		return nil, err
	}
	l := scalarMultBase(nonce)
	r := scalarMult(hp, nonce)
	afterSigner := challenge(scope, ring, message, l, r)

	responses := make([][]byte, len(ring))
	c := new(big.Int).Set(afterSigner)
	for i := signerIndex + 1; i < len(ring); i++ {
		s, err := source.nextScalar(true)
		if err != nil {
			return nil, err
		}
		responses[i] = scalarBytes(s)
		hpI, err := Hp(ring[i], scope)
		if err != nil {
			return nil, err
		}
		li := pointMulAdd(s, mustPoint(ring[i]), c)
		ri := pointAdd(scalarMult(hpI, s), scalarMult(image, c))
		c = challenge(scope, ring, message, li, ri)
	}

	// The final challenge of the upper segment is the starting challenge of
	// the verification chain.
	c0 := new(big.Int).Set(c)
	for i := 0; i < signerIndex; i++ {
		s, err := source.nextScalar(true)
		if err != nil {
			return nil, err
		}
		responses[i] = scalarBytes(s)
		hpI, err := Hp(ring[i], scope)
		if err != nil {
			return nil, err
		}
		li := pointMulAdd(s, mustPoint(ring[i]), c)
		ri := pointAdd(scalarMult(hpI, s), scalarMult(image, c))
		c = challenge(scope, ring, message, li, ri)
	}

	signerResponse := scalarSub(nonce, new(big.Int).Mul(c, x))
	responses[signerIndex] = scalarBytes(signerResponse)

	sig := newSignature(c0, responses, image)
	sig.syncAliases()
	return sig, nil
}

func resolveSignerIndex(ring [][]byte, privateScalar *big.Int) (int, error) {
	public := scalarMultBase(privateScalar)
	for i, member := range ring {
		parsed, err := parseCompressedPoint(member)
		if err != nil {
			return -1, err
		}
		if pointEqual(public, parsed) {
			return i, nil
		}
	}
	return -1, errors.New("private key does not match any ring member")
}

func mustPoint(compressed []byte) *point {
	p, err := parseCompressedPoint(compressed)
	if err != nil {
		panic(err)
	}
	return p
}

// VerifySignature verifies a linkable ring signature. It returns false with a
// nil error for a well-formed signature that does not satisfy the challenge
// chain, and an error for malformed points, non-canonical scalars, duplicate
// or invalid ring members, or a wrong response count.
func verifyRingSignature(ring [][]byte, message, scope []byte, sig *Signature) (bool, error) {
	members, err := validateRing(ring)
	if err != nil {
		return false, err
	}
	if sig == nil {
		return false, errors.New("signature is nil")
	}
	if err := sig.aliasesConsistent(); err != nil {
		return false, err
	}
	c0, err := canonicalScalar(sig.c0Bytes())
	if err != nil {
		return false, fmt.Errorf("C0: %w", err)
	}
	responses := sig.responses()
	if len(responses) != len(members) {
		return false, fmt.Errorf("expected %d responses, got %d", len(members), len(responses))
	}
	image, err := parseKeyImage(sig.keyImageBytes())
	if err != nil {
		return false, fmt.Errorf("key image: %w", err)
	}

	c := new(big.Int).Set(c0)
	for i, member := range members {
		s, err := canonicalScalar(responses[i])
		if err != nil {
			return false, fmt.Errorf("response %d: %w", i, err)
		}
		hp, err := Hp(member, scope)
		if err != nil {
			return false, err
		}
		l := pointMulAdd(s, mustPoint(member), c)
		r := pointAdd(scalarMult(hp, s), scalarMult(image, c))
		c = challenge(scope, members, message, l, r)
	}
	return c.Cmp(c0) == 0, nil
}

// KeyImage computes I = x * Hp(P, scope) for a private key. When ringMember is
// nil the public key is derived from the private key.
func keyImage(privateKey *ecdsa.PrivateKey, ringMember []byte, scope []byte) ([]byte, error) {
	if privateKey == nil || privateKey.D == nil {
		return nil, errors.New("private key is required")
	}
	if !validScalar(privateKey.D) || privateKey.D.Sign() == 0 {
		return nil, fmt.Errorf("%w: private key", errInvalidScalar)
	}
	member := ringMember
	if member == nil {
		member = marshalCompressed(scalarMultBase(privateKey.D))
	} else {
		parsed, err := parseCompressedPoint(member)
		if err != nil {
			return nil, err
		}
		if !pointEqual(parsed, scalarMultBase(privateKey.D)) {
			return nil, errors.New("private key does not match ring member")
		}
	}
	hp, err := Hp(member, scope)
	if err != nil {
		return nil, err
	}
	image := scalarMult(hp, privateKey.D)
	if image.isInfinity() {
		return nil, errIdentityPoint
	}
	return marshalCompressed(image), nil
}

// LinkSignatures reports whether two signatures carry the same key image.
// Key images are derived using the scope, so honest signatures from different
// scopes are not linkable.
func linkSignatures(first, second *Signature) (bool, error) {
	if first == nil || second == nil {
		return false, errors.New("signature is nil")
	}
	if err := first.aliasesConsistent(); err != nil {
		return false, err
	}
	if err := second.aliasesConsistent(); err != nil {
		return false, err
	}
	a, err := parseKeyImage(first.keyImageBytes())
	if err != nil {
		return false, fmt.Errorf("first key image: %w", err)
	}
	b, err := parseKeyImage(second.keyImageBytes())
	if err != nil {
		return false, fmt.Errorf("second key image: %w", err)
	}
	return pointEqual(a, b), nil
}

// VerifyDigest is a small helper for callers that already have a 32-byte
// digest and want the challenge reduced modulo the P-256 order.
func VerifyDigest(digest []byte) *big.Int {
	c := new(big.Int).SetBytes(digest)
	return c.Mod(c, groupOrder)
}
