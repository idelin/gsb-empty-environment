package dpop_test

import (
	"net/http"
	"testing"
	"time"

	"formal.local/w28-dd7127bdf9/dpop"
)

func TestSignatureByWrongKeyRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	opts.signer = keys.ecOther // embedded JWK is key A, but signed by key B

	req := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))
	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
}

func TestSymmetricJWKRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	opts.rawHeader = map[string]interface{}{
		"typ": "dpop+jwt",
		"alg": "HS256",
		"jwk": map[string]interface{}{
			"kty": "oct",
			"k":   b64url(make([]byte, 32)),
		},
	}
	req := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))
	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
}

func TestReplayCacheBoundedEviction(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	cache := dpop.NewMemoryReplayCache(time.Minute)
	verifier, err := dpop.NewVerifier(dpop.Options{
		ReplayCache:  cache,
		Now:          clock.now,
		ReplayWindow: time.Minute,
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	keys := loadTestKeys(t)
	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	htu := "https://api.example.com/resource/1"

	// A first proof succeeds; reusing its jti through the verifier must fail.
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	opts.jti = "bounded-jti"
	req := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))
	if _, err := verifier.VerifyRequest(req); err != nil {
		t.Fatalf("first use: %v", err)
	}
	req2 := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))
	if _, err := verifier.VerifyRequest(req2); err == nil {
		t.Fatalf("reused jti must be rejected within the retention window")
	}

	// After the window elapses the same jti is usable again.
	clock.advance(61 * time.Second)
	req3 := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))
	if _, err := verifier.VerifyRequest(req3); err != nil {
		t.Fatalf("jti should be reusable after the window: %v", err)
	}
}

func TestMissingAuthorizationAndDPoPHeaders(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)

	req := newRequest(t, http.MethodGet, "https://api.example.com/resource/1", "", "", "")
	if _, err := verifier.VerifyRequest(req); dpop.AsError(err) == nil {
		t.Fatalf("missing Authorization must fail")
	}

	keys := loadTestKeys(t)
	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	req2 := newRequest(t, http.MethodGet, "https://api.example.com/resource/1", "DPoP", accessToken, "")
	_, err := verifier.VerifyRequest(req2)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
}
