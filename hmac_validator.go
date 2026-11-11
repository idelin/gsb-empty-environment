package netsec

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// HMACValidator verifies compact, HMAC-SHA256-signed ID tokens issued by one
// fixed issuer. The token format mirrors a JWT structurally
// (base64url(header).base64url(payload).base64url(signature)) but uses a
// symmetric secret, which keeps the test harness dependency-free while still
// proving signature, issuer, expiry and nonce binding.
type HMACValidator struct {
	Issuer string
	Secret []byte

	// Leeway relaxes expiry checks for clock skew.
	Leeway time.Duration

	now func() time.Time
}

// NewHMACValidator builds a validator pinned to one issuer.
func NewHMACValidator(issuer string, secret []byte) *HMACValidator {
	return &HMACValidator{Issuer: issuer, Secret: secret, now: time.Now}
}

// VerifyIDToken implements Validator.
func (v *HMACValidator) VerifyIDToken(_ context.Context, rawToken, expectedNonce string) (Claims, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("netsec: malformed ID token")
	}

	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("netsec: malformed ID token signature: %w", err)
	}
	mac := hmac.New(sha256.New, v.Secret)
	mac.Write([]byte(signingInput))
	if !hmac.Equal(mac.Sum(nil), signature) {
		return nil, errors.New("netsec: ID token signature mismatch")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("netsec: malformed ID token payload: %w", err)
	}
	claims := Claims{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("netsec: malformed ID token claims: %w", err)
	}

	if claims.Issuer() != v.Issuer {
		return nil, ErrIssuerMismatch
	}
	if claims.Subject() == "" {
		return nil, errors.New("netsec: ID token missing subject")
	}
	if expectedNonce != "" && claims.Nonce() != expectedNonce {
		return nil, errors.New("netsec: ID token nonce mismatch")
	}

	now := v.now()
	leeway := v.Leeway
	if exp, ok := claims["exp"].(float64); ok {
		if now.Add(leeway).After(time.Unix(int64(exp), 0)) {
			return nil, errors.New("netsec: ID token expired")
		}
	}
	if iat, ok := claims["iat"].(float64); ok {
		if now.Add(-leeway).Before(time.Unix(int64(iat), 0)) {
			return nil, errors.New("netsec: ID token issued in the future")
		}
	}

	return claims, nil
}

// SignIDToken mints an ID token accepted by an HMACValidator with the same
// issuer and secret. It is used by the local fake provider and in tests.
func SignIDToken(issuer string, secret []byte, claims Claims) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	if claims["iss"] == nil {
		claims["iss"] = issuer
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payloadJSON)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + signature, nil
}
