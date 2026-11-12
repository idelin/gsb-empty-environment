package netsec

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testIssuer   = "https://as.example"
	testClientID = "client-a"
)

type testFixture struct {
	parser *Parser
	key    *rsa.PrivateKey
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		ClientID:                       testClientID,
		RequestObjectSigningAlgorithms: []string{"RS256"},
		RequestObjectSigningKeys:       []any{&key.PublicKey},
	}
	parser, err := NewParser(Config{
		Issuer:         testIssuer,
		Clients:        StaticClients{testClientID: client},
		DecryptionKeys: []any{key},
		Clock:          func() time.Time { return time.Unix(1_800_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testFixture{parser: parser, key: key}
}

func (f *testFixture) claims(overrides map[string]any) map[string]any {
	claims := map[string]any{
		"iss":           testClientID,
		"aud":           testIssuer,
		"client_id":     testClientID,
		"response_type": "code",
		"scope":         "openid profile",
		"state":         "signed-state",
		"redirect_uri":  "https://client.example/callback",
		"exp":           1_800_000_300,
	}
	for name, value := range overrides {
		if value == nil {
			delete(claims, name)
			continue
		}
		claims[name] = value
	}
	return claims
}

func (f *testFixture) parse(t *testing.T, query url.Values) (*RequestObject, error) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "https://as.example/authorize?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return f.parser.ParseAuthorizationRequest(context.Background(), request)
}

func TestSignedRequestObjectIgnoresQueryAuthorizationParameters(t *testing.T) {
	f := newTestFixture(t)
	signed := signJWS(t, f.key, "RS256", nil, mustJSON(t, f.claims(nil)))

	query := url.Values{
		"client_id":     {testClientID},
		"request":       {signed},
		"scope":         {"openid admin"},
		"response_type": {"token"},
		"state":         {"query-state"},
		"redirect_uri":  {"https://attacker.example/callback"},
	}
	object, err := f.parse(t, query)
	if err != nil {
		t.Fatalf("ParseAuthorizationRequest: %v", err)
	}

	if got, _ := object.StringClaim("scope"); got != "openid profile" {
		t.Errorf("scope = %q, want signed value", got)
	}
	if got, _ := object.StringClaim("response_type"); got != "code" {
		t.Errorf("response_type = %q, want signed value", got)
	}
	if got, _ := object.StringClaim("state"); got != "signed-state" {
		t.Errorf("state = %q, want signed value", got)
	}
	if got, _ := object.StringClaim("redirect_uri"); got != "https://client.example/callback" {
		t.Errorf("redirect_uri = %q, want signed value", got)
	}
	if got := object.EffectiveParameters().Get("scope"); got != "openid profile" {
		t.Errorf("effective scope = %q, want signed value", got)
	}
	if object.Encrypted {
		t.Errorf("Encrypted = true for a plain JWS")
	}
}

func TestRequestObjectRejectsNestedRequestParameters(t *testing.T) {
	for _, name := range []string{"request", "request_uri"} {
		t.Run(name, func(t *testing.T) {
			f := newTestFixture(t)
			claims := f.claims(map[string]any{
				name: "https://attacker.example/inner",
			})
			signed := signJWS(t, f.key, "RS256", nil, mustJSON(t, claims))
			_, err := f.parse(t, url.Values{
				"client_id": {testClientID},
				"request":   {signed},
			})
			if !errors.Is(err, ErrNestedRequestParameter) {
				t.Fatalf("error = %v, want ErrNestedRequestParameter", err)
			}
		})
	}
}

func TestRequestObjectRejectsClientIDMismatch(t *testing.T) {
	f := newTestFixture(t)
	claims := f.claims(map[string]any{
		"client_id": "client-b",
		"iss":       "client-b",
	})
	signed := signJWS(t, f.key, "RS256", nil, mustJSON(t, claims))
	_, err := f.parse(t, url.Values{
		"client_id": {testClientID},
		"request":   {signed},
	})
	if !errors.Is(err, ErrClientIDMismatch) {
		t.Fatalf("error = %v, want ErrClientIDMismatch", err)
	}
}

func TestRequestObjectRejectsNoneAlgorithm(t *testing.T) {
	f := newTestFixture(t)
	claims := f.claims(nil)
	header := map[string]any{"alg": "none", "typ": "JWT"}
	token := compactJWT(header, mustJSON(t, claims), nil)
	_, err := f.parse(t, url.Values{
		"client_id": {testClientID},
		"request":   {token},
	})
	if !errors.Is(err, ErrAlgorithmNotAllowed) {
		t.Fatalf("error = %v, want ErrAlgorithmNotAllowed", err)
	}
}

func TestRequestObjectRejectsUnregisteredAlgorithm(t *testing.T) {
	f := newTestFixture(t)
	claims := f.claims(nil)
	payload := mustJSON(t, claims)
	header := map[string]any{"alg": "RS512", "typ": "JWT"}
	signingInput := encodeBase64URL(mustJSON(t, header)) + "." + encodeBase64URL(payload)
	digest, err := hashBytes(crypto.SHA512, []byte(signingInput))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA512, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	token := signingInput + "." + encodeBase64URL(signature)
	_, err = f.parse(t, url.Values{
		"client_id": {testClientID},
		"request":   {token},
	})
	if !errors.Is(err, ErrAlgorithmNotAllowed) {
		t.Fatalf("error = %v, want ErrAlgorithmNotAllowed", err)
	}
}

func TestEncryptedJWSRequestObject(t *testing.T) {
	f := newTestFixture(t)
	claims := f.claims(nil)
	signed := signJWS(t, f.key, "RS256", nil, mustJSON(t, claims))
	encrypted := encryptJWE(t, &f.key.PublicKey, signed)

	object, err := f.parse(t, url.Values{
		"client_id": {testClientID},
		"request":   {encrypted},
		"scope":     {"openid admin"},
	})
	if err != nil {
		t.Fatalf("ParseAuthorizationRequest: %v", err)
	}
	if !object.Encrypted {
		t.Errorf("Encrypted = false for an outer JWE")
	}
	if got, _ := object.StringClaim("scope"); got != "openid profile" {
		t.Errorf("scope = %q, want signed value", got)
	}
}

func TestRejectsDoubleWrappedJWE(t *testing.T) {
	f := newTestFixture(t)
	claims := f.claims(nil)
	signed := signJWS(t, f.key, "RS256", nil, mustJSON(t, claims))
	innerJWE := encryptJWE(t, &f.key.PublicKey, signed)
	outerJWE := encryptJWE(t, &f.key.PublicKey, innerJWE)

	_, err := f.parse(t, url.Values{
		"client_id": {testClientID},
		"request":   {outerJWE},
	})
	if !errors.Is(err, ErrNestedJWE) {
		t.Fatalf("error = %v, want ErrNestedJWE", err)
	}
}

func TestRejectsJWEWithoutInnerJWS(t *testing.T) {
	f := newTestFixture(t)
	encrypted := encryptJWE(t, &f.key.PublicKey, string(mustJSON(t, f.claims(nil))))
	_, err := f.parse(t, url.Values{
		"client_id": {testClientID},
		"request":   {encrypted},
	})
	if !errors.Is(err, ErrMalformedJOSE) {
		t.Fatalf("error = %v, want ErrMalformedJOSE for a JWE without an inner JWS", err)
	}
}

func TestRequestURIFetcher(t *testing.T) {
	f := newTestFixture(t)
	signed := signJWS(t, f.key, "RS256", nil, mustJSON(t, f.claims(nil)))
	fetcher := RequestURIFetcherFunc(func(context.Context, string) ([]byte, error) {
		return []byte(signed), nil
	})
	parser, err := NewParser(Config{
		Issuer:            testIssuer,
		Clients:           StaticClients{testClientID: f.parser.clients.(StaticClients)[testClientID]},
		DecryptionKeys:    []any{f.key},
		RequestURIFetcher: fetcher,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://as.example/authorize?"+url.Values{
		"client_id":   {testClientID},
		"request_uri": {"urn:example:request:123"},
		"scope":       {"openid admin"},
	}.Encode(), nil)
	object, err := parser.ParseAuthorizationRequest(context.Background(), request)
	if err != nil {
		t.Fatalf("ParseAuthorizationRequest: %v", err)
	}
	if object.RequestURI != "urn:example:request:123" {
		t.Errorf("RequestURI = %q", object.RequestURI)
	}
	if got, _ := object.StringClaim("scope"); got != "openid profile" {
		t.Errorf("scope = %q, want signed value", got)
	}
}

func TestRejectsIssuerAndAudienceMismatch(t *testing.T) {
	f := newTestFixture(t)
	cases := []struct {
		name   string
		claims map[string]any
		want   error
	}{
		{
			name:   "iss",
			claims: f.claims(map[string]any{"iss": "someone-else"}),
			want:   ErrIssuerMismatch,
		},
		{
			name:   "aud",
			claims: f.claims(map[string]any{"aud": []string{"https://other-as.example"}}),
			want:   ErrAudienceMismatch,
		},
		{
			name:   "aud array containing issuer",
			claims: f.claims(map[string]any{"aud": []string{"https://other-as.example", testIssuer}}),
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signed := signJWS(t, f.key, "RS256", nil, mustJSON(t, tc.claims))
			_, err := f.parse(t, url.Values{
				"client_id": {testClientID},
				"request":   {signed},
			})
			if tc.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRejectsDuplicateJSONClaims(t *testing.T) {
	f := newTestFixture(t)
	payload := []byte(`{"iss":"client-a","iss":"client-b","aud":"https://as.example","client_id":"client-a"}`)
	token := signRawPayload(t, f.key, "RS256", nil, payload)
	_, err := f.parse(t, url.Values{
		"client_id": {testClientID},
		"request":   {token},
	})
	if !errors.Is(err, ErrDuplicateJSONKey) {
		t.Fatalf("error = %v, want ErrDuplicateJSONKey", err)
	}
}

func signJWS(t *testing.T, key *rsa.PrivateKey, alg string, header map[string]any, payload []byte) string {
	t.Helper()
	return signRawPayload(t, key, alg, header, payload)
}

func signRawPayload(t *testing.T, key *rsa.PrivateKey, alg string, header map[string]any, payload []byte) string {
	t.Helper()
	if header == nil {
		header = map[string]any{"alg": alg, "typ": "JWT"}
	}
	signingInput := encodeBase64URL(mustJSON(t, header)) + "." + encodeBase64URL(payload)
	var hash crypto.Hash
	switch alg {
	case "RS256":
		hash = crypto.SHA256
	case "RS384":
		hash = crypto.SHA384
	case "RS512":
		hash = crypto.SHA512
	default:
		t.Fatalf("test helper does not sign %s", alg)
	}
	digest, err := hashBytes(hash, []byte(signingInput))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, hash, digest)
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + encodeBase64URL(signature)
}

func compactJWT(header map[string]any, payload, signature []byte) string {
	return encodeBase64URL(mustJSONNoTest(header)) + "." + encodeBase64URL(payload) + "." + encodeBase64URL(signature)
}

func encryptJWE(t *testing.T, publicKey *rsa.PublicKey, plaintext string) string {
	t.Helper()
	protected := mustJSON(t, map[string]any{"alg": "RSA-OAEP-256", "enc": "A256GCM"})
	protectedSegment := encodeBase64URL(protected)

	cek := make([]byte, 32)
	if _, err := rand.Read(cek); err != nil {
		t.Fatal(err)
	}
	wrappedKey, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, publicKey, cek, nil)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	sealed := gcm.Seal(nil, iv, []byte(plaintext), []byte(protectedSegment))
	ciphertext := sealed[:len(sealed)-16]
	tag := sealed[len(sealed)-16:]
	return strings.Join([]string{
		protectedSegment,
		encodeBase64URL(wrappedKey),
		encodeBase64URL(iv),
		encodeBase64URL(ciphertext),
		encodeBase64URL(tag),
	}, ".")
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustJSONNoTest(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
