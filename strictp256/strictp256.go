// Package strictp256 implements a hardened P-256 ECDSA verification entry point
// for a transaction ingress host.
//
// The plain Go standard library treats (r, s) and (r, n-s) as two distinct but
// equally valid signatures over the same message. An ingress that derives a
// request identifier directly from the raw signature bytes can therefore be
// made to register the same authorization twice by flipping s to n-s. This
// package closes that gap in two complementary ways:
//
//   - Verify only accepts a single canonical signature form: an ASN.1 DER
//     SEQUENCE of exactly two INTEGERs, in shortest-form DER, positive, without
//     superfluous leading zeros or trailing bytes, with 1 <= r, s < n and the
//     low-S rule s <= n/2 enforced before the cryptographic check.
//   - RequestID is derived solely from the request content (a fixed domain
//     separator, the canonical compressed public key and the message), never
//     from signature bytes, so both S variants of one authorization map to the
//     same identifier.
package strictp256

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"errors"
	"math/big"
)

// DomainLabel is the fixed, protocol-specific prefix mixed into every request
// identifier so that hashes from different contexts can never collide.
const DomainLabel = "strictp256/v1:transaction-ingress"

var (
	// ErrMalformedDER is returned when a signature is not a shortest-form DER
	// SEQUENCE containing exactly two INTEGERs.
	ErrMalformedDER = errors.New("strictp256: malformed or non-canonical DER signature")
	// ErrIntegerRange is returned when either scalar is outside [1, n-1].
	ErrIntegerRange = errors.New("strictp256: r and s must satisfy 1 <= r,s < n")
	// ErrHighS is returned when s is not in low-S form (s > n/2).
	ErrHighS = errors.New("strictp256: signature is not in low-S form (s > n/2)")
	// ErrInvalidPublicKey is returned for a nil key or a key not on P-256.
	ErrInvalidPublicKey = errors.New("strictp256: invalid or non-P256 public key")
	// ErrVerification is returned when the signature does not verify.
	ErrVerification = errors.New("strictp256: signature verification failed")
	// ErrRevoked is returned when the (public key, request ID) pair is revoked.
	ErrRevoked = errors.New("strictp256: request has been revoked")
)

// tagInteger and tagSequence are the relevant ASN.1 universal tags.
const (
	tagInteger  = 0x02
	tagSequence = 0x30
)

// VerifyStrict parses and verifies a P-256 signature using the strict rules
// documented on the package. msg is the raw message; it is hashed with SHA-256
// internally. Any failure returns one of the package's sentinel errors.
func VerifyStrict(pub *ecdsa.PublicKey, msg, sig []byte) error {
	if err := validatePublicKey(pub); err != nil {
		return err
	}
	r, s, err := parseSignature(sig)
	if err != nil {
		return err
	}
	if err := enforceScalarBounds(r, s, pub.Curve.Params().N); err != nil {
		return err
	}
	digest := sha256.Sum256(msg)
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return ErrVerification
	}
	return nil
}

// DigestMessage exposes the exact message preprocessing used by VerifyStrict so
// callers and tests stay aligned with the host's hashing convention.
func DigestMessage(msg []byte) []byte {
	digest := sha256.Sum256(msg)
	return digest[:]
}

// validatePublicKey rejects nil keys, keys on another curve and points that do
// not lie on P-256.
func validatePublicKey(pub *ecdsa.PublicKey) error {
	if pub == nil || pub.Curve == nil || pub.X == nil || pub.Y == nil {
		return ErrInvalidPublicKey
	}
	params := pub.Curve.Params()
	p256 := elliptic.P256().Params()
	if params.Name != p256.Name || params.BitSize != p256.BitSize ||
		params.N.Cmp(p256.N) != 0 || params.P.Cmp(p256.P) != 0 {
		return ErrInvalidPublicKey
	}
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return ErrInvalidPublicKey
	}
	return nil
}

// parseSignature enforces the exact wire format: one DER SEQUENCE whose entire
// byte range is consumed, containing exactly two shortest-form positive
// INTEGERs.
func parseSignature(sig []byte) (*big.Int, *big.Int, error) {
	outer, content, rest, err := readTLV(sig)
	if err != nil {
		return nil, nil, err
	}
	if outer.Tag != tagSequence || len(rest) != 0 {
		return nil, nil, ErrMalformedDER
	}

	first, firstContent, afterFirst, err := readTLV(content)
	if err != nil {
		return nil, nil, err
	}
	if first.Tag != tagInteger {
		return nil, nil, ErrMalformedDER
	}
	r, err := parseInteger(firstContent)
	if err != nil {
		return nil, nil, err
	}

	second, secondContent, afterSecond, err := readTLV(afterFirst)
	if err != nil {
		return nil, nil, err
	}
	if second.Tag != tagInteger {
		return nil, nil, ErrMalformedDER
	}
	s, err := parseInteger(secondContent)
	if err != nil {
		return nil, nil, err
	}
	if len(afterSecond) != 0 {
		return nil, nil, ErrMalformedDER
	}
	return r, s, nil
}

// parsedTLV is a decoded tag-length-value triplet.
type parsedTLV struct {
	Tag     byte
	Content []byte
}

// readTLV parses a single DER TLV, rejecting indefinite length, non-minimal
// length encodings and truncated input.
func readTLV(in []byte) (t parsedTLV, content []byte, rest []byte, err error) {
	if len(in) < 2 {
		return parsedTLV{}, nil, nil, ErrMalformedDER
	}
	tag := in[0]
	idx := 1

	length := int(in[idx])
	idx++
	if length&0x80 != 0 {
		numBytes := length & 0x7f
		if numBytes == 0 {
			// 0x80: indefinite length, forbidden in DER.
			return parsedTLV{}, nil, nil, ErrMalformedDER
		}
		if numBytes > 4 || idx+numBytes >= len(in) {
			return parsedTLV{}, nil, nil, ErrMalformedDER
		}
		first := in[idx]
		// Long form must be minimal and must not start with a zero octet.
		if first == 0 || (numBytes == 1 && first < 0x80) {
			return parsedTLV{}, nil, nil, ErrMalformedDER
		}
		length = 0
		for i := 0; i < numBytes; i++ {
			length = (length << 8) | int(in[idx+i])
		}
		idx += numBytes
	}

	if idx+length > len(in) {
		return parsedTLV{}, nil, nil, ErrMalformedDER
	}
	return parsedTLV{Tag: tag}, in[idx : idx+length], in[idx+length:], nil
}

// parseInteger decodes an INTEGER content payload, requiring a positive value
// in the shortest possible DER encoding (no unnecessary leading zero octet, no
// sign bit set, non-empty).
func parseInteger(payload []byte) (*big.Int, error) {
	if len(payload) == 0 {
		return nil, ErrMalformedDER
	}
	if payload[0]&0x80 != 0 {
		// Negative INTEGER (or sign bit set): rejected.
		return nil, ErrMalformedDER
	}
	if len(payload) > 1 && payload[0] == 0x00 && payload[1]&0x80 == 0 {
		// Leading 0x00 that is not required to clear the sign bit.
		return nil, ErrMalformedDER
	}
	return new(big.Int).SetBytes(payload), nil
}

// enforceScalarBounds checks 1 <= r, s < n and the low-S requirement s <= n/2.
func enforceScalarBounds(r, s, n *big.Int) error {
	if r.Sign() == 0 || s.Sign() == 0 || r.Cmp(n) >= 0 || s.Cmp(n) >= 0 {
		return ErrIntegerRange
	}
	halfN := new(big.Int).Rsh(n, 1)
	if s.Cmp(halfN) > 0 {
		return ErrHighS
	}
	return nil
}
