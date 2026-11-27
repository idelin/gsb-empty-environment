package dpop_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"testing"
	"time"

	"formal.local/w28-dd7127bdf9/dpop"
)

func TestBaseURLHTUPinnedBehindProxy(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	htuFunc, err := dpop.BaseURLHTU("https://api.example.com")
	if err != nil {
		t.Fatalf("BaseURLHTU: %v", err)
	}
	verifier, err := dpop.NewVerifier(dpop.Options{
		HTUFunc: htuFunc,
		Now:     clock.now,
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	keys := loadTestKeys(t)

	// Internal request URL uses the upstream host, but the proof is bound to
	// the external gateway base URL.
	accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
	opts := defaultProofOptions(t, keys, accessToken, http.MethodPost, "https://api.example.com/v1/orders", clock.t)
	req := newRequest(t, http.MethodPost, "http://upstream.internal:8080/v1/orders?x=1", "DPoP", accessToken, signProof(t, opts))

	if _, err := verifier.VerifyRequest(req); err != nil {
		t.Fatalf("pinned base URL htu should verify: %v", err)
	}
}

func TestBaseURLHTURejectsInvalidBase(t *testing.T) {
	for _, base := range []string{"", "api.example.com", "https://api.example.com?x=1", "https://api.example.com#f"} {
		if _, err := dpop.BaseURLHTU(base); err == nil {
			t.Fatalf("base %q should be rejected", base)
		}
	}
}

func TestDuplicateJSONMembersRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	req, accessToken := validRequest(t, keys, clock, nil)

	// Craft a JWK header with a duplicated "x" member to smuggle ambiguity.
	headerJSON := `{"typ":"dpop+jwt","alg":"ES256","jwk":{"kty":"EC","crv":"P-256","x":"` +
		keys.ecPublic.X + `","y":"` + keys.ecPublic.Y + `","x":"` + b64url(make([]byte, 32)) + `"}}`
	payload := `{"htm":"POST","htu":"https://api.example.com/resource/1","ath":"` +
		mustATH(accessToken) + `","jti":"dup-jti","iat":` + int64ToString(clock.t.Unix()) + `}`

	compact := b64url([]byte(headerJSON)) + "." + b64url([]byte(payload)) + "." + b64url(make([]byte, 64))
	req.Header.Set("DPoP", compact)

	_, err := verifier.VerifyRequest(req)
	assertDPoPError(t, err, dpop.ErrInvalidDPoP)
}

func TestMalformedCompactJWTRejected(t *testing.T) {
	clock := &fixedClock{t: time.Unix(1_700_000_000, 0)}
	verifier := newVerifier(t, clock, nil)
	keys := loadTestKeys(t)

	for _, proof := range []string{
		"not-a-jwt",
		"a.b",
		"a.b.c.d",
		"###.###.###",
		b64url([]byte(`{"typ":"dpop+jwt"}`)) + ".e30.sig",
	} {
		accessToken := mintAccessToken(t, mustThumbprint(t, keys.ecPublic))
		req := newRequest(t, http.MethodPost, "https://api.example.com/resource/1", "DPoP", accessToken, proof)
		_, err := verifier.VerifyRequest(req)
		assertDPoPError(t, err, dpop.ErrInvalidDPoP)
	}
}

func mustATH(accessToken string) string {
	sum := sha256Token(accessToken)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func sha256Token(accessToken string) [32]byte {
	return sha256.Sum256([]byte(accessToken))
}

func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
