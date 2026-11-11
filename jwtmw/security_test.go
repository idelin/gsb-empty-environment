package jwtmw

import (
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"net/http"
	"testing"
)

func seedRSA(t *testing.T, v *Verifier, h *jwksHandler, kid string, key *rsa.PrivateKey) {
	t.Helper()
	h.setKeys(jwkFromPublic(kid, &key.PublicKey))
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed jwks: %v", err)
	}
	if got := h.fetches.Load(); got != 1 {
		t.Fatalf("expected 1 seed fetch, got %d", got)
	}
}

func TestValidRS256Token_Accepted(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	rsaKey := mustRSAKey(t)
	seedRSA(t, v, h, "rsa-1", rsaKey)

	token := signToken(t, "RS256", "rsa-1", rsaKey, baseClaims(clock))
	rec := doRequest(t, v, token)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == "" || rec.Body.String()[:1] != "{" {
		t.Fatalf("expected JSON body, got %q", rec.Body.String())
	}
}

func TestAlgNone_Rejected(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	rsaKey := mustRSAKey(t)
	seedRSA(t, v, h, "rsa-1", rsaKey)

	// Both canonical lowercase "none" and the common "None"/"NONE" bypass
	// variants must be refused before any key lookup or fetch happens.
	for _, alg := range []string{"none", "None", "NONE", "nOnE"} {
		token := noneToken(t, "rsa-1", baseClaims(clock))
		if alg != "none" {
			token = mutateAlg(t, token, alg)
		}
		rec := doRequest(t, v, token)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("alg %q: expected 401, got %d", alg, rec.Code)
		}
		if got := h.fetches.Load(); got != 1 {
			t.Fatalf("alg %q triggered a JWKS fetch (total=%d)", alg, got)
		}
	}
}

func TestAlgorithmConfusion_HMACWithRSAPublicKey_Rejected(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	rsaKey := mustRSAKey(t)
	seedRSA(t, v, h, "rsa-1", rsaKey)

	// Real-world confusion attacks feed the public key as an HMAC secret.
	// PKIX DER is the stable byte form of the JWKS RSA public key.
	secret, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	token := hmacToken(t, "rsa-1", secret, baseClaims(clock))

	rec := doRequest(t, v, token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for HMAC/RSA confusion, got %d: %s", rec.Code, rec.Body.String())
	}

	// A second variant using the modulus bytes directly must fail identically.
	token = hmacToken(t, "rsa-1", rsaKey.PublicKey.N.Bytes(), baseClaims(clock))
	rec = doRequest(t, v, token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for modulus HMAC confusion, got %d", rec.Code)
	}

	_, err = v.Verify(t.Context(), token)
	if !errors.Is(err, ErrUnsupportedAlg) {
		t.Fatalf("expected ErrUnsupportedAlg, got %v", err)
	}
}

func TestHeaderAlgMustMatchKeyType(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	rsaKey := mustRSAKey(t)
	ecKey := mustECKey(t, ecdsaCurve(t, "P-256"))
	h.setKeys(
		jwkFromPublic("rsa-1", &rsaKey.PublicKey),
		jwkFromPublic("ec-1", &ecKey.PublicKey),
	)
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// ES256 token presented against the RSA key id.
	ecToken := signToken(t, "ES256", "ec-1", ecKey, baseClaims(clock))
	confused := mutateKidAndAlg(t, ecToken, "rsa-1", "ES256")
	rec := doRequest(t, v, confused)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ES256 against RSA key: expected 401, got %d", rec.Code)
	}

	// RS256 token presented against the EC key id.
	rsaToken := signToken(t, "RS256", "rsa-1", rsaKey, baseClaims(clock))
	confused = mutateKidAndAlg(t, rsaToken, "ec-1", "RS256")
	rec = doRequest(t, v, confused)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("RS256 against EC key: expected 401, got %d", rec.Code)
	}

	// Tampered signature on an otherwise valid token.
	valid := signToken(t, "RS256", "rsa-1", rsaKey, baseClaims(clock))
	tampered := tamperSignature(valid)
	rec = doRequest(t, v, tampered)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered signature: expected 401, got %d", rec.Code)
	}
}

func TestJWKAlgPin_Enforced(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	rsaKey := mustRSAKey(t)
	jwk := jwkFromPublic("rsa-1", &rsaKey.PublicKey)
	jwk["alg"] = "PS256"
	h.setKeys(jwk)
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Key is pinned to PS256; an RS256 token with the same kid must fail.
	rsToken := signToken(t, "RS256", "rsa-1", rsaKey, baseClaims(clock))
	if rec := doRequest(t, v, rsToken); rec.Code != http.StatusUnauthorized {
		t.Fatalf("pinned alg mismatch: expected 401, got %d", rec.Code)
	}

	psToken := signToken(t, "PS256", "rsa-1", rsaKey, baseClaims(clock))
	if rec := doRequest(t, v, psToken); rec.Code != http.StatusOK {
		t.Fatalf("pinned matching alg: expected 200, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestECAndEd25519Flows(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	ecKey := mustECKey(t, ecdsaCurve(t, "P-256"))
	edPub, edPriv := mustEdKey(t)
	h.setKeys(
		jwkFromPublic("ec-1", &ecKey.PublicKey),
		jwkFromPublic("ed-1", edPub),
	)
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ecToken := signToken(t, "ES256", "ec-1", ecKey, baseClaims(clock))
	if rec := doRequest(t, v, ecToken); rec.Code != http.StatusOK {
		t.Fatalf("ES256: expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	edToken := signToken(t, "EdDSA", "ed-1", edPriv, baseClaims(clock))
	if rec := doRequest(t, v, edToken); rec.Code != http.StatusOK {
		t.Fatalf("EdDSA: expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	// ES384 against a P-256 key is rejected (curve/hash mismatch).
	ec384 := mustECKey(t, ecdsaCurve(t, "P-384"))
	token384 := signToken(t, "ES384", "ec-1", ec384, baseClaims(clock))
	if rec := doRequest(t, v, token384); rec.Code != http.StatusUnauthorized {
		t.Fatalf("ES384/P-256 mismatch: expected 401, got %d", rec.Code)
	}
}

func TestAllowedAlgs_RestrictsAcceptableAlgorithms(t *testing.T) {
	clock := newFakeClock()
	server := newJWKSServer(t)
	rsaKey := mustRSAKey(t)
	server.handler.setKeys(jwkFromPublic("rsa-1", &rsaKey.PublicKey))

	cfg := testConfig(t, server.URL, clock, server.client)
	cfg.AllowedAlgs = []string{"RS256"}
	v, err := New(cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := v.Refresh(t.Context()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if rec := doRequest(t, v, signToken(t, "RS256", "rsa-1", rsaKey, baseClaims(clock))); rec.Code != http.StatusOK {
		t.Fatalf("RS256 should be allowed: %d", rec.Code)
	}
	if rec := doRequest(t, v, signToken(t, "PS256", "rsa-1", rsaKey, baseClaims(clock))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("PS256 should be blocked by allowlist: %d", rec.Code)
	}

	if _, err := New(Config{JWKSURI: server.URL, AllowedAlgs: []string{"HS256"}}); err == nil {
		t.Fatal("expected error when allowlisting symmetric HS256")
	}
}

func TestMissingOrMalformedAuthorizationHeader(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)

	cases := map[string]string{
		"no header":         "",
		"wrong scheme":      "Basic abc",
		"empty bearer":      "Bearer ",
		"garbage token":     "Bearer not-a-jwt",
		"two segments only": "Bearer aaa.bbb",
		"bad base64 header": "Bearer @@.bbb.ccc",
	}
	for name, auth := range cases {
		t.Run(name, func(t *testing.T) {
			req := newReqWithAuth(auth)
			rec := httptestServe(v, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("expected WWW-Authenticate challenge header")
			}
		})
	}
}

func TestSymmetricOctJWK_IsNeverAccepted(t *testing.T) {
	clock := newFakeClock()
	h := &jwksHandler{}
	v := newVerifier(t, clock, h)
	h.setKeys(map[string]any{
		"kty": "oct",
		"kid": "sym-1",
		"alg": "HS256",
		"k":   "c2VjcmV0",
	})
	err := v.Refresh(t.Context())
	if err == nil {
		t.Fatal("expected JWKS with only oct keys to be rejected")
	}
	rec := doRequest(t, v, hmacToken(t, "sym-1", []byte("secret"), baseClaims(clock)))
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401/503 for oct-key token, got %d", rec.Code)
	}
}
