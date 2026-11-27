package dnssec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"hash"
	"math/big"
	"time"
)

// DNSSEC algorithm numbers (RFC 8624 / IANA registry).
const (
	AlgRSASHA1         = 5
	AlgRSASHA256       = 8
	AlgECDSAP256SHA256 = 13
	AlgECDSAP384SHA384 = 14
	AlgED25519         = 15
)

// DS digest types (RFC 4034 section 5.1.3 / RFC 4509).
const (
	DigestSHA1   = 1
	DigestSHA256 = 2
)

// ErrUnsupportedAlgorithm is returned for algorithms this core does not
// implement.
var ErrUnsupportedAlgorithm = errors.New("dnssec: unsupported algorithm")

// KeyTag computes the key tag of a DNSKEY RDATA per RFC 4034 appendix B.
// Algorithm 1 (RSA/MD5, historic) would use a different rule and is rejected
// elsewhere; the standard rule applies to every supported algorithm.
func KeyTag(flags uint16, protocol, algorithm uint8, pub []byte) uint16 {
	buf := make([]byte, 0, 4+len(pub))
	buf = appendU16(buf, flags)
	buf = append(buf, protocol, algorithm)
	buf = append(buf, pub...)

	var acc uint32
	for i, b := range buf {
		if i&1 == 0 {
			acc += uint32(b) << 8
		} else {
			acc += uint32(b)
		}
	}
	acc += acc >> 16
	return uint16(acc & 0xFFFF)
}

// DNSKEYKeyTag is a convenience wrapper around KeyTag for a parsed key.
func DNSKEYKeyTag(k *DNSKEY) uint16 {
	return KeyTag(k.Flags, k.Protocol, k.Algorithm, k.PublicKey)
}

// ComputeDSDigest builds the DS digest over the wire owner name and the
// DNSKEY RDATA as prescribed by RFC 4034 section 5.1.4:
//
//	digest = H(canonical(wire owner name) || DNSKEY RDATA)
func ComputeDSDigest(owner Name, key *DNSKEY, digestType uint8) ([]byte, error) {
	h, err := newDigestHash(digestType)
	if err != nil {
		return nil, err
	}
	h.Write(owner.LowerWire())
	h.Write(appendU16(nil, key.Flags))
	h.Write([]byte{key.Protocol, key.Algorithm})
	h.Write(key.PublicKey)
	return h.Sum(nil), nil
}

func newDigestHash(t uint8) (hash.Hash, error) {
	switch t {
	case DigestSHA1:
		return sha1.New(), nil
	case DigestSHA256:
		return sha256.New(), nil
	default:
		return nil, ErrUnsupportedAlgorithm
	}
}

// VerifyDS validates a parsed DS record against a child-zone DNSKEY: the key
// tag must match and the digest must recompute exactly.
func VerifyDS(ds *DS, childOwner Name, key *DNSKEY) error {
	if ds.KeyTag != DNSKEYKeyTag(key) {
		return ErrBogus("DS key tag does not match DNSKEY")
	}
	if ds.Algorithm != key.Algorithm {
		return ErrBogus("DS algorithm field does not match DNSKEY")
	}
	digest, err := ComputeDSDigest(childOwner, key, ds.DigestType)
	if err != nil {
		return err
	}
	if !constantTimeEqual(digest, ds.Digest) {
		return ErrBogus("DS digest mismatch")
	}
	return nil
}

func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// VerifySignature performs the cryptographic signature check for a signed
// blob using a DNSKEY. ECDSA P-256/SHA-256 (13) and P-384/SHA-384 (14) use
// the raw concatenated R||S signature encoding of RFC 6605 section 4, which
// is converted to the ASN.1 form consumed by the Go standard library.
func VerifySignature(algorithm uint8, pub, signed, signature []byte) error {
	switch algorithm {
	case AlgECDSAP256SHA256, AlgECDSAP384SHA384:
		curve := elliptic.P256()
		hasher := sha256.New
		coordLen := 32
		if algorithm == AlgECDSAP384SHA384 {
			curve = elliptic.P384()
			hasher = sha512.New384
			coordLen = 48
		}
		if len(pub) != 2*coordLen {
			return ErrBogus("ECDSA public key has invalid length")
		}
		if len(signature) != 2*coordLen {
			return ErrBogus("ECDSA signature has invalid length")
		}
		x := new(big.Int).SetBytes(pub[:coordLen])
		y := new(big.Int).SetBytes(pub[coordLen:])
		publicKey := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}

		h := hasher()
		h.Write(signed)
		digest := h.Sum(nil)

		r := new(big.Int).SetBytes(signature[:coordLen])
		s := new(big.Int).SetBytes(signature[coordLen:])
		if !ecdsa.Verify(publicKey, digest, r, s) {
			return ErrBogus("ECDSA signature verification failed")
		}
		return nil
	default:
		return ErrUnsupportedAlgorithm
	}
}

// SerialCompare implements the 32-bit serial number ordering of RFC 1982.
// It returns -1 if i1 is earlier than i2, 1 if later, and 0 if equal.
func SerialCompare(i1, i2 uint32) int {
	if i1 == i2 {
		return 0
	}
	// Forward modular distance from i1 to i2 in the 32-bit space. A distance
	// in (0, 2^31) means i2 is in i1's serial future; larger distances mean
	// i2 is in the past. Arithmetic stays in uint32 to avoid signed overflow.
	const half = uint32(1 << 31)
	d := i2 - i1
	if d > 0 && d < half {
		return -1
	}
	return 1
}

// InValidityWindow checks inception <= now <= expiration using RFC 1982
// serial arithmetic so wraparound signatures validate correctly. skew widens
// the window on both sides to tolerate resolver/authority clock drift.
//
// The expiration must be in the strict serial future of the inception for a
// well-formed signature; "now" must then not be earlier than inception and
// not be later than expiration, which holds across a 2^32 wrap.
func InValidityWindow(inception, expiration, now uint32, skew time.Duration) bool {
	skewSecs := uint32(skew / time.Second)
	start := inception - skewSecs
	end := expiration + skewSecs

	// Reject a degenerate window whose end is not in the future of its start.
	if SerialCompare(end, start) <= 0 {
		return false
	}
	// start <= now (now must not be before start)
	if SerialCompare(now, start) < 0 {
		return false
	}
	// now <= end (now must not be after end)
	if SerialCompare(end, now) < 0 {
		return false
	}
	return true
}

// nowUnixSeconds is a small helper for converting time to the 32-bit form
// used inside RRSIG inception/expiration fields.
func nowUnixSeconds(t time.Time) uint32 {
	return uint32(t.UTC().Unix())
}
