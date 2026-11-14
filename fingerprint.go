package netsec

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"unicode"
)

const (
	// Full fingerprints are either 160-bit (40 hexadecimal digits, as used by
	// OpenPGP v4 keys) or 256-bit (64 hexadecimal digits).
	fingerprintLenSHA1   = 40
	fingerprintLenSHA256 = 64
	shortIDLen           = 8
)

// NormalizeFingerprint strips the separators commonly used when displaying
// fingerprints (spaces, tabs, colons and a leading 0x), then validates that
// the result is a complete 40- or 64-digit hexadecimal fingerprint. The result
// is upper case.
func NormalizeFingerprint(fingerprint string) (string, error) {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r == ':' || r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '-':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(fingerprint))
	if len(clean) >= 2 && (strings.HasPrefix(clean, "0x") || strings.HasPrefix(clean, "0X")) {
		clean = clean[2:]
	}
	if len(clean) != fingerprintLenSHA1 && len(clean) != fingerprintLenSHA256 {
		return "", fmt.Errorf("%w: got %d digits", ErrInvalidFingerprint, len(clean))
	}
	for _, r := range clean {
		if !unicode.Is(unicode.Hex_Digit, r) {
			return "", fmt.Errorf("%w: non-hexadecimal digit %q", ErrInvalidFingerprint, r)
		}
	}
	return strings.ToUpper(clean), nil
}

// NormalizeShortID canonicalizes a short identifier hint. A short identifier
// is only a locally generated hint; it is deliberately not interpreted as a
// full fingerprint. Empty values are returned unchanged.
func NormalizeShortID(shortID string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r), r == ':', r == '-':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(shortID))
	if len(clean) >= 2 && (strings.HasPrefix(clean, "0x") || strings.HasPrefix(clean, "0X")) {
		clean = clean[2:]
	}
	return strings.ToUpper(clean)
}

// DerivedShortID returns the conventional 8-digit short identifier: the
// trailing 64 bits of the full fingerprint. It is useful as a display hint and
// for collision detection, never as an authorization token.
func DerivedShortID(fingerprint string) (string, error) {
	normalized, err := NormalizeFingerprint(fingerprint)
	if err != nil {
		return "", err
	}
	return normalized[len(normalized)-shortIDLen:], nil
}

// Fingerprint returns the preferred full fingerprint of a public key:
// uppercase hexadecimal SHA-256 over the key's PKIX (DER) encoding.
//
// A KeyRecord may also carry the SHA-1 form of the same encoding, which is the
// traditional 40-digit full fingerprint. Both forms are derived from the
// supplied key, so a caller cannot substitute a key while retaining somebody
// else's fingerprint.
func Fingerprint(publicKey any) (string, error) {
	der, err := CanonicalPublicKey(publicKey)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(der)
	return strings.ToUpper(hex.EncodeToString(digest[:])), nil
}

// FingerprintSet returns every supported complete fingerprint form for a
// public key. It is used during validation to verify that the claimed
// fingerprint was really derived from the key material in the record.
func FingerprintSet(publicKey any) (map[string]struct{}, error) {
	key, der, err := normalizePublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, 4)
	addDigest := func(digest []byte) {
		set[strings.ToUpper(hex.EncodeToString(digest))] = struct{}{}
	}
	sha256Digest := sha256.Sum256(der)
	sha1Digest := sha1.Sum(der)
	addDigest(sha256Digest[:])
	addDigest(sha1Digest[:])

	// Accept the common convenience form for Ed25519, where the raw 32-byte
	// key (rather than a PKIX wrapper) is hashed.
	if edKey, ok := key.(ed25519.PublicKey); ok {
		rawSHA256 := sha256.Sum256(edKey)
		rawSHA1 := sha1.Sum(edKey)
		addDigest(rawSHA256[:])
		addDigest(rawSHA1[:])
	}
	return set, nil
}

// CanonicalPublicKey returns the deterministic PKIX DER representation of the
// supplied public key. It accepts the standard library key types, PKIX DER
// bytes, PEM blocks and raw Ed25519 public keys.
func CanonicalPublicKey(publicKey any) ([]byte, error) {
	_, der, err := normalizePublicKey(publicKey)
	return der, err
}

// PublicKeysEqual reports whether two public keys have the same canonical
// encoding.
func PublicKeysEqual(a, b any) bool {
	derA, errA := CanonicalPublicKey(a)
	derB, errB := CanonicalPublicKey(b)
	if errA != nil || errB != nil {
		return false
	}
	if len(derA) != len(derB) {
		return false
	}
	for i := range derA {
		if derA[i] != derB[i] {
			return false
		}
	}
	return true
}

func normalizePublicKey(publicKey any) (any, []byte, error) {
	var key any
	switch typed := publicKey.(type) {
	case nil:
		return nil, nil, fmt.Errorf("%w: nil", ErrInvalidPublicKey)
	case ed25519.PublicKey:
		if len(typed) != ed25519.PublicKeySize {
			return nil, nil, fmt.Errorf("%w: wrong Ed25519 key size", ErrInvalidPublicKey)
		}
		key = append(ed25519.PublicKey(nil), typed...)
	case *ed25519.PublicKey:
		if typed == nil || len(*typed) != ed25519.PublicKeySize {
			return nil, nil, fmt.Errorf("%w: wrong Ed25519 key size", ErrInvalidPublicKey)
		}
		key = append(ed25519.PublicKey(nil), (*typed)...)
	case *rsa.PublicKey:
		if typed == nil {
			return nil, nil, fmt.Errorf("%w: nil RSA key", ErrInvalidPublicKey)
		}
		key = typed
	case *ecdsa.PublicKey:
		if typed == nil {
			return nil, nil, fmt.Errorf("%w: nil ECDSA key", ErrInvalidPublicKey)
		}
		key = typed
	case *ecdh.PublicKey:
		if typed == nil {
			return nil, nil, fmt.Errorf("%w: nil ECDH key", ErrInvalidPublicKey)
		}
		key = typed
	case *x509.Certificate:
		if typed == nil || typed.PublicKey == nil {
			return nil, nil, fmt.Errorf("%w: empty certificate", ErrInvalidPublicKey)
		}
		return normalizePublicKey(typed.PublicKey)
	case []byte:
		if parsed, err := x509.ParsePKIXPublicKey(typed); err == nil {
			return normalizePublicKey(parsed)
		}
		if len(typed) == ed25519.PublicKeySize {
			key = append(ed25519.PublicKey(nil), typed...)
			break
		}
		return nil, nil, fmt.Errorf("%w: cannot parse PKIX bytes", ErrInvalidPublicKey)
	case string:
		if block, _ := pem.Decode([]byte(typed)); block != nil {
			parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
			}
			return normalizePublicKey(parsed)
		}
		if parsed, err := x509.ParsePKIXPublicKey([]byte(typed)); err == nil {
			return normalizePublicKey(parsed)
		}
		if raw, err := hex.DecodeString(typed); err == nil && len(raw) == ed25519.PublicKeySize {
			return normalizePublicKey(ed25519.PublicKey(raw))
		}
		return nil, nil, fmt.Errorf("%w: unrecognized key encoding", ErrInvalidPublicKey)
	default:
		key = publicKey
	}

	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
	}
	return key, der, nil
}
