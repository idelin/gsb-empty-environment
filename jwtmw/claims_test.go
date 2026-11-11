package jwtmw

import (
	"net/http"
	"testing"
	"time"
)

func TestExpiryBoundary(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	claimsWithExp := func(exp time.Time) map[string]any {
		c := baseClaims(clock)
		c["exp"] = exp.Unix()
		return c
	}

	// Token expiring exactly now is still accepted (exp is an inclusive bound).
	token := signToken(t, "RS256", "k", key, claimsWithExp(clock.now()))
	if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
		t.Fatalf("exp == now: expected 200, got %d", rec.Code)
	}

	// One second past expiry is rejected.
	token = signToken(t, "RS256", "k", key, claimsWithExp(clock.now().Add(-time.Second)))
	if rec := doRequest(t, v, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("exp in past: expected 401, got %d", rec.Code)
	}

	// Future expiry accepted.
	token = signToken(t, "RS256", "k", key, claimsWithExp(clock.now().Add(time.Minute)))
	if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
		t.Fatalf("future exp: expected 200, got %d", rec.Code)
	}
}

func TestLeewayToleratesClockSkew(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	server := newMemoryServer(t, h)
	cfg := testConfig(t, server.URL, clock, server.client())
	cfg.Leeway = 5 * time.Second
	v, err := New(cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	claims := baseClaims(clock)
	claims["exp"] = clock.now().Add(-4 * time.Second).Unix()
	token := signToken(t, "RS256", "k", key, claims)
	if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
		t.Fatalf("exp within leeway: expected 200, got %d", rec.Code)
	}

	claims["exp"] = clock.now().Add(-6 * time.Second).Unix()
	token = signToken(t, "RS256", "k", key, claims)
	if rec := doRequest(t, v, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("exp beyond leeway: expected 401, got %d", rec.Code)
	}
}

func TestNotBeforeBoundary(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	claims := baseClaims(clock)

	// nbf in the future: not yet valid.
	claims["nbf"] = clock.now().Add(time.Minute).Unix()
	token := signToken(t, "RS256", "k", key, claims)
	if rec := doRequest(t, v, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("future nbf: expected 401, got %d", rec.Code)
	}

	// nbf exactly now: valid.
	claims["nbf"] = clock.now().Unix()
	token = signToken(t, "RS256", "k", key, claims)
	if rec := doRequest(t, v, token); rec.Code != http.StatusOK {
		t.Fatalf("nbf == now: expected 200, got %d", rec.Code)
	}
}

func TestIssuerAndAudienceEnforced(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	server := newMemoryServer(t, h)
	cfg := testConfig(t, server.URL, clock, server.client())
	cfg.Issuer = "https://issuer.example"
	cfg.Audience = "gateway"
	v, err := New(cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	good := baseClaims(clock)
	if rec := doRequest(t, v, signToken(t, "RS256", "k", key, good)); rec.Code != http.StatusOK {
		t.Fatalf("matching iss/aud: expected 200, got %d", rec.Code)
	}

	badIssuer := baseClaims(clock)
	badIssuer["iss"] = "https://attacker.example"
	if rec := doRequest(t, v, signToken(t, "RS256", "k", key, badIssuer)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong issuer: expected 401, got %d", rec.Code)
	}

	badAudience := baseClaims(clock)
	badAudience["aud"] = []string{"other-service"}
	if rec := doRequest(t, v, signToken(t, "RS256", "k", key, badAudience)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong audience: expected 401, got %d", rec.Code)
	}

	// Array audience containing the expected value is accepted.
	arrayAud := baseClaims(clock)
	arrayAud["aud"] = []string{"gateway", "billing"}
	if rec := doRequest(t, v, signToken(t, "RS256", "k", key, arrayAud)); rec.Code != http.StatusOK {
		t.Fatalf("array audience match: expected 200, got %d", rec.Code)
	}
}

func TestMissingKidRejected(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	key := mustRSAKey(t)
	h.setKeys(jwkFromPublic("k", &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	parts := splitToken(signToken(t, "RS256", "k", key, baseClaims(clock)))
	headerJSON := decodeSegment(t, parts[0])
	var header map[string]any
	_ = jsonUnmarshal(headerJSON, &header)
	delete(header, "kid")
	token := encodeReplaced(t, parts, header, nil)

	if rec := doRequest(t, v, token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing kid: expected 401, got %d", rec.Code)
	}
}
