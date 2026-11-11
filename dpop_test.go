package dpopcheck

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type testKey struct {
	private *ecdsa.PrivateKey
	jwk     jwk
}

func newTestKey(t *testing.T) *testKey {
	t.Helper()
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &testKey{
		private: private,
		jwk: jwk{
			Kty: "EC",
			Crv: "P-256",
			X:   base64.RawURLEncoding.EncodeToString(private.PublicKey.X.Bytes()),
			Y:   base64.RawURLEncoding.EncodeToString(private.PublicKey.Y.Bytes()),
		},
	}
}

func (k *testKey) thumbprint(t *testing.T) string {
	t.Helper()
	thumbprint, err := k.jwk.thumbprint()
	if err != nil {
		t.Fatalf("thumbprint: %v", err)
	}
	return thumbprint
}

type proofOptions struct {
	key       *testKey
	htm       string
	htu       string
	iat       time.Time
	jti       string
	nonce     string
	ath       string
	typ       string
	alg       string
	headerJWK *jwk
	signKey   *ecdsa.PrivateKey
	signErr   bool
}

func signES256(t *testing.T, key *ecdsa.PrivateKey, signingInput string) string {
	t.Helper()
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		t.Fatalf("sign proof: %v", err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return base64.RawURLEncoding.EncodeToString(signature)
}

func makeProof(t *testing.T, opts proofOptions) string {
	t.Helper()
	headerJWK := opts.key.jwk
	if opts.headerJWK != nil {
		headerJWK = *opts.headerJWK
	}
	header := proofHeader{Typ: opts.typ, Alg: opts.alg, JWK: headerJWK}
	if header.Typ == "" {
		header.Typ = dpopJWTType
	}
	if header.Alg == "" {
		header.Alg = supportedAlgorithm
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsMap := map[string]any{
		"htm": opts.htm,
		"htu": opts.htu,
		"iat": float64(opts.iat.Unix()),
		"jti": opts.jti,
	}
	if opts.nonce != "" {
		claimsMap["nonce"] = opts.nonce
	}
	if opts.ath != "" {
		claimsMap["ath"] = opts.ath
	}
	payloadBytes, err := json.Marshal(claimsMap)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerBytes) + "." +
		base64.RawURLEncoding.EncodeToString(payloadBytes)
	signKey := opts.signKey
	if signKey == nil {
		signKey = opts.key.private
	}
	signature := signES256(t, signKey, signingInput)
	if opts.signErr {
		signature = strings.Repeat("A", 43)
	}
	return signingInput + "." + signature
}

func makeAccessToken(t *testing.T, key *testKey) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString(mustJSON(t, map[string]string{
		"alg": "ES256",
		"typ": "JWT",
	}))
	payload := base64.RawURLEncoding.EncodeToString(mustJSON(t, map[string]any{
		"iss": "https://issuer.example",
		"sub": "client-1",
		"cnf": map[string]string{"jkt": key.thumbprint(t)},
	}))
	signingInput := header + "." + payload
	return signingInput + "." + signES256(t, key.private, signingInput)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func urlParse(raw string) (*url.URL, error) {
	return url.Parse(raw)
}

type testHarness struct {
	verifier *Verifier
	handler  http.Handler
	now      time.Time
	baseURL  string
}

func newHarness(t *testing.T, options ...Option) *testHarness {
	t.Helper()
	now := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	allOptions := append([]Option{WithClock(func() time.Time { return now })}, options...)
	verifier := NewVerifier(allOptions...)
	handler := verifier.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := FromContext(r.Context())
		fmt.Fprintf(w, "ok:%s", info.JTI)
	}))
	return &testHarness{
		verifier: verifier,
		handler:  handler,
		now:      now,
		baseURL:  "http://dpop.example.test",
	}
}

func (h *testHarness) validProof(t *testing.T, key *testKey, method, target, jti string) string {
	t.Helper()
	return makeProof(t, proofOptions{
		key: key,
		htm: method,
		htu: h.baseURL + target,
		iat: h.now,
		jti: jti,
	})
}

func (h *testHarness) doRequest(t *testing.T, method, target, proof string, accessToken ...string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, h.baseURL+target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("DPoP", proof)
	if len(accessToken) > 0 && accessToken[0] != "" {
		req.Header.Set("Authorization", dpopAuthScheme+" "+accessToken[0])
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, req)
	return recorder.Result()
}

func TestValidProofAccepted(t *testing.T) {
	h := newHarness(t)
	key := newTestKey(t)
	proof := h.validProof(t, key, http.MethodGet, "/accounts", "jti-1")

	resp := h.doRequest(t, http.MethodGet, "/accounts?page=3", proof)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; challenge = %q",
			resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

func TestReplayRejected(t *testing.T) {
	h := newHarness(t)
	key := newTestKey(t)
	proof := h.validProof(t, key, http.MethodGet, "/accounts", "jti-replay")

	first := h.doRequest(t, http.MethodGet, "/accounts", proof)
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.StatusCode)
	}

	second := h.doRequest(t, http.MethodGet, "/accounts", proof)
	defer second.Body.Close()
	if second.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want 401", second.StatusCode)
	}
	if !strings.Contains(second.Header.Get("WWW-Authenticate"), ErrInvalidDPoP) {
		t.Fatalf("challenge = %q, want invalid_dpop_proof",
			second.Header.Get("WWW-Authenticate"))
	}
}

func TestCrossURLAndMethodRejected(t *testing.T) {
	h := newHarness(t)
	key := newTestKey(t)

	t.Run("different path", func(t *testing.T) {
		proof := h.validProof(t, key, http.MethodGet, "/accounts", "jti-url-1")
		resp := h.doRequest(t, http.MethodGet, "/admin", proof)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "htu") {
			t.Fatalf("challenge = %q, want an htu mismatch description",
				resp.Header.Get("WWW-Authenticate"))
		}
	})

	t.Run("different method", func(t *testing.T) {
		proof := h.validProof(t, key, http.MethodPost, "/accounts", "jti-url-2")
		resp := h.doRequest(t, http.MethodGet, "/accounts", proof)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized ||
			!strings.Contains(resp.Header.Get("WWW-Authenticate"), "htm") {
			t.Fatalf("status = %d challenge = %q, want htm mismatch 401",
				resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	})

	t.Run("query string is normalized away", func(t *testing.T) {
		proof := h.validProof(t, key, http.MethodGet, "/accounts", "jti-url-3")
		resp := h.doRequest(t, http.MethodGet, "/accounts?refresh=1", proof)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 because query is not part of htu",
				resp.StatusCode)
		}
	})
}

func TestThumbprintMismatchRejected(t *testing.T) {
	h := newHarness(t)
	proofKey := newTestKey(t)
	tokenKey := newTestKey(t)

	accessToken := makeAccessToken(t, tokenKey)
	proof := h.validProof(t, proofKey, http.MethodGet, "/accounts", "jti-jkt-1")

	resp := h.doRequest(t, http.MethodGet, "/accounts", proof, accessToken)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, ErrInvalidToken) || !strings.Contains(challenge, "jkt") {
		t.Fatalf("challenge = %q, want invalid_token jkt mismatch", challenge)
	}
}

func TestBoundTokenAcceptedAndATHChecked(t *testing.T) {
	h := newHarness(t)
	key := newTestKey(t)
	accessToken := makeAccessToken(t, key)

	t.Run("matching jkt", func(t *testing.T) {
		proof := h.validProof(t, key, http.MethodGet, "/accounts", "jti-jkt-ok")
		resp := h.doRequest(t, http.MethodGet, "/accounts", proof, accessToken)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200; challenge = %q",
				resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	})

	t.Run("wrong ath", func(t *testing.T) {
		proof := makeProof(t, proofOptions{
			key: key,
			htm: http.MethodGet,
			htu: h.baseURL + "/accounts",
			iat: h.now,
			jti: "jti-ath-bad",
			ath: base64.RawURLEncoding.EncodeToString([]byte("wrong")),
		})
		resp := h.doRequest(t, http.MethodGet, "/accounts", proof, accessToken)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized ||
			!strings.Contains(resp.Header.Get("WWW-Authenticate"), "ath") {
			t.Fatalf("status = %d challenge = %q, want ath mismatch 401",
				resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	})

	t.Run("correct ath", func(t *testing.T) {
		proof := makeProof(t, proofOptions{
			key: key,
			htm: http.MethodGet,
			htu: h.baseURL + "/accounts",
			iat: h.now,
			jti: "jti-ath-ok",
			ath: hashAccessToken(accessToken),
		})
		resp := h.doRequest(t, http.MethodGet, "/accounts", proof, accessToken)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200; challenge = %q",
				resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
		}
	})
}

func TestNonceRequiredAfterIssuance(t *testing.T) {
	h := newHarness(t)
	key := newTestKey(t)

	nonce, err := h.verifier.NonceManager().Issue()
	if err != nil {
		t.Fatalf("issue nonce: %v", err)
	}

	missing := h.validProof(t, key, http.MethodGet, "/accounts", "jti-nonce-1")
	resp := h.doRequest(t, http.MethodGet, "/accounts", missing)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized ||
		!strings.Contains(resp.Header.Get("WWW-Authenticate"), ErrUseDPoPNonce) {
		t.Fatalf("status = %d challenge = %q, want use_dpop_nonce 401",
			resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if resp.Header.Get(DPoPNonceHeader) == "" {
		t.Fatal("response must carry a fresh DPoP-Nonce header")
	}

	withNonce := makeProof(t, proofOptions{
		key:   key,
		htm:   http.MethodGet,
		htu:   h.baseURL + "/accounts",
		iat:   h.now,
		jti:   "jti-nonce-2",
		nonce: nonce,
	})
	okResp := h.doRequest(t, http.MethodGet, "/accounts", withNonce)
	defer okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; challenge = %q",
			okResp.StatusCode, okResp.Header.Get("WWW-Authenticate"))
	}

	stale := makeProof(t, proofOptions{
		key:   key,
		htm:   http.MethodGet,
		htu:   h.baseURL + "/accounts",
		iat:   h.now,
		jti:   "jti-nonce-3",
		nonce: "not-an-issued-nonce",
	})
	staleResp := h.doRequest(t, http.MethodGet, "/accounts", stale)
	defer staleResp.Body.Close()
	if staleResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unknown nonce", staleResp.StatusCode)
	}
}

func TestIATWindowEnforced(t *testing.T) {
	h := newHarness(t)
	key := newTestKey(t)

	tooOld := makeProof(t, proofOptions{
		key: key,
		htm: http.MethodGet,
		htu: h.baseURL + "/accounts",
		iat: h.now.Add(-61 * time.Second),
		jti: "jti-iat-old",
	})
	resp := h.doRequest(t, http.MethodGet, "/accounts", tooOld)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an expired iat", resp.StatusCode)
	}

	future := makeProof(t, proofOptions{
		key: key,
		htm: http.MethodGet,
		htu: h.baseURL + "/accounts",
		iat: h.now.Add(2 * time.Minute),
		jti: "jti-iat-future",
	})
	futureResp := h.doRequest(t, http.MethodGet, "/accounts", future)
	defer futureResp.Body.Close()
	if futureResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a future iat", futureResp.StatusCode)
	}
}

func TestHeaderAndSignatureChecks(t *testing.T) {
	h := newHarness(t)
	key := newTestKey(t)

	t.Run("wrong typ", func(t *testing.T) {
		proof := makeProof(t, proofOptions{
			key: key,
			typ: "JWT",
			htm: http.MethodGet,
			htu: h.baseURL + "/accounts",
			iat: h.now,
			jti: "jti-typ",
		})
		resp := h.doRequest(t, http.MethodGet, "/accounts", proof)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("wrong alg", func(t *testing.T) {
		proof := makeProof(t, proofOptions{
			key: key,
			alg: "RS256",
			htm: http.MethodGet,
			htu: h.baseURL + "/accounts",
			iat: h.now,
			jti: "jti-alg",
		})
		resp := h.doRequest(t, http.MethodGet, "/accounts", proof)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("tampered signature", func(t *testing.T) {
		proof := h.validProof(t, key, http.MethodGet, "/accounts", "jti-sig-bad")
		parts := strings.Split(proof, ".")
		parts[2] = signES256(t, key.private, "different.signing.input")
		resp := h.doRequest(t, http.MethodGet, "/accounts", strings.Join(parts, "."))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for a signature over another input",
				resp.StatusCode)
		}
	})

	t.Run("signed by another key", func(t *testing.T) {
		attacker := newTestKey(t)
		proof := makeProof(t, proofOptions{
			key:     key,
			signKey: attacker.private,
			htm:     http.MethodGet,
			htu:     h.baseURL + "/accounts",
			iat:     h.now,
			jti:     "jti-sig-other",
		})
		resp := h.doRequest(t, http.MethodGet, "/accounts", proof)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for a mismatched embedded key",
				resp.StatusCode)
		}
	})

	t.Run("missing DPoP header", func(t *testing.T) {
		resp := h.doRequest(t, http.MethodGet, "/accounts", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})
}

func TestJTIReusableAfterWindowElapses(t *testing.T) {
	now := time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)
	h := newHarness(t, WithWindow(10*time.Second), WithClock(func() time.Time { return now }))
	key := newTestKey(t)

	first := makeProof(t, proofOptions{
		key: key,
		htm: http.MethodGet,
		htu: h.baseURL + "/accounts",
		iat: now,
		jti: "jti-window",
	})
	resp := h.doRequest(t, http.MethodGet, "/accounts", first)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200", resp.StatusCode)
	}

	now = now.Add(11 * time.Second)
	second := makeProof(t, proofOptions{
		key: key,
		htm: http.MethodGet,
		htu: h.baseURL + "/accounts",
		iat: now,
		jti: "jti-window",
	})
	secondResp := h.doRequest(t, http.MethodGet, "/accounts", second)
	defer secondResp.Body.Close()
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want 200 once the window elapsed",
			secondResp.StatusCode)
	}
}

func TestNormalizeHTU(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"drops query", "https://api.example.com/accounts?page=1", "https://api.example.com/accounts"},
		{"drops fragment", "https://api.example.com/accounts#frag", "https://api.example.com/accounts"},
		{"drops default https port", "https://api.example.com:443/accounts", "https://api.example.com/accounts"},
		{"drops default http port", "http://api.example.com:80/accounts", "http://api.example.com/accounts"},
		{"keeps non-default port", "https://api.example.com:8443/accounts", "https://api.example.com:8443/accounts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := urlParse(tc.raw)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.raw, err)
			}
			if got := normalizeHTU(parsed); got != tc.want {
				t.Fatalf("normalizeHTU(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDefaultJKTResolver(t *testing.T) {
	key := newTestKey(t)
	token := makeAccessToken(t, key)
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/accounts", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", dpopAuthScheme+" "+token)

	jkt, err := DefaultJKTResolver(req)
	if err != nil {
		t.Fatalf("resolver error: %v", err)
	}
	if jkt != key.thumbprint(t) {
		t.Fatalf("jkt = %q, want %q", jkt, key.thumbprint(t))
	}
}
