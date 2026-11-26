package spiffefed_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	spiffefed "formal.local/w27-6accbac653"
)

func TestJWTSVIDFederationAndCrossDomainKeyIsolation(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	audience := "spiffe://local.example/core"
	localKey := mustECDSAKey(t)
	externalKey := mustECDSAKey(t)

	localJWKS := testJWKS(t, "local.example", map[string]*ecdsa.PrivateKey{
		"shared":     localKey,
		"local-only": localKey,
	})
	externalJWKS := testJWKS(t, "external.example", map[string]*ecdsa.PrivateKey{
		"shared":        externalKey,
		"external-only": externalKey,
	})
	ring, err := spiffefed.NewJWTKeyRing(localJWKS, externalJWKS)
	if err != nil {
		t.Fatalf("key ring: %v", err)
	}
	verifier, err := spiffefed.NewJWTSVIDVerifier(ring, audience)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	verifier.Policy = mustPolicy(t, "local.example", map[string]spiffefed.ServicePolicy{
		"core":    {FederatesWith: []string{"external.example"}},
		"private": {},
	})

	t.Run("valid local token", func(t *testing.T) {
		token := signTestJWT(t, localKey, "local-only", nil, testClaims(
			"spiffe://local.example/ns/core/sa/proxy", audience, now, 5*time.Minute,
		))
		got, err := verifier.VerifyForServiceAt(token, "core", now)
		if err != nil {
			t.Fatalf("verify local token: %v", err)
		}
		if got.String() != "spiffe://local.example/ns/core/sa/proxy" {
			t.Fatalf("unexpected subject %q", got)
		}
	})

	t.Run("valid federated token", func(t *testing.T) {
		token := signTestJWT(t, externalKey, "external-only", nil, testClaims(
			"spiffe://external.example/ns/app/sa/client", audience, now, 5*time.Minute,
		))
		if _, err := verifier.VerifyForServiceAt(token, "core", now); err != nil {
			t.Fatalf("verify federated token: %v", err)
		}
	})

	t.Run("cross-domain kid collision local subject", func(t *testing.T) {
		token := signTestJWT(t, externalKey, "shared", nil, testClaims(
			"spiffe://local.example/ns/core/sa/proxy", audience, now, 5*time.Minute,
		))
		if _, err := verifier.VerifyForServiceAt(token, "core", now); !errors.Is(err, spiffefed.ErrSignature) {
			t.Fatalf("error = %v, want ErrSignature", err)
		}
	})

	t.Run("cross-domain kid collision external subject", func(t *testing.T) {
		token := signTestJWT(t, localKey, "shared", nil, testClaims(
			"spiffe://external.example/ns/app/sa/client", audience, now, 5*time.Minute,
		))
		if _, err := verifier.VerifyForServiceAt(token, "core", now); !errors.Is(err, spiffefed.ErrSignature) {
			t.Fatalf("error = %v, want ErrSignature", err)
		}
	})

	t.Run("kid from another domain is not searched", func(t *testing.T) {
		token := signTestJWT(t, externalKey, "external-only", nil, testClaims(
			"spiffe://local.example/ns/core/sa/proxy", audience, now, 5*time.Minute,
		))
		if _, err := verifier.VerifyForServiceAt(token, "core", now); !errors.Is(err, spiffefed.ErrUnknownKey) {
			t.Fatalf("error = %v, want ErrUnknownKey", err)
		}
	})

	t.Run("audience mismatch", func(t *testing.T) {
		token := signTestJWT(t, localKey, "local-only", nil, testClaims(
			"spiffe://local.example/ns/core/sa/proxy", "spiffe://local.example/other", now, 5*time.Minute,
		))
		if _, err := verifier.VerifyForServiceAt(token, "core", now); !errors.Is(err, spiffefed.ErrAudience) {
			t.Fatalf("error = %v, want ErrAudience", err)
		}
	})

	t.Run("audience array exact hit", func(t *testing.T) {
		claims := testClaims("spiffe://local.example/ns/core/sa/proxy", "unused", now, 5*time.Minute)
		claims["aud"] = []string{"spiffe://local.example/other", audience}
		token := signTestJWT(t, localKey, "local-only", nil, claims)
		if _, err := verifier.VerifyForServiceAt(token, "core", now); err != nil {
			t.Fatalf("verify audience array: %v", err)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		token := signTestJWT(t, localKey, "local-only", nil, testClaims(
			"spiffe://local.example/ns/core/sa/proxy", audience, now, -time.Minute,
		))
		if _, err := verifier.VerifyForServiceAt(token, "core", now); !errors.Is(err, spiffefed.ErrTokenLifetime) {
			t.Fatalf("error = %v, want ErrTokenLifetime", err)
		}
	})

	t.Run("overlong token", func(t *testing.T) {
		token := signTestJWT(t, localKey, "local-only", nil, testClaims(
			"spiffe://local.example/ns/core/sa/proxy", audience, now, 25*time.Hour,
		))
		if _, err := verifier.VerifyForServiceAt(token, "core", now); !errors.Is(err, spiffefed.ErrTokenLifetime) {
			t.Fatalf("error = %v, want ErrTokenLifetime", err)
		}
	})

	t.Run("federated service must be explicit", func(t *testing.T) {
		token := signTestJWT(t, externalKey, "external-only", nil, testClaims(
			"spiffe://external.example/ns/app/sa/client", audience, now, 5*time.Minute,
		))
		if _, err := verifier.VerifyForServiceAt(token, "private", now); !errors.Is(err, spiffefed.ErrFederationDenied) {
			t.Fatalf("error = %v, want ErrFederationDenied", err)
		}
		if _, err := verifier.VerifyAt(token, now); !errors.Is(err, spiffefed.ErrTargetServiceRequired) {
			t.Fatalf("no-service error = %v, want ErrTargetServiceRequired", err)
		}
	})

	t.Run("normalization bypass in sub", func(t *testing.T) {
		token := signTestJWT(t, localKey, "local-only", nil, testClaims(
			"spiffe://local.example/ns/%2e%2e/core/admin", audience, now, 5*time.Minute,
		))
		if _, err := verifier.VerifyForServiceAt(token, "core", now); !errors.Is(err, spiffefed.ErrInvalidJWT) {
			t.Fatalf("error = %v, want ErrInvalidJWT", err)
		}
	})

	t.Run("embedded key header", func(t *testing.T) {
		header := map[string]any{"alg": "ES256", "typ": "JWT", "kid": "local-only", "jwk": map[string]any{"kty": "EC"}}
		token := signTestJWT(t, localKey, "local-only", header, testClaims(
			"spiffe://local.example/ns/core/sa/proxy", audience, now, 5*time.Minute,
		))
		if _, err := verifier.VerifyForServiceAt(token, "core", now); !errors.Is(err, spiffefed.ErrInvalidJWT) {
			t.Fatalf("error = %v, want ErrInvalidJWT", err)
		}
	})
}

func TestParseJWKSBindsDomainAndRejectsAmbiguity(t *testing.T) {
	t.Parallel()
	key := mustECDSAKey(t)
	x := make([]byte, 32)
	y := make([]byte, 32)
	key.PublicKey.X.FillBytes(x)
	key.PublicKey.Y.FillBytes(y)
	jwk := map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"kid": "parsed-key",
		"use": "sig",
		"alg": "ES256",
		"x":   base64.RawURLEncoding.EncodeToString(x),
		"y":   base64.RawURLEncoding.EncodeToString(y),
	}
	doc := map[string]any{"keys": []any{jwk}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	set, err := spiffefed.ParseJWKS("local.example", data)
	if err != nil {
		t.Fatalf("parse JWKS: %v", err)
	}
	ring, err := spiffefed.NewJWTKeyRing(set)
	if err != nil {
		t.Fatalf("key ring: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	verifier, err := spiffefed.NewJWTSVIDVerifier(ring, "spiffe://local.example/core")
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	token := signTestJWT(t, key, "parsed-key", nil, testClaims(
		"spiffe://local.example/workload", "spiffe://local.example/core", now, time.Minute,
	))
	if _, err := verifier.VerifyAt(token, now); err != nil {
		t.Fatalf("verify parsed JWKS token: %v", err)
	}

	duplicate := map[string]any{"keys": []any{jwk, jwk}}
	duplicateData, err := json.Marshal(duplicate)
	if err != nil {
		t.Fatalf("marshal duplicate JWKS: %v", err)
	}
	if _, err := spiffefed.ParseJWKS("local.example", duplicateData); !errors.Is(err, spiffefed.ErrInvalidJWKS) {
		t.Fatalf("duplicate kid error = %v, want ErrInvalidJWKS", err)
	}

	mismatched := map[string]any{"kty": "EC", "crv": "P-256", "kid": "bad-alg", "alg": "ES384", "x": jwk["x"], "y": jwk["y"]}
	mismatchedData, err := json.Marshal(map[string]any{"keys": []any{mismatched}})
	if err != nil {
		t.Fatalf("marshal mismatched JWKS: %v", err)
	}
	if _, err := spiffefed.ParseJWKS("local.example", mismatchedData); !errors.Is(err, spiffefed.ErrInvalidJWKS) {
		t.Fatalf("mismatched alg error = %v, want ErrInvalidJWKS", err)
	}
}

func TestJWTSVIDRejectsUnsupportedAlgorithmsBeforeSignature(t *testing.T) {
	t.Parallel()
	key := mustECDSAKey(t)
	set := testJWKS(t, "local.example", map[string]*ecdsa.PrivateKey{"k": key})
	ring, err := spiffefed.NewJWTKeyRing(set)
	if err != nil {
		t.Fatalf("key ring: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	audience := "spiffe://local.example/core"
	verifier, err := spiffefed.NewJWTSVIDVerifier(ring, audience)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	claims := testClaims("spiffe://local.example/workload", audience, now, time.Minute)
	for _, header := range []map[string]any{
		{"alg": "none", "typ": "JWT", "kid": "k"},
		{"alg": "HS256", "typ": "JWT", "kid": "k"},
		{"alg": "ES256", "typ": "JWT", "kid": "k", "b64": false},
		{"alg": "ES256", "typ": "JWT", "kid": "k", "crit": []string{"exp"}},
	} {
		token := signTestJWT(t, key, "k", header, claims)
		if _, err := verifier.VerifyAt(token, now); !errors.Is(err, spiffefed.ErrInvalidJWT) {
			t.Fatalf("header %v error = %v, want ErrInvalidJWT", header, err)
		}
	}

	unsigned := fmt.Sprintf("%s.%s.",
		base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"k"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"sub":"spiffe://local.example/workload","aud":%q,"exp":%d}`,
			audience, now.Add(time.Minute).Unix()))),
	)
	if _, err := verifier.VerifyAt(unsigned, now); !errors.Is(err, spiffefed.ErrInvalidJWT) {
		t.Fatalf("unsigned token error = %v, want ErrInvalidJWT", err)
	}
}

func mustECDSAKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	return key
}
