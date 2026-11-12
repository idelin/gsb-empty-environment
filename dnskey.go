package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"errors"
	"hash"
	"math/big"
)

// DNSKEY flags (RFC 4034, section 2.1.1).
const (
	FlagZoneKey = uint16(0x0100) // 256
	FlagSEP     = uint16(0x0001) // 257
	FlagRevoke  = uint16(0x0080) // 128
)

// DNSKEY is a DNSSEC public key record.
type DNSKEY struct {
	Flags     uint16
	Protocol  uint8
	Algorithm uint8
	// PublicKey is the parsed public key: *rsa.PublicKey, *ecdsa.PublicKey
	// or ed25519.PublicKey.
	PublicKey crypto.PublicKey
}

// ErrUnsupportedAlgorithm is returned when an algorithm has no verifier.
var ErrUnsupportedAlgorithm = errors.New("netsec: unsupported DNSKEY algorithm")

// KeyTag computes the RFC 4034, Appendix B key tag for the key using the
// RDATA form (flags, protocol, algorithm, public key octets).
func (k DNSKEY) KeyTag() (uint16, error) {
	wire, err := k.rdata()
	if err != nil {
		return 0, err
	}
	acme := uint32(0)
	for i, b := range wire {
		if i%2 == 0 {
			acme += uint32(b) << 8
		} else {
			acme += uint32(b)
		}
	}
	acme += acme >> 16
	return uint16(acme & 0xffff), nil
}

// rdata renders the DNSKEY RDATA used for key-tag computation.
func (k DNSKEY) rdata() ([]byte, error) {
	pub, err := encodePublicKey(k.Algorithm, k.PublicKey)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, 4+len(pub))
	out = append(out, byte(k.Flags>>8), byte(k.Flags))
	out = append(out, k.Protocol, k.Algorithm)
	out = append(out, pub...)
	return out, nil
}

// encodePublicKey serializes a parsed public key into the DNSKEY public-key
// wire encoding for the given algorithm.
func encodePublicKey(alg uint8, pub crypto.PublicKey) ([]byte, error) {
	switch alg {
	case AlgRSASHA1, AlgRSASHA256, AlgRSASHA512:
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("netsec: RSA algorithm requires *rsa.PublicKey")
		}
		return encodeRSAPublicKey(rsaKey), nil
	case AlgECDSAP256, AlgECDSAP384:
		ecKey, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return nil, errors.New("netsec: ECDSA algorithm requires *ecdsa.PublicKey")
		}
		return encodeECDSAPublicKey(ecKey), nil
	case AlgEd25519:
		edKey, ok := pub.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("netsec: Ed25519 algorithm requires ed25519.PublicKey")
		}
		return []byte(edKey), nil
	default:
		return nil, ErrUnsupportedAlgorithm
	}
}

// encodeRSAPublicKey implements the RSA public-key wire format of RFC 3110,
// section 2 (exponent-length prefix, exponent, modulus).
func encodeRSAPublicKey(k *rsa.PublicKey) []byte {
	exponent := k.E
	var expLen int
	switch {
	case exponent > 0xffffff:
		expLen = 4
	case exponent > 0xffff:
		expLen = 3
	case exponent > 0xff:
		expLen = 2
	default:
		expLen = 1
	}
	modulus := k.N.Bytes()
	// Strip a leading zero byte that would look like a negative indicator.
	for len(modulus) > 1 && modulus[0] == 0 {
		modulus = modulus[1:]
	}

	out := make([]byte, 0, 1+expLen+len(modulus))
	if expLen > 255 {
		out = append(out, 0, byte(expLen>>8), byte(expLen))
	} else {
		out = append(out, byte(expLen))
	}
	for i := expLen - 1; i >= 0; i-- {
		out = append(out, byte(exponent>>(8*i)))
	}
	out = append(out, modulus...)
	return out
}

// encodeECDSAPublicKey returns the fixed-size concatenation of the padded X and
// Y coordinates (RFC 6605, section 4).
func encodeECDSAPublicKey(k *ecdsa.PublicKey) []byte {
	size := (k.Params().BitSize + 7) / 8
	out := make([]byte, 2*size)
	x := k.X.Bytes()
	y := k.Y.Bytes()
	copy(out[size-len(x):size], x)
	copy(out[2*size-len(y):2*size], y)
	return out
}

func hashForAlgorithm(alg uint8) (crypto.Hash, func() hash.Hash, error) {
	switch alg {
	case AlgRSASHA256, AlgECDSAP256:
		return crypto.SHA256, sha256.New, nil
	case AlgRSASHA512, AlgECDSAP384:
		return crypto.SHA384, sha512.New384, nil
	case AlgEd25519:
		return 0, nil, nil
	default:
		return 0, nil, ErrUnsupportedAlgorithm
	}
}

// ecdsaSignature mirrors the two INTEGER DER structure used by ECDSA.
type ecdsaSignature struct {
	R, S *big.Int
}

// verifySignature checks a raw DNSSEC signature against the already-hashed
// signed data (or the raw message for Ed25519).
func verifySignature(alg uint8, pub crypto.PublicKey, digest, signature []byte) error {
	switch alg {
	case AlgRSASHA256, AlgRSASHA512:
		rsaKey, ok := pub.(*rsa.PublicKey)
		if !ok {
			return errors.New("netsec: RSA algorithm requires *rsa.PublicKey")
		}
		hashID, _, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		if err := rsa.VerifyPKCS1v15(rsaKey, hashID, digest, signature); err != nil {
			return err
		}
		return nil

	case AlgECDSAP256, AlgECDSAP384:
		ecKey, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("netsec: ECDSA algorithm requires *ecdsa.PublicKey")
		}
		var der ecdsaSignature
		if _, err := asn1.Unmarshal(signature, &der); err != nil {
			return err
		}
		if der.R == nil || der.S == nil {
			return errors.New("netsec: invalid ECDSA signature")
		}
		if !ecdsa.Verify(ecKey, digest, der.R, der.S) {
			return errors.New("netsec: ECDSA verification failed")
		}
		return nil

	case AlgEd25519:
		edKey, ok := pub.(ed25519.PublicKey)
		if !ok {
			return errors.New("netsec: Ed25519 algorithm requires ed25519.PublicKey")
		}
		if !ed25519.Verify(edKey, digest, signature) {
			return errors.New("netsec: Ed25519 verification failed")
		}
		return nil

	default:
		return ErrUnsupportedAlgorithm
	}
}
