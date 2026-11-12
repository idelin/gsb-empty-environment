package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testKeyID  = "gateway-test-key"
	testTarget = "https://api.example.com/v1/items?x=1"
)

var fullCoverage = []string{"@method", "@target-uri", "content-digest"}

func ed25519Registry(t *testing.T) (ed25519.PrivateKey, *MemoryKeyRegistry) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewKeyRegistry()
	if err := registry.Register(testKeyID, AlgorithmEd25519, publicKey); err != nil {
		t.Fatal(err)
	}
	return privateKey, registry
}

func contentDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":"
}

func signedRequest(
	t *testing.T,
	privateKey ed25519.PrivateKey,
	body []byte,
	components []string,
	created int64,
	expires *int64,
	alg string,
	extraHeaders http.Header,
) *http.Request {
	t.Helper()
	return signedRequestWithOptionalCreated(t, body, components, &created, expires, alg, extraHeaders,
		func(base []byte) []byte {
			return ed25519.Sign(privateKey, base)
		})
}

func signedRequestWithSigner(
	t *testing.T,
	body []byte,
	components []string,
	created int64,
	expires *int64,
	alg string,
	extraHeaders http.Header,
	signer func([]byte) []byte,
) *http.Request {
	t.Helper()
	return signedRequestWithOptionalCreated(t, body, components, &created, expires, alg, extraHeaders,
		signer)
}

func signedRequestWithoutCreated(
	t *testing.T,
	privateKey ed25519.PrivateKey,
	body []byte,
	components []string,
	expires *int64,
	alg string,
	extraHeaders http.Header,
) *http.Request {
	t.Helper()
	return signedRequestWithOptionalCreated(t, body, components, nil, expires, alg, extraHeaders,
		func(base []byte) []byte {
			return ed25519.Sign(privateKey, base)
		})
}

func signedRequestWithOptionalCreated(
	t *testing.T,
	body []byte,
	components []string,
	created *int64,
	expires *int64,
	alg string,
	extraHeaders http.Header,
	signer func([]byte) []byte,
) *http.Request {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, testTarget, bytes.NewReader(body))
	request.Host = "api.example.com"
	for name, values := range extraHeaders {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	request.Header.Set("Content-Digest", contentDigest(body))

	var base strings.Builder
	for _, component := range components {
		var value string
		switch component {
		case "@method":
			value = request.Method
		case "@target-uri":
			value = request.URL.String()
		case "content-digest":
			value = request.Header.Get("Content-Digest")
		default:
			values := request.Header.Values(component)
			if len(values) == 0 {
				t.Fatalf("component %q has no header values", component)
			}
			value = strings.Join(values, ", ")
		}
		fmt.Fprintf(&base, "%q: %s\n", component, value)
	}

	quotedComponents := make([]string, len(components))
	for i, component := range components {
		quotedComponents[i] = fmt.Sprintf("%q", component)
	}
	parameters := ""
	if created != nil {
		parameters += fmt.Sprintf(";created=%d", *created)
	}
	if expires != nil {
		parameters += fmt.Sprintf(";expires=%d", *expires)
	}
	parameters += fmt.Sprintf(";keyid=%q", testKeyID)
	if alg != "" {
		parameters += fmt.Sprintf(";alg=%q", alg)
	}
	coveredList := fmt.Sprintf("(%s)", strings.Join(quotedComponents, " "))
	request.Header.Set("Signature-Input", "sig1="+coveredList+parameters)
	fmt.Fprintf(&base, "%q: %s%s\n", "@signature-params", coveredList, parameters)
	signature := signer([]byte(base.String()))
	request.Header.Set("Signature", "sig1=:"+base64.StdEncoding.EncodeToString(signature)+":")
	return request
}

func verifyAt(request *http.Request, registry KeyRegistry, now time.Time) (*Result, error) {
	return VerifyRequest(request, registry, WithNow(now))
}

func TestVerifyValidSignatureAndRestoreBody(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	body := []byte(`{"hello":"world"}`)
	request := signedRequest(t, privateKey, body, fullCoverage, now.Unix(), nil, "", nil)

	result, err := verifyAt(request, registry, now)
	if err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}
	if result.KeyID != testKeyID || result.Algorithm != AlgorithmEd25519 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.SignatureLabel != "sig1" {
		t.Fatalf("signature label = %q", result.SignatureLabel)
	}

	restored, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, body) {
		t.Fatalf("body was not restored: got %q want %q", restored, body)
	}
}

func TestRejectsMissingCoveredComponents(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	body := []byte("payload")

	for _, omitted := range fullCoverage {
		t.Run(omitted, func(t *testing.T) {
			components := make([]string, 0, len(fullCoverage)-1)
			for _, component := range fullCoverage {
				if component != omitted {
					components = append(components, component)
				}
			}
			request := signedRequest(t, privateKey, body, components, now.Unix(), nil, "", nil)
			_, err := verifyAt(request, registry, now)
			if !errors.Is(err, ErrMissingCoveredComponent) {
				t.Fatalf("error = %v, want ErrMissingCoveredComponent", err)
			}
			if !strings.Contains(err.Error(), omitted) {
				t.Fatalf("error %q does not name omitted component %q", err, omitted)
			}
		})
	}

	t.Run("created", func(t *testing.T) {
		request := signedRequestWithoutCreated(t, privateKey, body, fullCoverage, nil, "", nil)
		_, err := verifyAt(request, registry, now)
		if !errors.Is(err, ErrTimestamp) {
			t.Fatalf("error = %v, want ErrTimestamp for missing created parameter", err)
		}
	})
}

func TestRejectsAlgorithmSubstitution(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	body := []byte("payload")
	request := signedRequest(t, privateKey, body, fullCoverage, now.Unix(), nil, "hmac-sha256", nil)

	_, err := verifyAt(request, registry, now)
	if !errors.Is(err, ErrAlgorithmMismatch) {
		t.Fatalf("error = %v, want ErrAlgorithmMismatch", err)
	}
}

func TestCreatedParameterIsBoundBySignature(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	body := []byte("payload")
	request := signedRequest(t, privateKey, body, fullCoverage, now.Unix()-10, nil, "", nil)

	input := request.Header.Get("Signature-Input")
	input = strings.Replace(input, fmt.Sprintf("created=%d", now.Unix()-10),
		fmt.Sprintf("created=%d", now.Unix()-9), 1)
	request.Header.Set("Signature-Input", input)

	_, err := verifyAt(request, registry, now)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("error = %v, want ErrInvalidSignature after changing created", err)
	}
}

func TestOmittingAlgStillUsesRegisteredAlgorithm(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	body := []byte("payload")
	request := signedRequest(t, privateKey, body, fullCoverage, now.Unix(), nil, "", nil)

	result, err := verifyAt(request, registry, now)
	if err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}
	if result.Algorithm != AlgorithmEd25519 {
		t.Fatalf("algorithm = %q, want %q", result.Algorithm, AlgorithmEd25519)
	}
}

func TestHMACSignatureUsesRegisteredAlgorithm(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	secret := []byte("0123456789abcdef0123456789abcdef")
	registry := NewKeyRegistry()
	if err := registry.Register(testKeyID, AlgorithmHMACSHA256, secret); err != nil {
		t.Fatal(err)
	}
	body := []byte("payload")
	request := signedRequestWithSigner(t, body, fullCoverage, now.Unix(), nil, "hmac-sha256", nil,
		func(base []byte) []byte {
			mac := hmac.New(sha256.New, secret)
			_, _ = mac.Write(base)
			return mac.Sum(nil)
		})

	result, err := verifyAt(request, registry, now)
	if err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}
	if result.Algorithm != AlgorithmHMACSHA256 {
		t.Fatalf("algorithm = %q, want %q", result.Algorithm, AlgorithmHMACSHA256)
	}
}

func TestRejectsModifiedBody(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	request := signedRequest(t, privateKey, []byte("original"), fullCoverage, now.Unix(), nil, "", nil)
	request.Body = io.NopCloser(strings.NewReader("tampered"))

	_, err := verifyAt(request, registry, now)
	if !errors.Is(err, ErrContentDigest) {
		t.Fatalf("error = %v, want ErrContentDigest", err)
	}
}

func TestRejectsCRLFInjectionInCoveredHeader(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	body := []byte("payload")

	for name, injected := range map[string]string{
		"CRLF": "safe\r\nX-Evil: injected",
		"LF":   "safe\nX-Evil: injected",
	} {
		t.Run(name, func(t *testing.T) {
			components := append(append([]string{}, fullCoverage...), "x-inject")
			request := signedRequest(t, privateKey, body, components, now.Unix(), nil, "",
				http.Header{"X-Inject": {injected}})
			_, err := verifyAt(request, registry, now)
			if !errors.Is(err, ErrHeaderValue) {
				t.Fatalf("error = %v, want ErrHeaderValue", err)
			}
		})
	}
}

func TestDuplicateHeadersAreMergedInFieldOrder(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	body := []byte("payload")
	components := append(append([]string{}, fullCoverage...), "x-duplicate")
	request := signedRequest(t, privateKey, body, components, now.Unix(), nil, "",
		http.Header{"X-Duplicate": {"first", "second"}})

	if _, err := verifyAt(request, registry, now); err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}

	request.Header["X-Duplicate"] = []string{"second", "first"}
	if _, err := verifyAt(request, registry, now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("reordered duplicate headers error = %v, want ErrInvalidSignature", err)
	}
}

func TestCreatedAndExpiresWindows(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	body := []byte("payload")

	tests := []struct {
		name      string
		created   int64
		expires   *int64
		wantError error
	}{
		{name: "created exactly old boundary", created: now.Unix() - 60},
		{name: "created exactly future boundary", created: now.Unix() + 60},
		{name: "created too old", created: now.Unix() - 61, wantError: ErrTimestamp},
		{name: "created too far future", created: now.Unix() + 61, wantError: ErrTimestamp},
		{name: "expired", created: now.Unix() - 10, expires: int64Ptr(now.Unix() - 1), wantError: ErrExpired},
		{name: "not yet expired", created: now.Unix() - 10, expires: int64Ptr(now.Unix() + 30)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := signedRequest(t, privateKey, body, fullCoverage, test.created, test.expires, "", nil)
			_, err := verifyAt(request, registry, now)
			if test.wantError == nil {
				if err != nil {
					t.Fatalf("VerifyRequest() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func TestRejectsMissingOrWrongContentDigest(t *testing.T) {
	now := time.Unix(1_760_000_000, 0).UTC()
	privateKey, registry := ed25519Registry(t)
	body := []byte("payload")

	request := signedRequest(t, privateKey, body, fullCoverage, now.Unix(), nil, "", nil)
	request.Header.Del("Content-Digest")
	if _, err := verifyAt(request, registry, now); !errors.Is(err, ErrContentDigest) {
		t.Fatalf("missing digest error = %v, want ErrContentDigest", err)
	}

	request = signedRequest(t, privateKey, body, fullCoverage, now.Unix(), nil, "", nil)
	request.Header.Set("Content-Digest", contentDigest([]byte("different")))
	if _, err := verifyAt(request, registry, now); !errors.Is(err, ErrContentDigest) {
		t.Fatalf("wrong digest error = %v, want ErrContentDigest", err)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
