package dpop_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"formal.local/w28-dd7127bdf9/dpop"
)

type fixedClock struct {
	t time.Time
}

func (c *fixedClock) now() time.Time { return c.t }

func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

type testKeys struct {
	ecPrivate  *ecdsa.PrivateKey
	ecPublic   *dpop.JWK
	edPrivate  ed25519.PrivateKey
	edPublic   *dpop.JWK
	ecOther    *ecdsa.PrivateKey
	ecOtherJwk *dpop.JWK
}

func b64url(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func mustECJWK(pub *ecdsa.PublicKey) *dpop.JWK {
	x := pub.X.Bytes()
	y := pub.Y.Bytes()
	xPadded := make([]byte, 32)
	yPadded := make([]byte, 32)
	copy(xPadded[32-len(x):], x)
	copy(yPadded[32-len(y):], y)
	return &dpop.JWK{KeyType: "EC", Curve: "P-256", X: b64url(xPadded), Y: b64url(yPadded)}
}

func mustEdJWK(pub ed25519.PublicKey) *dpop.JWK {
	return &dpop.JWK{KeyType: "OKP", Curve: "Ed25519", X: b64url(pub)}
}

func loadTestKeys(t *testing.T) *testKeys {
	t.Helper()
	ecPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate EC key: %v", err)
	}
	ecOther, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate second EC key: %v", err)
	}
	_, edPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	return &testKeys{
		ecPrivate:  ecPrivate,
		ecPublic:   mustECJWK(&ecPrivate.PublicKey),
		edPrivate:  edPrivate,
		edPublic:   mustEdJWK(edPrivate.Public().(ed25519.PublicKey)),
		ecOther:    ecOther,
		ecOtherJwk: mustECJWK(&ecOther.PublicKey),
	}
}

// proofOptions controls how a test DPoP proof is minted.
type proofOptions struct {
	alg       string
	signAlg   string
	jwk       *dpop.JWK
	signer    interface{}
	htm       string
	htu       string
	ath       string
	jti       string
	iat       time.Time
	nonce     string
	typ       string
	rawHeader map[string]interface{}
}

func defaultProofOptions(t *testing.T, keys *testKeys, accessToken, method, target string, now time.Time) proofOptions {
	t.Helper()
	sum := sha256.Sum256([]byte(accessToken))
	return proofOptions{
		alg:    "ES256",
		jwk:    keys.ecPublic,
		signer: keys.ecPrivate,
		htm:    method,
		htu:    target,
		ath:    b64url(sum[:]),
		jti:    "jti-" + b64url([]byte(now.String()+target)),
		iat:    now,
		typ:    "dpop+jwt",
	}
}

// signProof builds a compact DPoP JWT. When rawHeader is set it is used as the
// full header, which lets tests inject malformed or forbidden members.
func signProof(t *testing.T, opts proofOptions) string {
	t.Helper()

	var headerMap map[string]interface{}
	if opts.rawHeader != nil {
		headerMap = opts.rawHeader
	} else {
		headerMap = map[string]interface{}{
			"typ": opts.typ,
			"alg": opts.alg,
			"jwk": opts.jwk,
		}
	}
	headerJSON, err := json.Marshal(headerMap)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}

	claims := map[string]interface{}{
		"htm": opts.htm,
		"htu": opts.htu,
		"ath": opts.ath,
		"jti": opts.jti,
		"iat": opts.iat.Unix(),
	}
	if opts.nonce != "" {
		claims["nonce"] = opts.nonce
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	signingInput := b64url(headerJSON) + "." + b64url(payloadJSON)
	signAlg := opts.signAlg
	if signAlg == "" {
		signAlg = opts.alg
	}
	signature := signSigningInput(t, signAlg, opts.signer, []byte(signingInput))
	return signingInput + "." + b64url(signature)
}

func signSigningInput(t *testing.T, alg string, signer interface{}, input []byte) []byte {
	t.Helper()
	switch alg {
	case "ES256":
		key, ok := signer.(*ecdsa.PrivateKey)
		if !ok {
			t.Fatalf("ES256 requires *ecdsa.PrivateKey, got %T", signer)
		}
		sum := sha256.Sum256(input)
		r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		signature := make([]byte, 64)
		r.FillBytes(signature[:32])
		s.FillBytes(signature[32:])
		return signature
	case "EdDSA":
		key, ok := signer.(ed25519.PrivateKey)
		if !ok {
			t.Fatalf("EdDSA requires ed25519.PrivateKey, got %T", signer)
		}
		return ed25519.Sign(key, input)
	default:
		t.Fatalf("unsupported test alg %q", alg)
		return nil
	}
}

// mintAccessToken builds an unsigned compact JWT carrying cnf.jkt.
func mintAccessToken(t *testing.T, jkt string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{
		"iss": "https://issuer.example",
		"sub": "subject-123",
		"cnf": map[string]string{"jkt": jkt},
	})
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	header := b64url([]byte(`{"alg":"RS256","typ":"at+jwt"}`))
	return header + "." + b64url(payload) + "."
}

func newRequest(t *testing.T, method, target, authScheme, accessToken, proof string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if accessToken != "" {
		req.Header.Set("Authorization", authScheme+" "+accessToken)
	}
	if proof != "" {
		req.Header.Set("DPoP", proof)
	}
	return req
}

func newTestHandler(t *testing.T, verifier *dpop.Verifier) http.Handler {
	t.Helper()
	handler := verifier.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := dpop.FromContext(r.Context())
		if claims == nil {
			http.Error(w, "no claims", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok:"+claims.JTI)
	}))
	return handler
}

func invoke(t *testing.T, handler http.Handler, method, target string, build func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if build != nil {
		build(req)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertErrorCode(t *testing.T, resp *http.Response, wantCode string) {
	t.Helper()
	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("status = %d, want 401, body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	challenge := resp.Header.Get("Www-Authenticate")
	if !strings.Contains(challenge, `error="`+wantCode+`"`) {
		t.Fatalf("WWW-Authenticate = %q, want error %q", challenge, wantCode)
	}
}

func mustThumbprint(t *testing.T, key *dpop.JWK) string {
	t.Helper()
	jkt, err := key.Thumbprint()
	if err != nil {
		t.Fatalf("thumbprint: %v", err)
	}
	return jkt
}
