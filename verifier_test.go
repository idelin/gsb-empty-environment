package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestIssuerSwapAttackRejected reproduces the vulnerability described in the
// task: an attacker keeps issuer A's signing key (so the JWS signature is
// cryptographically valid) but changes the "iss" claim to issuer B. The
// verifier for B must reject it before B's cached key can come into play.
func TestIssuerSwapAttackRejected(t *testing.T) {
	for _, alg := range []string{"RS256", "ES256", "EdDSA"} {
		t.Run(alg, func(t *testing.T) {
			issuerA := newTestIssuer(t, alg)
			defer issuerA.close()
			issuerB := newTestIssuer(t, alg)
			defer issuerB.close()

			// Two sessions, each pinned to one issuer's own keys. This mirrors
			// a deployment where keys are cached per session, and it guarantees
			// no verifier can ever see another issuer's key.
			verifierA, err := NewVerifier(Config{
				Issuer: issuerA.issuer,
				Keys:   issuerA.keySet(t),
			})
			if err != nil {
				t.Fatalf("verifier A: %v", err)
			}
			verifierB, err := NewVerifier(Config{
				Issuer: issuerB.issuer,
				Keys:   issuerB.keySet(t),
			})
			if err != nil {
				t.Fatalf("verifier B: %v", err)
			}

			// Case 1 (the headline attack): keep B's signing key exactly, but
			// rewrite the "iss" claim to A. Presented to B's session, the
			// signature is cryptographically valid with B's locally cached key
			// - a verifier that ignores iss would accept it. The issuer binding
			// check must reject it before the key is ever consulted.
			token := issuerB.mint(t, standardClaims(issuerA.issuer))
			if _, err := verifierB.Verify(context.Background(), token); !errors.Is(err, ErrIssuerMismatch) {
				t.Fatalf("expected ErrIssuerMismatch for rewritten iss, got %v", err)
			}

			// Case 2 (cross-issuer key confusion): token claims to be B but is
			// signed with A's key. iss matches B, yet B's isolated keys cannot
			// validate A's signature.
			token = issuerA.mint(t, standardClaims(issuerB.issuer))
			if _, err := verifierB.Verify(context.Background(), token); !errors.Is(err, ErrKeyNotFound) {
				t.Fatalf("expected ErrKeyNotFound for foreign key, got %v", err)
			}

			// Control: each issuer's own token still verifies with its own
			// verifier, proving the checks do not break legitimate flows.
			if _, err := verifierA.Verify(context.Background(),
				issuerA.mint(t, standardClaims(issuerA.issuer))); err != nil {
				t.Fatalf("issuer A legitimate token rejected: %v", err)
			}
			if _, err := verifierB.Verify(context.Background(),
				issuerB.mint(t, standardClaims(issuerB.issuer))); err != nil {
				t.Fatalf("issuer B legitimate token rejected: %v", err)
			}
		})
	}
}

// TestConsistentIssuerAndKeyAccepted confirms the legitimate path: when the
// issuer and the signing key come from the same provider, verification works.
func TestConsistentIssuerAndKeyAccepted(t *testing.T) {
	for _, alg := range []string{"RS256", "ES256", "EdDSA"} {
		t.Run(alg, func(t *testing.T) {
			provider := newTestIssuer(t, alg)
			defer provider.close()

			verifier := provider.verifier(t)
			token := provider.mint(t, standardClaims(provider.issuer))

			got, err := verifier.Verify(context.Background(), token)
			if err != nil {
				t.Fatalf("expected valid token, got %v", err)
			}
			if got.Issuer != provider.issuer {
				t.Fatalf("issuer = %q, want %q", got.Issuer, provider.issuer)
			}
			if got.Subject != "subject-1" || got.KeyID != provider.kid || got.Alg != alg {
				t.Fatalf("unexpected parsed token: %+v", got)
			}
		})
	}
}

// TestStaticKeySetIssuerIsolation uses pinned keys and proves a key cached for
// one verifier never validates a token addressed to a different issuer.
func TestStaticKeySetIssuerIsolation(t *testing.T) {
	provider := newTestIssuer(t, "RS256")
	defer provider.close()

	verifier, err := NewVerifier(Config{
		Issuer: provider.issuer,
		Keys:   provider.keySet(t),
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if _, err := verifier.Verify(context.Background(),
		provider.mint(t, standardClaims(provider.issuer))); err != nil {
		t.Fatalf("same-issuer token rejected: %v", err)
	}

	foreign := standardClaims("https://idp.other.example")
	_, err = verifier.Verify(context.Background(), provider.mint(t, foreign))
	if !errors.Is(err, ErrIssuerMismatch) {
		t.Fatalf("expected ErrIssuerMismatch, got %v", err)
	}
}

// TestMissingIssuerClaimRejected ensures an empty/absent iss is never treated
// as the configured issuer.
func TestMissingIssuerClaimRejected(t *testing.T) {
	provider := newTestIssuer(t, "ES256")
	defer provider.close()

	claims := standardClaims(provider.issuer)
	delete(claims, "iss")
	_, err := provider.verifier(t).Verify(context.Background(), provider.mint(t, claims))
	if !errors.Is(err, ErrIssuerMismatch) {
		t.Fatalf("expected ErrIssuerMismatch, got %v", err)
	}
}

// TestAudienceAndNonce checks the optional claim enforcement.
func TestAudienceAndNonce(t *testing.T) {
	provider := newTestIssuer(t, "RS256")
	defer provider.close()

	verifier, err := NewVerifier(Config{
		Issuer:   provider.issuer,
		Audience: []string{"client-id"},
		Nonce:    "nonce-123",
		Keys:     provider.keySet(t),
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	good := standardClaims(provider.issuer)
	good["nonce"] = "nonce-123"
	if _, err := verifier.Verify(context.Background(), provider.mint(t, good)); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}

	wrongNonce := standardClaims(provider.issuer)
	wrongNonce["nonce"] = "other"
	if _, err := verifier.Verify(context.Background(), provider.mint(t, wrongNonce)); !errors.Is(err, ErrClaim) {
		t.Fatalf("expected ErrClaim for nonce, got %v", err)
	}

	wrongAud := standardClaims(provider.issuer)
	wrongAud["aud"] = []string{"someone-else"}
	wrongAud["nonce"] = "nonce-123"
	if _, err := verifier.Verify(context.Background(), provider.mint(t, wrongAud)); !errors.Is(err, ErrClaim) {
		t.Fatalf("expected ErrClaim for audience, got %v", err)
	}
}

// TestLifetimeClaims exercises exp/nbf with leeway and a controllable clock.
func TestLifetimeClaims(t *testing.T) {
	provider := newTestIssuer(t, "RS256")
	defer provider.close()

	base := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	verifier, err := NewVerifier(Config{
		Issuer: provider.issuer,
		Keys:   provider.keySet(t),
		Now:    func() time.Time { return base },
		Leeway: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := standardClaims(provider.issuer)
	claims["iat"] = float64(base.Unix())
	claims["nbf"] = float64(base.Add(-time.Minute).Unix())
	claims["exp"] = float64(base.Add(time.Minute).Unix())
	if _, err := verifier.Verify(context.Background(), provider.mint(t, claims)); err != nil {
		t.Fatalf("valid lifetime rejected: %v", err)
	}

	expired := standardClaims(provider.issuer)
	expired["exp"] = float64(base.Add(-time.Minute).Unix())
	if _, err := verifier.Verify(context.Background(), provider.mint(t, expired)); !errors.Is(err, ErrClaim) {
		t.Fatalf("expected ErrClaim for expiry, got %v", err)
	}
}

// TestMalformedAndAlgNone ensures structurally bad tokens and the "none"
// algorithm are rejected without touching any key.
func TestMalformedAndAlgNone(t *testing.T) {
	provider := newTestIssuer(t, "RS256")
	defer provider.close()
	verifier := provider.verifier(t)

	for _, token := range []string{"", "a.b", "a.b.c.d", "..", "aaa.bbb.ccc"} {
		if _, err := verifier.Verify(context.Background(), token); !errors.Is(err, ErrMalformedToken) {
			t.Fatalf("token %q: expected ErrMalformedToken, got %v", token, err)
		}
	}

	header := map[string]string{"alg": "none", "kid": provider.kid, "typ": "JWT"}
	payload := standardClaims(provider.issuer)
	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(payload)
	unsigned := encodeSegment(headerJSON) + "." + encodeSegment(payloadJSON) + "."
	if _, err := verifier.Verify(context.Background(), unsigned); !errors.Is(err, ErrUnsupportedAlg) {
		t.Fatalf("expected ErrUnsupportedAlg for none, got %v", err)
	}
}

// TestUnknownKidReturnsKeyNotFound proves a token cannot fall back to some
// other cached key when its kid is absent from the issuer's KeySet.
func TestUnknownKidReturnsKeyNotFound(t *testing.T) {
	provider := newTestIssuer(t, "RS256")
	defer provider.close()

	header := map[string]string{"alg": "RS256", "kid": "kid-does-not-exist", "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)
	payloadJSON, _ := json.Marshal(standardClaims(provider.issuer))
	token, err := sign("RS256", provider.key, headerJSON, payloadJSON)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	_, err = provider.verifier(t).Verify(context.Background(), token)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

// TestDiscoveryRejectsForeignJWKSPin ensures the JWKS endpoint cannot point at
// a different origin than the configured issuer.
func TestDiscoveryRejectsForeignJWKSPin(t *testing.T) {
	good := newTestIssuer(t, "RS256")
	defer good.close()

	rogueIssuer := "https://rogue.idp.example"
	rogueClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		payload, _ := json.Marshal(map[string]string{
			"issuer":   rogueIssuer,
			"jwks_uri": "https://evil.example/jwks",
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(payload))),
			Request:    r,
		}, nil
	})}

	verifier, err := NewVerifier(Config{Issuer: rogueIssuer, HTTPClient: rogueClient})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	_, err = verifier.Verify(context.Background(), good.mint(t, standardClaims(rogueIssuer)))
	if err == nil {
		t.Fatal("expected discovery to reject foreign jwks_uri")
	}
}
