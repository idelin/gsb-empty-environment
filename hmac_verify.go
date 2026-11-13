package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// HMACTagSize is the size of an HMAC-SHA256 tag in bytes.
const HMACTagSize = sha256.Size

// CanonicalRequest serializes a request without delimiter ambiguity.  Each
// field is prefixed by its byte length as an unsigned 64-bit big-endian value:
//
//	uint64be(len(method)) || method ||
//	uint64be(len(path))   || path   ||
//	uint64be(len(body))   || body
//
// Therefore moving bytes between method, path, and body always changes the
// canonical byte string.
func CanonicalRequest(method, path string, body []byte) []byte {
	size := 8 + len(method) + 8 + len(path) + 8 + len(body)
	canonical := make([]byte, 0, size)
	canonical = appendLengthPrefixed(canonical, []byte(method))
	canonical = appendLengthPrefixed(canonical, []byte(path))
	canonical = appendLengthPrefixed(canonical, body)
	return canonical
}

// SignHMACRequest returns HMAC-SHA256(secret, CanonicalRequest(...)).
func SignHMACRequest(secret []byte, method, path string, body []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(CanonicalRequest(method, path, body))
	return mac.Sum(nil)
}

// VerifyHMACRequest verifies a migrated request authentication tag using
// HMAC-SHA256 over the length-prefixed canonical request.  Comparison is
// constant-time for equal-length tags.
func VerifyHMACRequest(secret []byte, method, path string, body, tag []byte) bool {
	expected := SignHMACRequest(secret, method, path, body)
	return hmac.Equal(tag, expected)
}

// HMACVerifier is a server-side verifier that keeps its secret private.  The
// secret is copied at construction and is never returned by the type.
type HMACVerifier struct {
	secret []byte
}

// NewHMACVerifier creates a verifier for the given HMAC key.  The key is copied
// so later mutation of secret by the caller cannot change the verifier.
func NewHMACVerifier(secret []byte) *HMACVerifier {
	return &HMACVerifier{secret: cloneBytes(secret)}
}

// Sign returns the migrated request tag for method, path, and body.
func (verifier *HMACVerifier) Sign(method, path string, body []byte) []byte {
	return SignHMACRequest(verifier.secret, method, path, body)
}

// Verify checks method, path, body, and tag with HMAC-SHA256 and a constant-time
// comparison.  A tag from the legacy SHA-256(secret||message) scheme, and any
// length-extended variant of it, does not authenticate the canonical request.
func (verifier *HMACVerifier) Verify(method, path string, body, tag []byte) bool {
	if verifier == nil {
		return false
	}
	return VerifyHMACRequest(verifier.secret, method, path, body, tag)
}

func appendLengthPrefixed(dst, field []byte) []byte {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(field)))
	dst = append(dst, length[:]...)
	return append(dst, field...)
}
