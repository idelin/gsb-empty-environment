package dpop_test

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"formal.local/w28-dd7127bdf9/dpop"
)

func TestCnfJKTThumbprintMismatchRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	// Access token is bound to a different key than the one signing the proof.
	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecOtherJwk))
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	req := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))

	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoPKeyBind)
}

func TestTokenMissingCnfJKTRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	accessToken := mintAccessToken(t, "")
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	req := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))

	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoPKeyBind)
}

func TestHTMMismatchRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	req, accessToken := validRequest(t, keys, clock, nil)
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodGet, htu, clock.t)
	req.Header.Set("DPoP", signProof(t, opts))

	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
	if !strings.Contains(dpop.AsError(err).Description, "htm") {
		t.Fatalf("expected htm error, got %v", err)
	}
}

func TestHTUQueryAndFragmentConfusionRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	confusions := []string{
		"https://api.example.com/resource/1?debug=1",
		"https://api.example.com/resource/1#fragment",
		"https://api.example.com/resource/1?x=1#f",
		"https://attacker.example/resource/1",
		"https://api.example.com/Resource/1", // path case is significant
		"https://api.example.com/resource/2",
	}
	for _, confusedHTU := range confusions {
		req, accessToken := validRequest(t, keys, clock, nil)
		opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, confusedHTU, clock.t)
		req.Header.Set("DPoP", signProof(t, opts))

		_, err := verifier.VerifyRequest(req)
		assertDPoPError(t, err, dpop.ErrInvalidDPoP)
	}
}

func TestHTUCanonicalizationAcceptsHostCaseAndStripsRequestQuery(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	// The request carries query/fragment; the proof uses the canonical URL
	// without them and with an uppercase scheme/host, which must still match.
	jkt := mustThumbprint(t, keys.ecPublic)
	accessToken := mintAccessToken(t, jkt)
	proofHTU := "HTTPS://API.Example.COM/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, proofHTU, clock.t)
	req := newRequest(t, http.MethodPost, "https://api.example.com/resource/1?a=b&c=d#section", "DPoP", accessToken, signProof(t, opts))

	claims, err := verifier.VerifyRequest(req)
	if err != nil {
		t.Fatalf("canonical htu should match: %v", err)
	}
	if claims.HTU != proofHTU {
		t.Fatalf("htu = %q", claims.HTU)
	}
}

func TestATHMismatchRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	req, accessToken := validRequest(t, keys, clock, nil)
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	opts.ath = b64url(bytes.Repeat([]byte{0xff}, 32))
	req.Header.Set("DPoP", signProof(t, opts))

	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
	if !strings.Contains(dpop.AsError(err).Description, "ath") {
		t.Fatalf("expected ath error, got %v", err)
	}
}

func TestIATWindowAndClockSkew(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	keys := loadTestKeys(t)
	htu := "https://api.example.com/resource/1"

	cases := []struct {
		name    string
		iat     time.Time
		wantErr bool
	}{
		{"now", base, false},
		{"future within skew", base.Add(30 * time.Second), false},
		{"past within ttl", base.Add(-4 * time.Minute), false},
		{"future beyond skew", base.Add(2 * time.Minute), true},
		{"proof too old", base.Add(-7 * time.Minute), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fixedClock{t: base}
			verifier := newVerifier(t, clock, nil)
			accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
			opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, tc.iat)
			opts.jti = "jti-" + tc.name
			req := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))
			_, err := verifier.VerifyRequest(req)
			if tc.wantErr {
				assertDPoPError(t, err, dpop.ErrInvalidDPoP)
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestJTIReplayAcrossEndpointsRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	htuA := "https://api.example.com/resource/a"
	htuB := "https://api.example.com/resource/b"

	optsA := defaultProofOptions(t, keys, accessToken, http.MethodPost, htuA, clock.t)
	optsA.jti = "shared-jti"
	reqA := newRequest(t, http.MethodPost, htuA, "DPoP", accessToken, signProof(t, optsA))
	if _, err := verifier.VerifyRequest(reqA); err != nil {
		t.Fatalf("first use: %v", err)
	}

	// Reusing the same jti against a different endpoint/method must fail.
	optsB := defaultProofOptions(t, keys, accessToken, http.MethodGet, htuB, clock.t)
	optsB.jti = "shared-jti"
	reqB := newRequest(t, http.MethodGet, htuB, "DPoP", accessToken, signProof(t, optsB))
	_, err := verifier.VerifyRequest(reqB)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
	if !strings.Contains(dpop.AsError(err).Description, "jti") {
		t.Fatalf("expected jti replay error, got %v", err)
	}
}

func TestJTIRotatesAfterWindow(t *testing.T) {
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
	htu := "https://api.example.com/resource/1"

	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	opts.jti = "expiring-jti"
	req := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))
	if _, err := verifier.VerifyRequest(req); err != nil {
		t.Fatalf("first use: %v", err)
	}

	clock.advance(61 * time.Second)
	req2 := newRequest(t, http.MethodPost, htu, "DPoP", accessToken, signProof(t, opts))
	if _, err := verifier.VerifyRequest(req2); err != nil {
		t.Fatalf("jti should be reusable after replay window: %v", err)
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	req, _ := validRequest(t, keys, clock, nil)
	proof := req.Header.Get("DPoP")
	parts := strings.Split(proof, ".")
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	signature[0] ^= 0xff
	parts[2] = b64url(signature)
	req.Header.Set("DPoP", strings.Join(parts, "."))

	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
}

func TestEdDSAKeyTypeMismatchRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	accessToken := mintAccessToken(t, mustThumbprint(t, keys.edPublic))
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodGet, htu, clock.t)
	opts.alg = "ES256" // mismatches the Ed25519 JWK
	opts.jwk = keys.edPublic
	opts.signer = keys.edPrivate
	opts.signAlg = "EdDSA"

	req := newRequest(t, http.MethodGet, htu, "DPoP", accessToken, signProof(t, opts))
	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
}
