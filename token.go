package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// tokenVersion tags the on-the-wire token format.
const tokenVersion = 1

// tokenClaims is the signed content of a confirmation token. Both sides of one
// transaction carry the same Digest and ExpireNano; Side prevents a token
// captured from one channel being replayed through the other.
type tokenClaims struct {
	Version    int    `json:"v"`
	UserID     string `json:"uid"`
	Digest     string `json:"digest"`
	Side       Side   `json:"side"`
	ExpireNano int64  `json:"exp"`
	Nonce      string `json:"nonce"`
}

// signer issues and verifies HMAC-signed confirmation tokens. The key is held
// only by the identity host and never leaves the service boundary.
type signer struct {
	key []byte
}

func newSigner(key []byte) (*signer, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("netsec: HMAC key must be at least 32 bytes, got %d", len(key))
	}
	return &signer{key: append([]byte(nil), key...)}, nil
}

func (h *signer) issue(claims tokenClaims) (string, error) {
	if claims.Version == 0 {
		claims.Version = tokenVersion
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := h.mac(body)
	return body + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

func (h *signer) parse(token string) (tokenClaims, error) {
	var claims tokenClaims
	dot := -1
	for i := len(token) - 1; i >= 0; i-- {
		if token[i] == '.' {
			dot = i
			break
		}
	}
	if dot <= 0 {
		return claims, fmt.Errorf("netsec: malformed confirmation token")
	}
	body, sig := token[:dot], token[dot+1:]
	expected := base64.RawURLEncoding.EncodeToString(h.mac(body))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		return claims, fmt.Errorf("netsec: confirmation token signature invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return claims, fmt.Errorf("netsec: malformed confirmation token payload: %w", err)
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, fmt.Errorf("netsec: malformed confirmation token claims: %w", err)
	}
	if claims.Version != tokenVersion {
		return claims, fmt.Errorf("netsec: unsupported confirmation token version %d", claims.Version)
	}
	return claims, nil
}

func (h *signer) mac(body string) []byte {
	mac := hmac.New(sha256.New, h.key)
	mac.Write([]byte(body))
	return mac.Sum(nil)
}

// computeDigest derives the change summary both proofs must bind. It covers the
// user, both channels and the deadline plus a random nonce, so two changes can
// never share a digest and a replayed proof cannot be re-bound to a new change.
func computeDigest(userID string, old, newChannel Channel, expiresNano int64, nonce string) string {
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s",
		"netsec/recovery-change/v1", userID, old.Kind, old.Value, newChannel.Kind, expiresNano, nonce)
	return base64.RawURLEncoding.EncodeToString(sum.Sum(nil))
}
