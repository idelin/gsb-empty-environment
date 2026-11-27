package dpop_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"formal.local/w28-dd7127bdf9/dpop"
)

func newVerifier(t *testing.T, clock *fixedClock, nonce *dpop.NonceManager) *dpop.Verifier {
	t.Helper()
	verifier, err := dpop.NewVerifier(dpop.Options{
		NonceManager: nonce,
		Now:          clock.now,
		ClockSkew:    time.Minute,
		ProofTTL:     5 * time.Minute,
		ReplayWindow: 10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return verifier
}

func validRequest(t *testing.T, keys *testKeys, clock *fixedClock, nonce *dpop.NonceManager) (*http.Request, string) {
	t.Helper()
	jkt := mustThumbprint(t, keys.ecPublic)
	accessToken := mintAccessToken(t, jkt)
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	if nonce != nil {
		opts.nonce = nonce.Issue()
	}
	proof := signProof(t, opts)
	req := newRequest(t, http.MethodPost, "https://api.example.com/resource/1?debug=1#frag", "DPoP", accessToken, proof)
	return req, accessToken
}

func TestHappyPathAndClaimsContext(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	nonce, err := dpop.NewNonceManager(bytes.Repeat([]byte{0x01}, 32), time.Hour)
	if err != nil {
		t.Fatalf("nonce manager: %v", err)
	}
	verifier := newVerifier(t, clock, nonce)
	keys := loadTestKeys(t)

	req, _ := validRequest(t, keys, clock, nonce)
	claims, err := verifier.VerifyRequest(req)
	if err != nil {
		t.Fatalf("VerifyRequest: %v", err)
	}
	if claims.JKT != mustThumbprint(t, keys.ecPublic) {
		t.Fatalf("jkt mismatch")
	}
	if claims.JTI == "" || claims.HTM != http.MethodPost {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func TestMiddlewareSuccessAndNonceHeader(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	nonce, _ := dpop.NewNonceManager(bytes.Repeat([]byte{0x02}, 32), time.Hour)
	verifier := newVerifier(t, clock, nonce)
	handler := newTestHandler(t, verifier)
	keys := loadTestKeys(t)

	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodGet, htu, clock.t)
	opts.nonce = nonce.Issue()

	rec := invoke(t, handler, http.MethodGet, "https://api.example.com/resource/1", func(req *http.Request) {
		req.Header.Set("Authorization", "DPoP "+accessToken)
		req.Header.Set("DPoP", signProof(t, opts))
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Dpop-Nonce") == "" {
		t.Fatalf("successful response should rotate DPoP-Nonce")
	}
}

func TestEdDSAProofAccepted(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	accessToken := mintAccessToken(t, mustThumbprint(t, keys.edPublic))
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodGet, htu, clock.t)
	opts.alg = "EdDSA"
	opts.jwk = keys.edPublic
	opts.signer = keys.edPrivate

	req := newRequest(t, http.MethodGet, htu, "DPoP", accessToken, signProof(t, opts))
	if _, err := verifier.VerifyRequest(req); err != nil {
		t.Fatalf("EdDSA proof rejected: %v", err)
	}
}

func TestPrivateJWKMemberRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodGet, htu, clock.t)

	dBytes := make([]byte, 32)
	opts.rawHeader = map[string]interface{}{
		"typ": "dpop+jwt",
		"alg": "ES256",
		"jwk": map[string]interface{}{
			"kty": "EC", "crv": "P-256",
			"x": keys.ecPublic.X, "y": keys.ecPublic.Y,
			"d": b64url(dBytes),
		},
	}
	req := newRequest(t, http.MethodGet, htu, "DPoP", accessToken, signProof(t, opts))
	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
}

func TestSymmetricAndNoneAlgorithmsRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	for _, alg := range []string{"HS256", "none", "RS256"} {
		accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
		htu := "https://api.example.com/resource/1"
		opts := defaultProofOptions(t, keys, accessToken, http.MethodGet, htu, clock.t)
		opts.rawHeader = map[string]interface{}{
			"typ": "dpop+jwt",
			"alg": alg,
			"jwk": keys.ecPublic,
		}
		req := newRequest(t, http.MethodGet, htu, "DPoP", accessToken, signProof(t, opts))
		_, err := verifier.VerifyRequest(req)
		assertDPoPError(t, err, dpop.ErrInvalidDPoP)
	}
}

func TestTypMustBeDPoPJWT(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	req, accessToken := validRequest(t, keys, clock, nil)
	htu := "https://api.example.com/resource/1"
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, htu, clock.t)
	opts.typ = "jwt"
	req.Header.Set("DPoP", signProof(t, opts))

	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
}

func TestBearerDowngradeHardRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	jkt := mustThumbprint(t, keys.ecPublic)
	accessToken := mintAccessToken(t, jkt)

	// Bound token presented as Bearer must be rejected even without a proof.
	req := newRequest(t, http.MethodGet, "https://api.example.com/resource/1", "Bearer", accessToken, "")
	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidToken)

	// A smuggled second Authorization header is rejected regardless of order.
	req2 := newRequest(t, http.MethodGet, "https://api.example.com/resource/1", "DPoP", accessToken, "")
	req2.Header.Add("Authorization", "Bearer "+accessToken)
	_, err = verifier.VerifyRequest(req2)
	assertDPoPError(t, err, dpop.ErrInvalidToken)
}

func TestMultipleDPoPHeadersRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	req, _ := validRequest(t, keys, clock, nil)
	req.Header.Add("DPoP", "eyJhbGciOiJFUzI1NiJ9.e30.sig")
	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
}

func assertDPoPError(t *testing.T, err error, wantCode string) {
	t.Helper()
	dpopErr := dpop.AsError(err)
	if dpopErr == nil {
		t.Fatalf("expected *dpop.Error, got %v", err)
	}
	if dpopErr.Code != wantCode {
		t.Fatalf("error code = %q, want %q (%s)", dpopErr.Code, wantCode, dpopErr.Description)
	}
}
