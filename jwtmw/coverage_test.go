package jwtmw

import (
	"crypto/elliptic"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRSAAlgorithmMatrix(t *testing.T) {
	for _, alg := range []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512"} {
		t.Run(alg, func(t *testing.T) {
			clock := newFakeClock()
			h := &jwksHandler{}
			v := newVerifier(t, clock, h)
			key := mustRSAKey(t)
			jwk := jwkFromPublic("k", &key.PublicKey)
			delete(jwk, "alg") // do not pin; accept by key type
			h.setKeys(jwk)
			if err := v.Refresh(t.Context()); err != nil {
				t.Fatalf("seed: %v", err)
			}
			token := signToken(t, alg, "k", key, baseClaims(clock))
			if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
				t.Fatalf("%s: expected 200, got %d %s", alg, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestECCurveMatrix(t *testing.T) {
	cases := []struct {
		crv string
		alg string
	}{
		{"P-256", "ES256"},
		{"P-384", "ES384"},
		{"P-521", "ES512"},
	}
	for _, tc := range cases {
		t.Run(tc.crv, func(t *testing.T) {
			clock := newFakeClock()
			h := &jwksHandler{}
			v := newVerifier(t, clock, h)
			key := mustECKey(t, ecdsaCurve(t, tc.crv))
			h.setKeys(jwkFromPublic("k", &key.PublicKey))
			if err := v.Refresh(t.Context()); err != nil {
				t.Fatalf("seed: %v", err)
			}
			token := signToken(t, tc.alg, "k", key, baseClaims(clock))
			if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
				t.Fatalf("%s: expected 200, got %d %s", tc.alg, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMalformedJWKsRejected(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	key := mustRSAKey(t)
	good := jwkFromPublic("good", &key.PublicKey)

	badN := jwkFromPublic("bad-n", &key.PublicKey)
	badN["n"] = "!!!not-base64!!!"

	ecKey := mustECKey(t, elliptic.P256())
	badCurve := jwkFromPublic("bad-curve", &ecKey.PublicKey)
	badCurve["crv"] = "P-999"

	noKid := jwkFromPublic("ignored", &key.PublicKey)
	delete(noKid, "kid")

	badAlg := jwkFromPublic("bad-alg", &key.PublicKey)
	badAlg["alg"] = "ES256" // incompatible with RSA key

	h.setKeys(good, badN, badCurve, noKid, badAlg)
	_ = v.Refresh(t.Context()) // partial parse errors are returned; cache still holds good keys

	// The good key remains usable despite malformed siblings.
	token := signToken(t, "RS256", "good", key, baseClaims(clock))
	if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
		t.Fatalf("good key alongside bad JWKs: expected 200, got %d", rec.Code)
	}

	for _, kid := range []string{"bad-n", "bad-curve", "ignored", "bad-alg"} {
		if _, err := v.lookup(kid); err == nil {
			t.Fatalf("malformed key %q should not be cached", kid)
		}
	}

	// A document where every key is malformed must not replace the cache.
	h.setKeys(badN)
	if err := v.Refresh(t.Context()); err == nil {
		t.Fatal("expected error for all-malformed JWKS")
	}
	if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
		t.Fatalf("cache must survive an all-malformed refresh: got %d", rec.Code)
	}
}

func TestConfigValidation(t *testing.T) {
	clock := newFakeClock()
	if _, err := New(Config{}); err == nil {
		t.Fatal("missing JWKSURI should error")
	}
	if _, err := New(Config{JWKSURI: "u", GracePeriod: -1}); err == nil {
		t.Fatal("negative GracePeriod should error")
	}
	if _, err := New(Config{JWKSURI: "u", MinRefreshInterval: -1}); err == nil {
		t.Fatal("negative MinRefreshInterval should error")
	}
	if _, err := New(Config{JWKSURI: "u", Leeway: -1}); err == nil {
		t.Fatal("negative Leeway should error")
	}

	v, err := New(Config{JWKSURI: "u", now: clock.now})
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if v.cfg.CacheTTL != 10*time.Minute {
		t.Fatalf("default CacheTTL not applied: %v", v.cfg.CacheTTL)
	}
	if v.client == nil || v.client.Timeout != 10*time.Second {
		t.Fatal("default HTTP client not applied")
	}
}

func TestMalformedTokens(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	valid := signToken(t, "RS256", "k", key, baseClaims(clock))
	mutatePayload := func(mut func(claims map[string]any)) string {
		parts := splitToken(valid)
		claims := map[string]any{}
		_ = jsonUnmarshal(decodeSegment(t, parts[1]), &claims)
		mut(claims)
		return encodeReplaced(t, parts, nil, claims)
	}

	// Tampered payload without re-signing must fail signature verification.
	tampered := mutatePayload(func(c map[string]any) { c["sub"] = "attacker" })
	if rec := doRequest(t, v, tampered); rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered payload: expected 401, got %d", rec.Code)
	}

	// Invalid JSON payload.
	parts := splitToken(valid)
	parts[1] = encodeB64([]byte("{not-json"))
	if rec := doRequest(t, v, joinParts(parts)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid payload json: expected 401, got %d", rec.Code)
	}

	// Invalid exp type.
	badExp := mutatePayload(func(c map[string]any) { c["exp"] = "not-a-number" })
	if rec := doRequest(t, v, badExp); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid exp: expected 401, got %d", rec.Code)
	}
}

func TestUnsupportedAlgVariants(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, alg := range []string{"HS256", "HS384", "HS512", "RS999", ""} {
		token := mutateAlg(t, signToken(t, "RS256", "k", key, baseClaims(clock)), alg)
		if alg == "" {
			token = dropAlg(t, token)
		}
		rec := doRequest(t, v, token)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("alg %q: expected 401, got %d", alg, rec.Code)
		}
	}
}

func encodeB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func joinParts(parts []string) string { return strings.Join(parts, ".") }

func dropAlg(t *testing.T, token string) string {
	t.Helper()
	parts := splitToken(token)
	header := map[string]any{}
	_ = jsonUnmarshal(decodeSegment(t, parts[0]), &header)
	delete(header, "alg")
	return encodeReplaced(t, parts, header, nil)
}
