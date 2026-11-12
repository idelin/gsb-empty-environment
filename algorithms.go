package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"fmt"
)

// Algorithm is an RFC 9421 signature algorithm identifier bound to a key in
// the registry. The alg signature parameter never selects an algorithm.
type Algorithm string

const (
	AlgorithmEd25519         Algorithm = "ed25519"
	AlgorithmHMACSHA256      Algorithm = "hmac-sha256"
	AlgorithmECDSAP256SHA256 Algorithm = "ecdsa-p256-sha256"
	AlgorithmECDSAP384SHA384 Algorithm = "ecdsa-p384-sha384"
	AlgorithmRSAPSSSHA512    Algorithm = "rsa-pss-sha512"
	AlgorithmRSAPSSSHA256    Algorithm = "rsa-pss-sha256"
	AlgorithmRSAV15SHA256    Algorithm = "rsa-v1_5-sha256"
)

func validateKey(alg Algorithm, key any) error {
	switch alg {
	case AlgorithmEd25519:
		publicKey, ok := key.(ed25519.PublicKey)
		if !ok || len(publicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: ed25519 requires an ed25519.PublicKey", ErrInvalidKey)
		}
	case AlgorithmHMACSHA256:
		secret, ok := key.([]byte)
		if !ok || len(secret) == 0 {
			return fmt.Errorf("%w: hmac-sha256 requires a non-empty []byte secret", ErrInvalidKey)
		}
	case AlgorithmECDSAP256SHA256, AlgorithmECDSAP384SHA384:
		publicKey, ok := key.(*ecdsa.PublicKey)
		if !ok || publicKey == nil || publicKey.Curve == nil {
			return fmt.Errorf("%w: %s requires an *ecdsa.PublicKey", ErrInvalidKey, alg)
		}
		if alg == AlgorithmECDSAP256SHA256 && publicKey.Curve != elliptic.P256() {
			return fmt.Errorf("%w: %s requires the P-256 curve", ErrInvalidKey, alg)
		}
		if alg == AlgorithmECDSAP384SHA384 && publicKey.Curve != elliptic.P384() {
			return fmt.Errorf("%w: %s requires the P-384 curve", ErrInvalidKey, alg)
		}
	case AlgorithmRSAPSSSHA512, AlgorithmRSAPSSSHA256, AlgorithmRSAV15SHA256:
		publicKey, ok := key.(*rsa.PublicKey)
		if !ok || publicKey == nil || publicKey.N == nil || publicKey.E < 2 {
			return fmt.Errorf("%w: %s requires an *rsa.PublicKey", ErrInvalidKey, alg)
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
	return nil
}

func verifySignature(alg Algorithm, key any, signatureBase, signature []byte) error {
	switch alg {
	case AlgorithmEd25519:
		publicKey, ok := key.(ed25519.PublicKey)
		if !ok || len(publicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: ed25519 key type mismatch", ErrInvalidKey)
		}
		if !ed25519.Verify(publicKey, signatureBase, signature) {
			return ErrInvalidSignature
		}
		return nil

	case AlgorithmHMACSHA256:
		secret, ok := key.([]byte)
		if !ok || len(secret) == 0 {
			return fmt.Errorf("%w: hmac-sha256 key type mismatch", ErrInvalidKey)
		}
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(signatureBase)
		expected := mac.Sum(nil)
		if subtle.ConstantTimeCompare(expected, signature) != 1 {
			return ErrInvalidSignature
		}
		return nil

	case AlgorithmECDSAP256SHA256, AlgorithmECDSAP384SHA384:
		publicKey, ok := key.(*ecdsa.PublicKey)
		if !ok || publicKey == nil {
			return fmt.Errorf("%w: %s key type mismatch", ErrInvalidKey, alg)
		}
		var digest []byte
		if alg == AlgorithmECDSAP256SHA256 {
			sum := sha256.Sum256(signatureBase)
			digest = sum[:]
		} else {
			sum := sha512.Sum384(signatureBase)
			digest = sum[:]
		}
		if !ecdsa.VerifyASN1(publicKey, digest, signature) {
			return ErrInvalidSignature
		}
		return nil

	case AlgorithmRSAPSSSHA512, AlgorithmRSAPSSSHA256:
		publicKey, ok := key.(*rsa.PublicKey)
		if !ok || publicKey == nil {
			return fmt.Errorf("%w: %s key type mismatch", ErrInvalidKey, alg)
		}
		hash := crypto.SHA512
		var digest []byte
		if alg == AlgorithmRSAPSSSHA512 {
			sum := sha512.Sum512(signatureBase)
			digest = sum[:]
		} else {
			hash = crypto.SHA256
			sum := sha256.Sum256(signatureBase)
			digest = sum[:]
		}
		opts := &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hash,
		}
		if err := rsa.VerifyPSS(publicKey, hash, digest, signature, opts); err != nil {
			return ErrInvalidSignature
		}
		return nil

	case AlgorithmRSAV15SHA256:
		publicKey, ok := key.(*rsa.PublicKey)
		if !ok || publicKey == nil {
			return fmt.Errorf("%w: %s key type mismatch", ErrInvalidKey, alg)
		}
		sum := sha256.Sum256(signatureBase)
		if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, sum[:], signature); err != nil {
			return ErrInvalidSignature
		}
		return nil

	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
}
