package jwtmw

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a goroutine-safe controllable clock for deterministic tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	return c.t
}

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key
}

func mustECKey(t *testing.T, curve elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("generate ec key: %v", err)
	}
	return key
}

func mustEdKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return pub, priv
}

// jwkFromPublic serializes a public key as a JWK map.
func jwkFromPublic(kid string, pub any) map[string]any {
	switch key := pub.(type) {
	case *rsa.PublicKey:
		return map[string]any{
			"kty": "RSA",
			"kid": kid,
			"use": "sig",
			"alg": "RS256",
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(bigEndianInt(key.E)),
		}
	case *ecdsa.PublicKey:
		crv := map[elliptic.Curve]string{
			elliptic.P256(): "P-256",
			elliptic.P384(): "P-384",
			elliptic.P521(): "P-521",
		}[key.Curve]
		size := (key.Curve.Params().BitSize + 7) / 8
		return map[string]any{
			"kty": "EC",
			"kid": kid,
			"use": "sig",
			"crv": crv,
			"x":   base64.RawURLEncoding.EncodeToString(fixedWidth(key.X.Bytes(), size)),
			"y":   base64.RawURLEncoding.EncodeToString(fixedWidth(key.Y.Bytes(), size)),
		}
	case ed25519.PublicKey:
		return map[string]any{
			"kty": "OKP",
			"kid": kid,
			"use": "sig",
			"crv": "Ed25519",
			"x":   base64.RawURLEncoding.EncodeToString(key),
		}
	default:
		panic(fmt.Sprintf("unsupported public key type %T", pub))
	}
}

func bigEndianInt(e int) []byte {
	var buf []byte
	for v := e; v > 0; v >>= 8 {
		buf = append([]byte{byte(v)}, buf...)
	}
	if len(buf) == 0 {
		buf = []byte{0}
	}
	return buf
}

func fixedWidth(b []byte, size int) []byte {
	if len(b) >= size {
		return b
	}
	padded := make([]byte, size)
	copy(padded[size-len(b):], b)
	return padded
}

// jwksHandler serves a controllable key set and counts fetches.
type jwksHandler struct {
	mu        sync.Mutex
	keys      []map[string]any
	fail      bool
	fetchWait chan struct{} // optional: fetch blocks until closed
	started   chan struct{} // closed on first fetch entry
	fetches   atomic.Int64
}

func (h *jwksHandler) setKeys(keys ...map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.keys = keys
}

func (h *jwksHandler) setFail(fail bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fail = fail
}

func (h *jwksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.fetches.Add(1)
	if h.started != nil {
		select {
		case <-h.started:
		default:
			close(h.started)
		}
	}
	if h.fetchWait != nil {
		<-h.fetchWait
	}
	h.mu.Lock()
	fail := h.fail
	keys := append([]map[string]any(nil), h.keys...)
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if fail {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"jwks unavailable"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}

func testConfig(t *testing.T, uri string, clock *fakeClock, client *http.Client) Config {
	t.Helper()
	return Config{
		JWKSURI:            uri,
		CacheTTL:           time.Minute,
		GracePeriod:        30 * time.Second,
		MinRefreshInterval: time.Minute,
		HTTPClient:         client,
		now:                clock.now,
	}
}

func newVerifier(t *testing.T, clock *fakeClock, handler *jwksHandler) *Verifier {
	t.Helper()
	server := newMemoryServer(t, handler)
	v, err := New(testConfig(t, server.URL, clock, server.client()))
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return v
}

// signToken builds a compact JWT signed with the given private key.
func signToken(t *testing.T, alg, kid string, priv any, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": alg, "kid": kid, "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)

	var signature []byte
	switch key := priv.(type) {
	case *rsa.PrivateKey:
		signature = signRSA(t, alg, key, []byte(signingInput))
	case *ecdsa.PrivateKey:
		signature = signECDSA(t, alg, key, []byte(signingInput))
	case ed25519.PrivateKey:
		signature = ed25519.Sign(key, []byte(signingInput))
	default:
		t.Fatalf("unsupported private key type %T", priv)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func signRSA(t *testing.T, alg string, key *rsa.PrivateKey, input []byte) []byte {
	t.Helper()
	hasher, pss, ok := rsaAlgSpec(alg)
	if !ok {
		t.Fatalf("unsupported rsa alg %q", alg)
	}
	h := hasher.New()
	h.Write(input)
	digest := h.Sum(nil)
	if pss {
		sig, err := rsa.SignPSS(rand.Reader, key, hasher, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			t.Fatalf("sign pss: %v", err)
		}
		return sig
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, hasher, digest)
	if err != nil {
		t.Fatalf("sign pkcs1: %v", err)
	}
	return sig
}

func signECDSA(t *testing.T, alg string, key *ecdsa.PrivateKey, input []byte) []byte {
	t.Helper()
	hash, err := ecdsaHash(alg, &key.PublicKey)
	if err != nil {
		t.Fatalf("ecdsa alg: %v", err)
	}
	h := hash.New()
	h.Write(input)
	sig, err := ecdsa.SignASN1(rand.Reader, key, h.Sum(nil))
	if err != nil {
		t.Fatalf("sign ecdsa: %v", err)
	}
	return sig
}

// noneToken builds an unsigned token (header alg=none, empty signature).
func noneToken(t *testing.T, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "none", "kid": kid, "typ": "JWT"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(hb) + "." +
		base64.RawURLEncoding.EncodeToString(cb) + "."
}

// hmacToken signs a token with HMAC-SHA256, the classic confusion attack:
// the secret is derived from the RSA public key PEM-equivalent bytes.
func hmacToken(t *testing.T, kid string, secret []byte, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "HS256", "kid": kid, "typ": "JWT"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." +
		base64.RawURLEncoding.EncodeToString(cb)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingInput))
	h := mac.Sum(nil)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(h)
}

func doRequest(t *testing.T, v *Verifier, token string) *httptest.ResponseRecorder {
	t.Helper()
	handler := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := ClaimsFromContext(r.Context())
		if !ok {
			t.Error("claims missing from context after middleware")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": claims.Subject})
	}))
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func baseClaims(clock *fakeClock) map[string]any {
	return map[string]any{
		"sub": "user-123",
		"iss": "https://issuer.example",
		"aud": "gateway",
		"iat": clock.now().Unix(),
		"exp": clock.now().Add(time.Hour).Unix(),
	}
}

func ecdsaCurve(t *testing.T, name string) elliptic.Curve {
	t.Helper()
	switch name {
	case "P-256":
		return elliptic.P256()
	case "P-384":
		return elliptic.P384()
	case "P-521":
		return elliptic.P521()
	default:
		t.Fatalf("unknown curve %q", name)
		return nil
	}
}

func mutateAlg(t *testing.T, token, alg string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	headerJSON := decodeSegment(t, parts[0])
	var header map[string]any
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("decode header: %v", err)
	}
	header["alg"] = alg
	return encodeReplaced(t, parts, header, nil)
}

func mutateKidAndAlg(t *testing.T, token, kid, alg string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	headerJSON := decodeSegment(t, parts[0])
	var header map[string]any
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("decode header: %v", err)
	}
	header["kid"] = kid
	header["alg"] = alg
	return encodeReplaced(t, parts, header, nil)
}

func tamperSignature(token string) string {
	parts := strings.Split(token, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return token + "x"
	}
	if len(sig) > 0 {
		sig[0] ^= 0xFF
	}
	parts[2] = base64.RawURLEncoding.EncodeToString(sig)
	return strings.Join(parts, ".")
}

func decodeSegment(t *testing.T, segment string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	return raw
}

func splitToken(token string) []string {
	return strings.Split(token, ".")
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func encodeReplaced(t *testing.T, parts []string, header map[string]any, claims map[string]any) string {
	t.Helper()
	if header != nil {
		hb, err := json.Marshal(header)
		if err != nil {
			t.Fatalf("marshal header: %v", err)
		}
		parts[0] = base64.RawURLEncoding.EncodeToString(hb)
	}
	if claims != nil {
		cb, err := json.Marshal(claims)
		if err != nil {
			t.Fatalf("marshal claims: %v", err)
		}
		parts[1] = base64.RawURLEncoding.EncodeToString(cb)
	}
	return strings.Join(parts, ".")
}

type testServer struct {
	URL     string
	handler *jwksHandler
	client  *http.Client
}

func newJWKSServer(t *testing.T) *testServer {
	t.Helper()
	handler := &jwksHandler{}
	mem := newMemoryServer(t, handler)
	return &testServer{URL: mem.URL, handler: handler, client: mem.client()}
}

func newReqWithAuth(auth string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return req
}

func httptestServe(v *Verifier, req *http.Request) *httptest.ResponseRecorder {
	handler := v.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
