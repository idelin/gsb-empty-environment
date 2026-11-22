package takeover

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Signer signs a canonical byte string. KeyID identifies the verification key.
type Signer interface {
	KeyID() string
	Sign(message []byte) ([]byte, error)
}

// SignatureVerifier verifies a signature produced by a Signer.
type SignatureVerifier interface {
	Verify(keyID string, message, signature []byte) bool
}

// HMACSigner is a compact shared-key signer suitable for a set of cooperating
// session hosts.
type HMACSigner struct {
	keyID string
	key   []byte
}

// NewHMACSigner constructs an HMAC-SHA256 signer.
func NewHMACSigner(keyID string, key []byte) *HMACSigner {
	return &HMACSigner{keyID: keyID, key: append([]byte(nil), key...)}
}

// KeyID returns the signer key identifier.
func (s *HMACSigner) KeyID() string {
	return s.keyID
}

// Sign signs message with HMAC-SHA256.
func (s *HMACSigner) Sign(message []byte) ([]byte, error) {
	if s == nil || len(s.key) == 0 {
		return nil, fmt.Errorf("takeover: empty HMAC key")
	}
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(message)
	return mac.Sum(nil), nil
}

// HMACVerifier verifies HMAC-SHA256 signatures from a keyring.
type HMACVerifier struct {
	keys map[string][]byte
}

// NewHMACVerifier constructs a verifier from key identifiers to secrets.
func NewHMACVerifier(keys map[string][]byte) *HMACVerifier {
	copied := make(map[string][]byte, len(keys))
	for keyID, key := range keys {
		copied[keyID] = append([]byte(nil), key...)
	}
	return &HMACVerifier{keys: copied}
}

// Verify verifies an HMAC signature.
func (v *HMACVerifier) Verify(keyID string, message, signature []byte) bool {
	if v == nil {
		return false
	}
	key, ok := v.keys[keyID]
	if !ok || len(key) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(message)
	return hmac.Equal(mac.Sum(nil), signature)
}

func canonicalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}
