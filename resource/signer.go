package resource

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Signer authenticates requests by signing the canonical form of an ID.
// Because the signature covers exactly the same string the cache and the
// object store use as a key, a signature can never be valid for one
// resource while the bytes are fetched for another.
type Signer struct {
	secret []byte
}

// NewSigner returns a Signer using the given HMAC secret.
func NewSigner(secret []byte) Signer {
	key := make([]byte, len(secret))
	copy(key, secret)
	return Signer{secret: key}
}

// Sign returns the hex-encoded HMAC-SHA256 of the ID's canonical form.
func (s Signer) Sign(id ID) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(id.canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether signature is valid for id.
func (s Signer) Verify(id ID, signature string) bool {
	want, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(id.canonical))
	return hmac.Equal(mac.Sum(nil), want)
}
