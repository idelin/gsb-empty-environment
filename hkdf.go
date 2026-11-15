package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
)

// sha256Size is the output length of HMAC-SHA256 in bytes.
const sha256Size = sha256.Size

// errHKDFLength is returned when an HKDF expansion requests more key material
// than the RFC 5869 construction can produce.
var errHKDFLength = errors.New("netsec: HKDF expand length exceeds 255*HashLen")

// Extract implements the HKDF-Extract step defined in RFC 5869 section 2.2:
//
//	PRK = HMAC-Hash(salt, IKM)
//
// If salt is nil or empty it is treated as a string of HashLen zero bytes, as
// mandated by the specification.
func Extract(salt, ikm []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, sha256Size)
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// Expand implements the HKDF-Expand step defined in RFC 5869 section 2.3:
//
//	T(0) = empty
//	T(i) = HMAC-Hash(PRK, T(i-1) | info | byte(i))
//	OKM  = first L bytes of T(1) | T(2) | T(3) | ...
//
// It returns an error when length is greater than 255*HashLen (8160 bytes for
// SHA-256).
func Expand(prk, info []byte, length int) ([]byte, error) {
	if length < 0 {
		return nil, errHKDFLength
	}
	if length > 255*sha256Size {
		return nil, errHKDFLength
	}

	okm := make([]byte, 0, length)
	var previous []byte
	for counter := byte(1); len(okm) < length; counter++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(previous)
		mac.Write(info)
		mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		okm = append(okm, previous...)
	}
	return okm[:length], nil
}

// DeriveKey is the one-shot HKDF construction from RFC 5869 section 2:
// HKDF-Expand(HKDF-Extract(salt, ikm), info, length).
func DeriveKey(salt, ikm, info []byte, length int) ([]byte, error) {
	prk := Extract(salt, ikm)
	return Expand(prk, info, length)
}
