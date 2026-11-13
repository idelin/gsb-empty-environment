package netsec_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	netsec "example.org/netsec/q0165"
)

const (
	testURI    = "https://api.example.com/orders"
	testMethod = http.MethodGet
)

func TestValidBindingWithMatchingHTMAndHTUAllowed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key, jwk, jkt := newECSigner(t)
	token := accessToken(t, map[string]any{
		"sub": "alice",
		"cnf": map[string]any{"jkt": jkt},
	})
	proof := makeProof(t, key, jwk, testMethod, testURI, now, "proof-1")

	result, err := netsec.ValidateDPoP(token, proof, testMethod, testURI, at(now))
	if err != nil {
		t.Fatalf("valid DPoP request rejected: %v", err)
	}
	if result.JKT != jkt {
		t.Fatalf("result JKT = %q, want %q", result.JKT, jkt)
	}
}

func TestSwappedProofKeyRejectsUnchangedToken(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	boundKey, boundJWK, boundJKT := newECSigner(t)
	attackerKey, attackerJWK, attackerJKT := newECSigner(t)

	token := accessToken(t, map[string]any{
		"sub": "alice",
		"cnf": map[string]any{"jkt": boundJKT},
	})
	if attackerJKT == boundJKT {
		t.Fatal("test keys unexpectedly have the same thumbprint")
	}

	validProof := makeProof(t, boundKey, boundJWK, testMethod, testURI, now, "proof-valid")
	if _, err := netsec.ValidateDPoP(token, validProof, testMethod, testURI, at(now)); err != nil {
		t.Fatalf("bound proof should be accepted: %v", err)
	}

	swappedProof := makeProof(t, attackerKey, attackerJWK, testMethod, testURI, now, "proof-swapped")
	result, err := netsec.ValidateDPoP(token, swappedProof, testMethod, testURI, at(now))
	if err == nil {
		t.Fatalf("proof signed by a different key was accepted: %#v", result)
	}
	if !errors.Is(err, netsec.ErrBindingMismatch) {
		t.Fatalf("error = %v, want ErrBindingMismatch", err)
	}
}

func TestTokenWithoutJKTMustRejectProofWithKey(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key, jwk, _ := newECSigner(t)
	proof := makeProof(t, key, jwk, testMethod, testURI, now, "proof-no-binding")

	tests := []struct {
		name   string
		claims map[string]any
	}{
		{
			name:   "no cnf",
			claims: map[string]any{"sub": "alice"},
		},
		{
			name:   "empty cnf",
			claims: map[string]any{"sub": "alice", "cnf": map[string]any{}},
		},
		{
			name:   "empty jkt",
			claims: map[string]any{"sub": "alice", "cnf": map[string]any{"jkt": ""}},
		},
		{
			name:   "non-string jkt",
			claims: map[string]any{"sub": "alice", "cnf": map[string]any{"jkt": 42}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := accessToken(t, tc.claims)
			result, err := netsec.ValidateDPoP(token, proof, testMethod, testURI, at(now))
			if err == nil {
				t.Fatalf("unbound token was accepted: %#v", result)
			}
			if !errors.Is(err, netsec.ErrTokenNotBound) {
				t.Fatalf("error = %v, want ErrTokenNotBound", err)
			}
		})
	}
}

func TestHTMAndHTUMustMatch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key, jwk, jkt := newECSigner(t)
	token := accessToken(t, map[string]any{
		"sub": "alice",
		"cnf": map[string]any{"jkt": jkt},
	})

	t.Run("method mismatch", func(t *testing.T) {
		proof := makeProof(t, key, jwk, http.MethodGet, testURI, now, "proof-method")
		_, err := netsec.ValidateDPoP(token, proof, http.MethodPost, testURI, at(now))
		if !errors.Is(err, netsec.ErrMethodMismatch) {
			t.Fatalf("error = %v, want ErrMethodMismatch", err)
		}
	})

	t.Run("URI mismatch", func(t *testing.T) {
		proof := makeProof(t, key, jwk, testMethod, testURI, now, "proof-uri")
		_, err := netsec.ValidateDPoP(token, proof, testMethod, "https://api.example.com/admin", at(now))
		if !errors.Is(err, netsec.ErrURIMismatch) {
			t.Fatalf("error = %v, want ErrURIMismatch", err)
		}
	})

	t.Run("query string is not part of htu", func(t *testing.T) {
		proof := makeProof(t, key, jwk, testMethod, testURI, now, "proof-query")
		if _, err := netsec.ValidateDPoP(token, proof, testMethod, testURI+"?limit=20", at(now)); err != nil {
			t.Fatalf("query string should not affect htu matching: %v", err)
		}
	})
}

func TestHTTPRequestIntegration(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key, jwk, jkt := newECSigner(t)
	token := accessToken(t, map[string]any{
		"sub": "alice",
		"cnf": map[string]any{"jkt": jkt},
	})
	proof := makeProof(t, key, jwk, testMethod, testURI, now, "proof-http")

	req := httptest.NewRequest(testMethod, testURI, nil)
	req.Header.Set("Authorization", "DPoP "+token)
	req.Header.Set("DPoP", proof)

	result, err := netsec.ValidateRequest(req, "", at(now))
	if err != nil {
		t.Fatalf("valid HTTP DPoP request rejected: %v", err)
	}
	if result.JKT != jkt {
		t.Fatalf("result JKT = %q, want %q", result.JKT, jkt)
	}
}

func TestExpiredProofRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key, jwk, jkt := newECSigner(t)
	token := accessToken(t, map[string]any{
		"sub": "alice",
		"cnf": map[string]any{"jkt": jkt},
	})
	proof := makeProof(t, key, jwk, testMethod, testURI, now.Add(-time.Hour), "proof-old")

	_, err := netsec.ValidateDPoP(token, proof, testMethod, testURI, at(now))
	if !errors.Is(err, netsec.ErrProofExpired) {
		t.Fatalf("error = %v, want ErrProofExpired", err)
	}
}

func TestRFC7638ThumbprintVector(t *testing.T) {
	const jwk = `{
		"kty": "RSA",
		"n": "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw",
		"e": "AQAB",
		"alg": "RS256",
		"kid": "2011-04-29"
	}`
	const want = "NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs"

	got, err := netsec.JWKThumbprint(json.RawMessage(jwk))
	if err != nil {
		t.Fatalf("JWKThumbprint: %v", err)
	}
	if got != want {
		t.Fatalf("thumbprint = %q, want %q", got, want)
	}
}

func TestPrivateMaterialInProofJWKRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key, jwk, jkt := newECSigner(t)
	token := accessToken(t, map[string]any{
		"sub": "alice",
		"cnf": map[string]any{"jkt": jkt},
	})

	var members map[string]any
	if err := json.Unmarshal(jwk, &members); err != nil {
		t.Fatal(err)
	}
	members["d"] = base64.RawURLEncoding.EncodeToString(key.D.Bytes())
	jwkWithPrivate, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	proof := makeProof(t, key, jwkWithPrivate, testMethod, testURI, now, "proof-private")

	_, err = netsec.ValidateDPoP(token, proof, testMethod, testURI, at(now))
	if !errors.Is(err, netsec.ErrInvalidJWK) {
		t.Fatalf("error = %v, want ErrInvalidJWK", err)
	}
}

func at(now time.Time) netsec.Option {
	return netsec.WithNow(func() time.Time { return now })
}

func newECSigner(t *testing.T) (*ecdsa.PrivateKey, json.RawMessage, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	size := (key.Curve.Params().BitSize + 7) / 8
	jwk := map[string]string{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(fixedBytes(key.X, size)),
		"y":   base64.RawURLEncoding.EncodeToString(fixedBytes(key.Y, size)),
	}
	raw, err := json.Marshal(jwk)
	if err != nil {
		t.Fatalf("marshal JWK: %v", err)
	}
	jkt, err := netsec.JWKThumbprint(raw)
	if err != nil {
		t.Fatalf("JWKThumbprint: %v", err)
	}
	return key, raw, jkt
}

func fixedBytes(value *big.Int, size int) []byte {
	out := make([]byte, size)
	value.FillBytes(out)
	return out
}

func accessToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signature := base64.RawURLEncoding.EncodeToString([]byte("issuer-signature"))
	return encodeSegment(header) + "." + encodeSegment(payload) + "." + signature
}

func makeProof(t *testing.T, key *ecdsa.PrivateKey, jwk json.RawMessage, method, uri string, iat time.Time, jti string) string {
	t.Helper()
	header := map[string]any{
		"typ": "dpop+jwt",
		"alg": "ES256",
		"jwk": jwk,
	}
	claims := map[string]any{
		"htm": method,
		"htu": uri,
		"iat": iat.Unix(),
		"jti": jti,
	}
	return signES256(t, key, header, claims)
}

func signES256(t *testing.T, key *ecdsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := encodeSegment(headerJSON) + "." + encodeSegment(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("ecdsa.Sign: %v", err)
	}
	size := (key.Curve.Params().BitSize + 7) / 8
	signature := make([]byte, 2*size)
	r.FillBytes(signature[:size])
	s.FillBytes(signature[size:])
	return signingInput + "." + encodeSegment(signature)
}

func encodeSegment(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
