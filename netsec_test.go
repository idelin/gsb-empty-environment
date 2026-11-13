package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testSecret = "test-access-token-secret"

func TestMiddlewareAllowsPlainBearerWithoutCNF(t *testing.T) {
	authenticator := MustNew(Config{HMACSecret: []byte(testSecret)})
	token := signHS256(t, map[string]any{
		"sub": "alice",
		"exp": time.Now().Add(time.Hour).Unix(),
	}, []byte(testSecret))

	req := httptest.NewRequest(http.MethodGet, "https://resource.example/items", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFromContext(r.Context())
		if !ok || identity.Subject != "alice" {
			t.Fatalf("identity missing from context: %#v, %v", identity, ok)
		}
		if identity.Proof != nil || identity.Certificate != nil {
			t.Fatal("plain bearer token unexpectedly has proof material")
		}
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("plain bearer status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestCNFTokenWithoutProofIsRejectedAfterProofWasPresent(t *testing.T) {
	proofKey := newProofKey(t)
	token := signHS256(t, map[string]any{
		"sub": "alice",
		"exp": time.Now().Add(time.Hour).Unix(),
		"cnf": map[string]any{"jkt": proofKey.thumbprint},
	}, []byte(testSecret))
	authenticator := MustNew(Config{
		HMACSecret:      []byte(testSecret),
		DPoPReplayCache: NoReplayCache{},
	})

	withProof := httptest.NewRequest(http.MethodGet, "https://resource.example/items/42", nil)
	withProof.Header.Set("Authorization", "Bearer "+token)
	withProof.Header.Set("DPoP", proofKey.proof(t, http.MethodGet, "https://resource.example/items/42", token, ""))
	rec := httptest.NewRecorder()
	authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFromContext(r.Context())
		if !ok || identity.Proof == nil {
			t.Fatal("expected a verified DPoP proof in the request context")
		}
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, withProof)
	if rec.Code != http.StatusOK {
		t.Fatalf("token with proof status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	withoutProof := httptest.NewRequest(http.MethodGet, "https://resource.example/items/42", nil)
	withoutProof.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("resource handler must not run when cnf proof is missing")
	})).ServeHTTP(rec, withoutProof)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("token without proof status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if challenge := rec.Header().Get("WWW-Authenticate"); challenge == "" {
		t.Fatal("missing WWW-Authenticate challenge for cnf token without proof")
	}
}

func TestCNFTokenRejectsMismatchedOrWrongProof(t *testing.T) {
	proofKey := newProofKey(t)
	otherKey := newProofKey(t)
	token := signHS256(t, map[string]any{
		"sub": "alice",
		"exp": time.Now().Add(time.Hour).Unix(),
		"cnf": map[string]any{"jkt": proofKey.thumbprint},
	}, []byte(testSecret))
	authenticator := MustNew(Config{HMACSecret: []byte(testSecret), DPoPReplayCache: NoReplayCache{}})

	tests := []struct {
		name  string
		proof string
	}{
		{
			name:  "proof signed by a different key",
			proof: otherKey.proof(t, http.MethodGet, "https://resource.example/items/42", token, ""),
		},
		{
			name:  "proof bound to a different URI",
			proof: proofKey.proof(t, http.MethodGet, "https://resource.example/items/99", token, ""),
		},
		{
			name:  "proof with wrong ath",
			proof: proofKey.proof(t, http.MethodGet, "https://resource.example/items/42", token, "wrong-token"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://resource.example/items/42", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("DPoP", tc.proof)
			rec := httptest.NewRecorder()
			authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Fatal("resource handler must not run for an invalid proof")
			})).ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCNFTokenRejectsUnsupportedConfirmation(t *testing.T) {
	token := signHS256(t, map[string]any{
		"sub": "alice",
		"exp": time.Now().Add(time.Hour).Unix(),
		"cnf": map[string]any{},
	}, []byte(testSecret))
	authenticator := MustNew(Config{HMACSecret: []byte(testSecret), DPoPReplayCache: NoReplayCache{}})

	req := httptest.NewRequest(http.MethodGet, "https://resource.example/items", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("resource handler must not run for cnf without a binding")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestCertificateBoundTokenRequiresMatchingCertificate(t *testing.T) {
	cert := newClientCertificate(t)
	otherCert := newClientCertificate(t)
	sum := sha256.Sum256(cert.Raw)
	token := signHS256(t, map[string]any{
		"sub": "alice",
		"exp": time.Now().Add(time.Hour).Unix(),
		"cnf": map[string]any{"x5t#S256": base64.RawURLEncoding.EncodeToString(sum[:])},
	}, []byte(testSecret))
	authenticator := MustNew(Config{HMACSecret: []byte(testSecret), DPoPReplayCache: NoReplayCache{}})

	withCert := httptest.NewRequest(http.MethodGet, "https://resource.example/items", nil)
	withCert.Header.Set("Authorization", "Bearer "+token)
	withCert = withCert.WithContext(WithClientCertificate(withCert.Context(), cert))
	rec := httptest.NewRecorder()
	authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFromContext(r.Context())
		if !ok || identity.Certificate == nil {
			t.Fatal("expected the matching client certificate in the request context")
		}
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, withCert)
	if rec.Code != http.StatusOK {
		t.Fatalf("certificate-bound token status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	withoutCert := httptest.NewRequest(http.MethodGet, "https://resource.example/items", nil)
	withoutCert.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("resource handler must not run without the bound certificate")
	})).ServeHTTP(rec, withoutCert)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing certificate status = %d, want 401: %s", rec.Code, rec.Body.String())
	}

	wrongCert := httptest.NewRequest(http.MethodGet, "https://resource.example/items", nil)
	wrongCert.Header.Set("Authorization", "Bearer "+token)
	wrongCert = wrongCert.WithContext(WithClientCertificate(wrongCert.Context(), otherCert))
	rec = httptest.NewRecorder()
	authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("resource handler must not run with a mismatched certificate")
	})).ServeHTTP(rec, wrongCert)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("mismatched certificate status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestDPoPProofCannotBeReplayed(t *testing.T) {
	proofKey := newProofKey(t)
	token := signHS256(t, map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		"cnf": map[string]any{"jkt": proofKey.thumbprint},
	}, []byte(testSecret))
	authenticator := MustNew(Config{HMACSecret: []byte(testSecret)})
	proof := proofKey.proof(t, http.MethodGet, "https://resource.example/items", token, "")

	serve := func() int {
		req := httptest.NewRequest(http.MethodGet, "https://resource.example/items", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("DPoP", proof)
		rec := httptest.NewRecorder()
		authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)
		return rec.Code
	}

	if status := serve(); status != http.StatusOK {
		t.Fatalf("first proof use status = %d, want 200", status)
	}
	if status := serve(); status != http.StatusUnauthorized {
		t.Fatalf("replayed proof status = %d, want 401", status)
	}
}

type proofKey struct {
	private    *ecdsa.PrivateKey
	jwk        map[string]any
	thumbprint string
}

func newProofKey(t *testing.T) proofKey {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwk := map[string]any{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64.RawURLEncoding.EncodeToString(paddedBytes(private.PublicKey.X.Bytes(), 32)),
		"y":   base64.RawURLEncoding.EncodeToString(paddedBytes(private.PublicKey.Y.Bytes(), 32)),
	}
	thumbprint, err := JWKThumbprint(jwk)
	if err != nil {
		t.Fatal(err)
	}
	return proofKey{private: private, jwk: jwk, thumbprint: thumbprint}
}

func (k proofKey) proof(t *testing.T, method, htu, accessToken, athOverride string) string {
	t.Helper()
	claims := map[string]any{
		"htm": method,
		"htu": htu,
		"iat": time.Now().Unix(),
		"jti": "proof-" + time.Now().Format("150405.000000000"),
		"ath": sha256Base64URL([]byte(accessToken)),
	}
	if athOverride != "" {
		claims["ath"] = sha256Base64URL([]byte(athOverride))
	}
	header := map[string]any{"typ": "dpop+jwt", "alg": "ES256", "jwk": k.jwk}
	return signES256(t, header, claims, k.private)
}

func signHS256(t *testing.T, claims map[string]any, secret []byte) string {
	t.Helper()
	header := map[string]any{"typ": "JWT", "alg": "HS256"}
	input := encodedJSON(t, header) + "." + encodedJSON(t, claims)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func signES256(t *testing.T, header, claims map[string]any, private *ecdsa.PrivateKey) string {
	t.Helper()
	input := encodedJSON(t, header) + "." + encodedJSON(t, claims)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, private, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(paddedBytes(r.Bytes(), 32), paddedBytes(s.Bytes(), 32)...)
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func encodedJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func paddedBytes(value []byte, size int) []byte {
	if len(value) >= size {
		return value
	}
	out := make([]byte, size)
	copy(out[size-len(value):], value)
	return out
}

func newClientCertificate(t *testing.T) *x509.Certificate {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &private.PublicKey, private)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
