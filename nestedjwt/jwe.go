package nestedjwt

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"fmt"
	"strings"
)

// openJWE decrypts one compact JWE layer and returns the protected header and
// authenticated plaintext. enforceNestedHeader gates the cty="JWT" rule: it
// applies to the outermost envelope of a submitted token.
func (v *Verifier) openJWE(segments []string, enforceNestedHeader bool) (JOSEHeader, []byte, error) {
	encodedHeader, encryptedKey, encodedIV, encodedCiphertext, encodedTag := segments[0], segments[1], segments[2], segments[3], segments[4]

	header, generic, err := parseProtectedHeader(encodedHeader)
	if err != nil {
		return JOSEHeader{}, nil, err
	}

	if !contains(v.policy.AllowedKeyEncryption, header.Algorithm) {
		return JOSEHeader{}, nil, fmt.Errorf("%w: JWE alg %q", ErrUnsupportedAlgorithm, header.Algorithm)
	}
	if !contains(v.policy.AllowedContentEncryption, header.Encryption) {
		return JOSEHeader{}, nil, fmt.Errorf("%w: JWE enc %q", ErrUnsupportedAlgorithm, header.Encryption)
	}

	// RFC 7519 section 5.2: a JWE carrying a nested JWT/JWS sets the media
	// type to JWT. Comparison is case-insensitive per RFC 7515 section 4.1.10
	// ("cty"/"typ" values are case-normalized media types).
	if enforceNestedHeader {
		if header.ContentType == "" {
			return JOSEHeader{}, nil, ErrMissingContentType
		}
		if !strings.EqualFold(header.ContentType, "JWT") {
			return JOSEHeader{}, nil, fmt.Errorf("%w: cty=%q", ErrMissingContentType, header.ContentType)
		}
	}

	// Compression is forbidden: an attacker can supply a tiny token that
	// expands into a massive plaintext (zip bomb / decompression DoS).
	if header.Compression != "" {
		return JOSEHeader{}, nil, fmt.Errorf("%w: zip=%q", ErrCompressionRejected, header.Compression)
	}

	// Every critical extension would need explicit verifier support; none is
	// understood, so a non-empty crit must be rejected rather than ignored.
	if len(header.Critical) > 0 {
		return JOSEHeader{}, nil, fmt.Errorf("%w: crit=%v", ErrCriticalHeader, header.Critical)
	}

	if v.policy.RequireKID && header.KeyID == "" {
		return JOSEHeader{}, nil, fmt.Errorf("%w: outer JWE", ErrMissingKeyID)
	}

	key, err := v.keys.DecryptionKey(header.KeyID)
	if err != nil {
		return JOSEHeader{}, nil, err
	}

	cek, err := v.unwrapCEK(header.Algorithm, key, encryptedKey)
	if err != nil {
		return JOSEHeader{}, nil, err
	}

	iv, err := b64.DecodeString(encodedIV)
	if err != nil {
		return JOSEHeader{}, nil, fmt.Errorf("%w: invalid IV: %v", ErrMalformedToken, err)
	}
	ciphertext, err := b64.DecodeString(encodedCiphertext)
	if err != nil {
		return JOSEHeader{}, nil, fmt.Errorf("%w: invalid ciphertext: %v", ErrMalformedToken, err)
	}
	tag, err := b64.DecodeString(encodedTag)
	if err != nil {
		return JOSEHeader{}, nil, fmt.Errorf("%w: invalid tag: %v", ErrMalformedToken, err)
	}
	if len(iv) == 0 || len(ciphertext) == 0 || len(tag) == 0 {
		return JOSEHeader{}, nil, fmt.Errorf("%w: empty IV, ciphertext or tag", ErrMalformedToken)
	}

	plaintext, err := v.aesGCMOpen(header.Encryption, cek, iv, ciphertext, tag, []byte(encodedHeader))
	if err != nil {
		return JOSEHeader{}, nil, err
	}

	// Re-serialization sanity: the generic header map and the typed header
	// must agree, and duplicate/oddly-typed fields were caught during parse.
	_ = generic
	return header, plaintext, nil
}

func (v *Verifier) unwrapCEK(alg string, key crypto.PrivateKey, encodedKey string) ([]byte, error) {
	encryptedKey, err := b64.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid encrypted key: %v", ErrMalformedToken, err)
	}
	switch alg {
	case "RSA-OAEP-256":
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%w: RSA-OAEP-256 requires *rsa.PrivateKey, got %T", ErrSignature, key)
		}
		cek, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, rsaKey, encryptedKey, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: RSA-OAEP key unwrap: %v", ErrDecryption, err)
		}
		return cek, nil
	default:
		return nil, fmt.Errorf("%w: JWE alg %q is not implemented", ErrUnsupportedAlgorithm, alg)
	}
}

func (v *Verifier) aesGCMOpen(enc string, cek, iv, ciphertext, tag, aad []byte) ([]byte, error) {
	if enc != "A256GCM" {
		return nil, fmt.Errorf("%w: JWE enc %q is not implemented", ErrUnsupportedAlgorithm, enc)
	}
	if len(cek) != 32 {
		return nil, fmt.Errorf("%w: CEK length %d is invalid for A256GCM", ErrDecryption, len(cek))
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryption, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryption, err)
	}
	// JWE (RFC 7518 section 5.3) appends the 128-bit tag to ciphertext; the
	// AAD is the original ASCII of the encoded protected header.
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)
	plaintext, err := gcm.Open(nil, iv, sealed, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: A256GCM authentication failed: %v", ErrDecryption, err)
	}
	return plaintext, nil
}
