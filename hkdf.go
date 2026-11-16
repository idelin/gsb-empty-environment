package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
)

// SHA256Size is the output size of SHA-256 in bytes. HKDF-Expand can produce
// at most 255 blocks of this size.
const SHA256Size = sha256.Size

// MaxExpandLength is the largest output HKDF-SHA256 can produce:
// 255 * HashLen (RFC 5869, Section 2.3).
const MaxExpandLength = 255 * SHA256Size

var (
	// ErrInvalidExpandLength is returned when an HKDF-Expand request is
	// negative or exceeds MaxExpandLength.
	ErrInvalidExpandLength = errors.New("netsec: invalid HKDF expand length")
)

// Extract implements HKDF-Extract with SHA-256 as defined in RFC 5869:
//
//	PRK = HMAC-SHA256(salt, IKM)
//
// An empty or nil salt is treated as HashLen zero bytes, which is exactly how
// HMAC treats an empty key. The returned pseudorandom key is always 32 bytes.
func Extract(salt, ikm []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write(ikm)
	return mac.Sum(nil)
}

// ExtractSHA256 is an explicit alias for Extract.
func ExtractSHA256(salt, ikm []byte) []byte {
	return Extract(salt, ikm)
}

// Expand implements HKDF-Expand with SHA-256 as defined in RFC 5869:
//
//	T(0) = empty
//	T(i) = HMAC-SHA256(PRK, T(i-1) || info || byte(i))
//	OKM  = first length bytes of T(1) || T(2) || ...
//
// The PRK is normally the 32-byte output of Extract. A request longer than
// 255*SHA256Size is rejected.
func Expand(prk, info []byte, length int) ([]byte, error) {
	if length < 0 || length > MaxExpandLength {
		return nil, ErrInvalidExpandLength
	}
	if length == 0 {
		return []byte{}, nil
	}

	out := make([]byte, length)
	mac := hmac.New(sha256.New, prk)
	var previous [SHA256Size]byte
	havePrevious := false

	for offset, counter := 0, byte(1); offset < length; counter++ {
		mac.Reset()
		if havePrevious {
			_, _ = mac.Write(previous[:])
		}
		_, _ = mac.Write(info)
		_, _ = mac.Write([]byte{counter})

		sum := mac.Sum(previous[:0])
		copy(previous[:], sum)
		havePrevious = true

		offset += copy(out[offset:], sum)
	}

	return out, nil
}

// ExpandSHA256 is an explicit alias for Expand.
func ExpandSHA256(prk, info []byte, length int) ([]byte, error) {
	return Expand(prk, info, length)
}

// Derive runs Extract followed by Expand with SHA-256.
func Derive(secret, salt, info []byte, length int) ([]byte, error) {
	prk := Extract(salt, secret)
	defer clear(prk)
	return Expand(prk, info, length)
}
