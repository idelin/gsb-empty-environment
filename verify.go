package netsec

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math/big"
)

const minimumPublicExponent = 65537

var (
	// ErrInvalidPublicKey reports an RSA public key that is unsafe or unusable
	// for PKCS#1 v1.5 verification.
	ErrInvalidPublicKey = errors.New("netsec: invalid RSA public key")
	// ErrInvalidDigest reports a digest whose length is not SHA-256's length.
	ErrInvalidDigest = errors.New("netsec: digest must be a SHA-256 digest")
	// ErrUnsupportedHash reports a hash other than SHA-256.
	ErrUnsupportedHash = errors.New("netsec: only SHA-256 is supported")
	// ErrVerification reports a signature that does not have the exact
	// PKCS#1 v1.5 SHA-256 encoding.
	ErrVerification = errors.New("netsec: RSA PKCS#1 v1.5 verification failed")
	// ErrInvalidSignature is provided as a descriptive alias.
	ErrInvalidSignature = ErrVerification
)

var sha256DigestInfoPrefix = []byte{
	0x30, 0x31,
	0x30, 0x0d,
	0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01,
	0x05, 0x00,
	0x04, 0x20,
}

// VerifySHA256PKCS1v15 verifies a raw RSA PKCS#1 v1.5 signature over digest.
// The digest must be exactly 32 bytes. Only the SHA-256 DigestInfo encoding
// with an explicit NULL parameter is accepted.
func VerifySHA256PKCS1v15(pub *rsa.PublicKey, digest, sig []byte) error {
	if err := validatePublicKey(pub); err != nil {
		return err
	}
	if len(digest) != sha256.Size {
		return ErrInvalidDigest
	}

	k := pub.Size()
	if len(sig) != k {
		return ErrVerification
	}

	c := new(big.Int).SetBytes(sig)
	if c.Cmp(pub.N) >= 0 {
		return ErrVerification
	}

	m := new(big.Int).Exp(c, big.NewInt(int64(pub.E)), pub.N)
	em := make([]byte, k)
	m.FillBytes(em)

	return verifyEncodedMessage(k, digest, em)
}

// VerifyPKCS1v15SHA256 is an alias for VerifySHA256PKCS1v15.
func VerifyPKCS1v15SHA256(pub *rsa.PublicKey, digest, sig []byte) error {
	return VerifySHA256PKCS1v15(pub, digest, sig)
}

// VerifyRSAPKCS1v15SHA256 is an alias for VerifySHA256PKCS1v15.
func VerifyRSAPKCS1v15SHA256(pub *rsa.PublicKey, digest, sig []byte) error {
	return VerifySHA256PKCS1v15(pub, digest, sig)
}

// Verify verifies an RSA PKCS#1 v1.5 SHA-256 signature.
func Verify(pub *rsa.PublicKey, digest, sig []byte) error {
	return VerifySHA256PKCS1v15(pub, digest, sig)
}

// VerifyPKCS1v15 accepts either (pub, digest, sig) or the
// crypto/rsa-compatible (pub, crypto.SHA256, digest, sig) form. The hash
// argument is optional because this package intentionally supports only
// SHA-256.
func VerifyPKCS1v15(pub *rsa.PublicKey, hashOrDigest any, digestOrSig []byte, sig ...[]byte) error {
	switch value := hashOrDigest.(type) {
	case []byte:
		if len(sig) != 0 {
			return ErrVerification
		}
		return VerifySHA256PKCS1v15(pub, value, digestOrSig)
	case crypto.Hash:
		if value != crypto.SHA256 {
			return ErrUnsupportedHash
		}
		if len(sig) != 1 {
			return ErrVerification
		}
		return VerifySHA256PKCS1v15(pub, digestOrSig, sig[0])
	default:
		return ErrInvalidDigest
	}
}

func validatePublicKey(pub *rsa.PublicKey) error {
	if pub == nil || pub.N == nil || pub.N.Sign() <= 0 {
		return ErrInvalidPublicKey
	}
	if pub.E < minimumPublicExponent {
		return ErrInvalidPublicKey
	}
	return nil
}

func verifyEncodedMessage(k int, digest, em []byte) error {
	if len(digest) != sha256.Size {
		return ErrInvalidDigest
	}
	if len(em) != k {
		return ErrVerification
	}

	tLen := len(sha256DigestInfoPrefix) + sha256.Size
	psLen := k - 3 - tLen
	if psLen < 8 {
		return ErrVerification
	}

	expected := make([]byte, k)
	expected[0] = 0x00
	expected[1] = 0x01
	for i := 2; i < 2+psLen; i++ {
		expected[i] = 0xff
	}
	expected[2+psLen] = 0x00
	copy(expected[3+psLen:], sha256DigestInfoPrefix)
	copy(expected[3+psLen+len(sha256DigestInfoPrefix):], digest)

	if subtle.ConstantTimeCompare(em, expected) != 1 {
		return ErrVerification
	}
	return nil
}
