package netsec

import (
	"bytes"
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/big"
)

var defaultRand io.Reader = rand.Reader

// ErrInvalidPoint is returned for any P-256 point that is not a canonical,
// finite point on the curve.
var ErrInvalidPoint = errors.New("netsec: invalid P-256 point")

// ParseP256Point accepts a canonical 33-byte compressed SEC1 encoding or a
// canonical 65-byte uncompressed encoding and returns the finite point plus its
// canonical compressed encoding.
func ParseP256Point(encoded []byte) (x, y *big.Int, canonical []byte, err error) {
	curve := elliptic.P256()
	switch len(encoded) {
	case 33:
		x, y = elliptic.UnmarshalCompressed(curve, encoded)
		if x == nil || y == nil {
			return nil, nil, nil, fmt.Errorf("%w: malformed compressed encoding", ErrInvalidPoint)
		}
		canonical = elliptic.MarshalCompressed(curve, x, y)
		if !bytes.Equal(canonical, encoded) {
			return nil, nil, nil, fmt.Errorf("%w: non-canonical compressed encoding", ErrInvalidPoint)
		}
	case 65:
		x, y = elliptic.Unmarshal(curve, encoded)
		if x == nil || y == nil {
			return nil, nil, nil, fmt.Errorf("%w: malformed uncompressed encoding", ErrInvalidPoint)
		}
		canonical = elliptic.MarshalCompressed(curve, x, y)
	default:
		return nil, nil, nil, fmt.Errorf("%w: length %d", ErrInvalidPoint, len(encoded))
	}
	if !curve.IsOnCurve(x, y) {
		return nil, nil, nil, fmt.Errorf("%w: point is not on P-256", ErrInvalidPoint)
	}
	if x.Sign() == 0 && y.Sign() == 0 {
		return nil, nil, nil, fmt.Errorf("%w: point at infinity", ErrInvalidPoint)
	}
	return new(big.Int).Set(x), new(big.Int).Set(y), canonical, nil
}

// EncodeCompressedPoint returns the canonical 33-byte compressed encoding of a
// valid P-256 point.
func EncodeCompressedPoint(x, y *big.Int) ([]byte, error) {
	curve := elliptic.P256()
	if x == nil || y == nil || !curve.IsOnCurve(x, y) || (x.Sign() == 0 && y.Sign() == 0) {
		return nil, ErrInvalidPoint
	}
	return elliptic.MarshalCompressed(curve, x, y), nil
}

// EphemeralKey is a P-256 ephemeral key pair. The private scalar is never put in
// a handshake message; only PublicBytes is sent.
type EphemeralKey struct {
	private *ecdh.PrivateKey
	scalar  []byte
	public  []byte
}

// GenerateEphemeral creates a fresh P-256 ephemeral key pair.
func GenerateEphemeral(random io.Reader) (*EphemeralKey, error) {
	if random == nil {
		random = defaultRand
	}
	private, err := ecdh.P256().GenerateKey(random)
	if err != nil {
		return nil, err
	}
	return newEphemeral(private)
}

// EphemeralFromScalar builds a deterministic ephemeral key for tests or a host
// that has its own validated scalar source. A scalar of 1..32 bytes is accepted
// and left-padded; zero and values >= the P-256 group order are rejected.
func EphemeralFromScalar(scalar []byte) (*EphemeralKey, error) {
	if len(scalar) == 0 || len(scalar) > 32 {
		return nil, fmt.Errorf("%w: scalar length %d", ErrInvalidPrivateKey, len(scalar))
	}
	padded := make([]byte, 32)
	copy(padded[32-len(scalar):], scalar)
	value := new(big.Int).SetBytes(padded)
	n := elliptic.P256().Params().N
	if value.Sign() == 0 || value.Cmp(n) >= 0 {
		return nil, fmt.Errorf("%w: scalar out of range", ErrInvalidPrivateKey)
	}
	private, err := ecdh.P256().NewPrivateKey(padded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPrivateKey, err)
	}
	return newEphemeral(private)
}

func newEphemeral(private *ecdh.PrivateKey) (*EphemeralKey, error) {
	scalar := private.Bytes()
	uncompressed := private.PublicKey().Bytes()
	_, _, canonical, err := ParseP256Point(uncompressed)
	if err != nil {
		return nil, err
	}
	return &EphemeralKey{
		private: private,
		scalar:  append([]byte(nil), scalar...),
		public:  canonical,
	}, nil
}

// PublicBytes returns a copy of the canonical compressed public point.
func (e *EphemeralKey) PublicBytes() []byte {
	if e == nil {
		return nil
	}
	return append([]byte(nil), e.public...)
}

// Scalar returns a copy of the private scalar. It is provided only for test
// fixtures and must not be put on the wire.
func (e *EphemeralKey) Scalar() []byte {
	if e == nil {
		return nil
	}
	return append([]byte(nil), e.scalar...)
}

// Matches reports whether encoded is the same point as the public key.
func (e *EphemeralKey) Matches(encoded []byte) bool {
	if e == nil {
		return false
	}
	_, _, canonical, err := ParseP256Point(encoded)
	return err == nil && bytes.Equal(canonical, e.public)
}

// SharedPoint validates the peer point, computes xyG, and returns its canonical
// compressed encoding. The crypto/ecdh result is cross-checked against the
// curve computation so a malformed or inconsistent point cannot slip through.
func (e *EphemeralKey) SharedPoint(peerEncoded []byte) ([]byte, error) {
	if e == nil || e.private == nil {
		return nil, errors.New("netsec: nil ephemeral key")
	}
	peerX, peerY, canonical, err := ParseP256Point(peerEncoded)
	if err != nil {
		return nil, err
	}
	curve := elliptic.P256()
	sharedX, sharedY := curve.ScalarMult(peerX, peerY, e.scalar)
	if sharedX == nil || sharedY == nil {
		return nil, fmt.Errorf("%w: ECDH produced the point at infinity", ErrInvalidPoint)
	}

	uncompressedPeer := elliptic.Marshal(curve, peerX, peerY)
	peerPublic, err := ecdh.P256().NewPublicKey(uncompressedPeer)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPoint, err)
	}
	sharedSecret, err := e.private.ECDH(peerPublic)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPoint, err)
	}
	if !bytes.Equal(sharedSecret, sharedX.FillBytes(make([]byte, 32))) {
		return nil, errors.New("netsec: inconsistent ECDH result")
	}
	output := elliptic.MarshalCompressed(curve, sharedX, sharedY)
	if !bytes.Equal(elliptic.MarshalCompressed(curve, peerX, peerY), canonical) {
		return nil, ErrInvalidPoint
	}
	return output, nil
}

// SamePointRule applies the X == Y special case. The caller must already hold a
// valid local ephemeral private key for localPublic. Equality is therefore
// accepted only when the peer point is a valid curve point and equals the local
// public point. Remote possession of the matching private scalar is still
// proven only by the confirmation MAC before traffic keys are released.
func SamePointRule(localPublic, peerEncoded []byte) error {
	_, _, peerCanonical, err := ParseP256Point(peerEncoded)
	if err != nil {
		return err
	}
	_, _, localCanonical, err := ParseP256Point(localPublic)
	if err != nil {
		return err
	}
	if !bytes.Equal(peerCanonical, localCanonical) {
		return fmt.Errorf("%w: X != Y", ErrInvalidPoint)
	}
	return nil
}

// deterministicScalar derives a valid non-zero P-256 scalar from a seed. It is
// used only by the deterministic attack-search model and tests.
func deterministicScalar(seed []byte) []byte {
	digest := sha256.Sum256(seed)
	n := elliptic.P256().Params().N
	value := new(big.Int).SetBytes(digest[:])
	value.Mod(value, new(big.Int).Sub(n, big.NewInt(1)))
	value.Add(value, big.NewInt(1))
	return value.FillBytes(make([]byte, 32))
}
