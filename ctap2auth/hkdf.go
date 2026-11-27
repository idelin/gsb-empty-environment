package ctap2auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
)

// hkdfSHA256 implements RFC 5869 HKDF with SHA-256 (Extract-then-Expand).
// salt may be nil/empty, in which case a HashLen zero-string salt is used, as
// required by the CTAP2 PIN/UV Auth Protocol Two derivation.
func hkdfSHA256(secret, salt, info []byte, length int) ([]byte, error) {
	if length <= 0 || length > 255*sha256.Size {
		return nil, errors.New("ctap2auth: HKDF output length out of range")
	}
	if len(salt) == 0 {
		salt = make([]byte, sha256.Size)
	}

	extract := hmac.New(sha256.New, salt)
	extract.Write(secret)
	prk := extract.Sum(nil)

	okm := make([]byte, 0, length)
	var previous []byte
	for counter := byte(1); len(okm) < length; counter++ {
		expand := hmac.New(sha256.New, prk)
		expand.Write(previous)
		expand.Write(info)
		expand.Write([]byte{counter})
		previous = expand.Sum(nil)
		okm = append(okm, previous...)
	}
	return okm[:length], nil
}
