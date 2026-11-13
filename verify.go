package netsec

import (
	"context"
	"crypto"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// joseHeader is the compact JWS header fields needed for verification.
type joseHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

// Verify validates a compact-serialized JWT. It enforces, in order:
//
//  1. structural JWS validity;
//  2. the "iss" claim equals the configured issuer (before any key lookup, so
//     a foreign issuer never reaches the local key cache);
//  3. the signature using only keys belonging to that same issuer;
//  4. standard claim validity (exp/nbf/iat, optional aud/nonce).
func (v *Verifier) Verify(ctx context.Context, compact string) (*IDToken, error) {
	if len(compact) > v.maxSize {
		return nil, fmt.Errorf("%w: token too large", ErrMalformedToken)
	}
	parts := strings.Split(compact, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return nil, ErrMalformedToken
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	var header joseHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	if header.Algorithm == "" || header.Algorithm == "none" {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlg, header.Algorithm)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: claims: %v", ErrMalformedToken, err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("%w: claims: %v", ErrMalformedToken, err)
	}

	// Issuer binding is checked first and independently of the key cache. The
	// token must be addressed to the issuer selected for this session.
	iss, _ := claims["iss"].(string)
	if iss == "" || iss != v.issuer {
		return nil, fmt.Errorf("%w: token iss %q, expected %q", ErrIssuerMismatch, iss, v.issuer)
	}

	signingInput := parts[0] + "." + parts[1]
	var signature []byte
	if parts[2] != "" {
		signature, err = base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return nil, fmt.Errorf("%w: signature: %v", ErrMalformedToken, err)
		}
	}

	// Keys are resolved only from the configured issuer's isolated KeySet,
	// never from a global cache shared with another issuer.
	key, err := v.keyForToken(ctx, header.KeyID, header.Algorithm)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(header.Algorithm, key, []byte(signingInput), signature); err != nil {
		return nil, err
	}

	token := parseClaims(claims, header)
	if err := v.checkClaims(token); err != nil {
		return nil, err
	}
	return token, nil
}

// keyForToken resolves the key identified by kid from the configured issuer's
// own keys. A verifier never consults keys belonging to another issuer.
func (v *Verifier) keyForToken(ctx context.Context, kid, alg string) (crypto.PublicKey, error) {
	if v.staticKeys != nil {
		if key := v.staticKeys.Lookup(kid, alg); key != nil {
			return key, nil
		}
		return nil, fmt.Errorf("%w: kid %q", ErrKeyNotFound, kid)
	}

	if err := v.ensureDiscovery(ctx); err != nil {
		return nil, err
	}

	v.mu.Lock()
	keys := v.cachedKeys
	v.mu.Unlock()
	if keys != nil {
		if key := keys.Lookup(kid, alg); key != nil {
			return key, nil
		}
	}

	// Cache miss: refresh the issuer's JWKS once to pick up key rotation.
	refreshed, err := v.fetchJWKS(ctx)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.cachedKeys = refreshed
	v.mu.Unlock()
	if key := refreshed.Lookup(kid, alg); key != nil {
		return key, nil
	}
	return nil, fmt.Errorf("%w: kid %q", ErrKeyNotFound, kid)
}
