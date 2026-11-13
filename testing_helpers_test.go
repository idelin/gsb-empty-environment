package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

var issuerSequence int

// testIssuer is an in-process OIDC provider. Requests are delivered through a
// virtual http.RoundTripper rather than a real TCP listener, so the full
// discovery + JWKS code path runs without binding a socket.
type testIssuer struct {
	issuer  string
	kid     string
	alg     string
	key     crypto.Signer
	pub     crypto.PublicKey
	client  *http.Client
	jwksURI string
	t       *testing.T
}

func newTestIssuer(t *testing.T, alg string) *testIssuer {
	t.Helper()

	var (
		key crypto.Signer
		err error
	)
	switch alg {
	case "RS256":
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	case "ES256":
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "EdDSA":
		_, priv, genErr := ed25519.GenerateKey(rand.Reader)
		err, key = genErr, priv
	default:
		t.Fatalf("unsupported test alg %q", alg)
	}
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	p := &testIssuer{
		issuer:  fmt.Sprintf("https://idp-%d.%s.example", issuerSequence, strings.ToLower(alg)),
		kid:     fmt.Sprintf("kid-%s-%d", alg, issuerSequence),
		alg:     alg,
		key:     key,
		pub:     key.Public(),
		jwksURI: "",
		t:       t,
	}
	issuerSequence++
	p.client = &http.Client{Transport: roundTripFunc(p.serve)}
	return p
}

func (p *testIssuer) close() {}

// serve handles a virtual request and returns a response with no network I/O.
func (p *testIssuer) serve(req *http.Request) (*http.Response, error) {
	body := func(payload []byte, status int) *http.Response {
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(payload))),
			Request:    req,
		}
	}
	switch req.URL.Path {
	case discoveryPath:
		doc, _ := json.Marshal(map[string]string{
			"issuer":   p.issuer,
			"jwks_uri": p.publishedJWKSURI(),
		})
		return body(doc, http.StatusOK), nil
	case "/jwks":
		return body(p.jwks(), http.StatusOK), nil
	default:
		return body([]byte(`{"error":"not found"}`), http.StatusNotFound), nil
	}
}

func (p *testIssuer) publishedJWKSURI() string {
	if p.jwksURI != "" {
		return p.jwksURI
	}
	return p.issuer + "/jwks"
}

// verifier builds a Verifier for this provider using its virtual HTTP client.
func (p *testIssuer) verifier(t *testing.T) *Verifier {
	t.Helper()
	v, err := NewVerifier(Config{
		Issuer:     p.issuer,
		HTTPClient: p.client,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// keySet exposes the provider's public key as a static, issuer-bound KeySet.
func (p *testIssuer) keySet(t *testing.T) *KeySet {
	t.Helper()
	set := &KeySet{}
	if err := set.AddKey(p.kid, p.alg, p.pub); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	return set
}

// mint signs a JWT with the provider's key while letting the caller choose any
// claim set (including a foreign "iss"), which models the attack.
func (p *testIssuer) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := map[string]string{"alg": p.alg, "kid": p.kid, "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	token, err := sign(p.alg, p.key, headerJSON, payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

func standardClaims(issuer string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": issuer,
		"sub": "subject-1",
		"aud": []string{"client-id"},
		"iat": float64(now.Unix()),
		"nbf": float64(now.Add(-time.Minute).Unix()),
		"exp": float64(now.Add(time.Hour).Unix()),
	}
}

func (p *testIssuer) jwks() []byte {
	switch key := p.pub.(type) {
	case *rsa.PublicKey:
		jwk := rawJWK{Kty: "RSA", Use: "sig", Alg: p.alg, Kid: p.kid,
			N: base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}
		return mustMarshalJWKS(p.t, jwk)
	case *ecdsa.PublicKey:
		size := (key.Curve.Params().BitSize + 7) / 8
		jwk := rawJWK{Kty: "EC", Use: "sig", Alg: p.alg, Kid: p.kid,
			Crv: curveName(key.Curve),
			X:   base64.RawURLEncoding.EncodeToString(fillBytes(key.X, size)),
			Y:   base64.RawURLEncoding.EncodeToString(fillBytes(key.Y, size))}
		return mustMarshalJWKS(p.t, jwk)
	case ed25519.PublicKey:
		jwk := rawJWK{Kty: "OKP", Use: "sig", Alg: p.alg, Kid: p.kid,
			Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(key)}
		return mustMarshalJWKS(p.t, jwk)
	default:
		p.t.Fatalf("unsupported public key type %T", p.pub)
		return nil
	}
}

func mustMarshalJWKS(t *testing.T, key rawJWK) []byte {
	t.Helper()
	data, err := json.Marshal(jwkDocument{Keys: []rawJWK{key}})
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return data
}

func curveName(curve elliptic.Curve) string {
	switch curve.Params().BitSize {
	case 256:
		return "P-256"
	case 384:
		return "P-384"
	case 521:
		return "P-521"
	default:
		return ""
	}
}

func fillBytes(value *big.Int, size int) []byte {
	out := make([]byte, size)
	value.FillBytes(out)
	return out
}

// roundTripFunc adapts a handler function into an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
