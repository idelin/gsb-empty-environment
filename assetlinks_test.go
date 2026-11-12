package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fp builds a valid uppercase colon-separated SHA-256 fingerprint whose
// octets all equal b.
func fp(b byte) string {
	parts := make([]string, 32)
	for i := range parts {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

var (
	fpA = fp(0xAA)
	fpB = fp(0xBB)
	fpC = fp(0xCC)
)

func stmt(relation []string, namespace, pkg string, fps ...string) Statement {
	return Statement{
		Relation: relation,
		Target: Target{
			Namespace:              namespace,
			PackageName:            pkg,
			SHA256CertFingerprints: fps,
		},
	}
}

func TestVerifyPackageAcceptsMatchingFingerprint(t *testing.T) {
	stmts := []Statement{
		stmt([]string{RelationHandleAllURLs}, NamespaceAndroidApp, "com.example.app", fpA),
	}
	if !VerifyPackage(stmts, "com.example.app", []string{fpA}) {
		t.Fatal("expected exact fingerprint match to verify")
	}
}

func TestVerifyPackageFingerprintCaseInsensitive(t *testing.T) {
	lower := strings.ToLower(fpA)
	stmts := []Statement{
		stmt([]string{RelationHandleAllURLs}, NamespaceAndroidApp, "com.example.app", lower),
	}
	if !VerifyPackage(stmts, "com.example.app", []string{fpA}) {
		t.Fatal("lowercase declared fingerprint should match uppercase app cert")
	}
	if !VerifyPackage([]Statement{
		stmt([]string{RelationHandleAllURLs}, NamespaceAndroidApp, "com.example.app", fpA),
	}, "com.example.app", []string{lower}) {
		t.Fatal("uppercase declared fingerprint should match lowercase app cert")
	}
}

func TestVerifyPackageRejectsWrongFingerprint(t *testing.T) {
	// Attacker publishes the same package name signed with a different key.
	stmts := []Statement{
		stmt([]string{RelationHandleAllURLs}, NamespaceAndroidApp, "com.example.app", fpA),
	}
	if VerifyPackage(stmts, "com.example.app", []string{fpB}) {
		t.Fatal("wrong signing fingerprint must not verify")
	}
}

func TestVerifyPackageMatchesOneOfSeveralAppCerts(t *testing.T) {
	stmts := []Statement{
		stmt([]string{RelationHandleAllURLs}, NamespaceAndroidApp, "com.example.app", fpB),
	}
	if !VerifyPackage(stmts, "com.example.app", []string{fpA, fpB, fpC}) {
		t.Fatal("fingerprint equal to one of the app signing certs must verify")
	}
}

func TestVerifyPackageRejectsOtherRelations(t *testing.T) {
	cases := map[string]Statement{
		"unrelated permission": stmt([]string{"delegate_permission/common.get_login_creds"}, NamespaceAndroidApp, "com.example.app", fpA),
		"empty relation":       stmt(nil, NamespaceAndroidApp, "com.example.app", fpA),
		"web namespace":        stmt([]string{RelationHandleAllURLs}, "web", "com.example.app", fpA),
		"other package":        stmt([]string{RelationHandleAllURLs}, NamespaceAndroidApp, "com.example.other", fpA),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if VerifyPackage([]Statement{s}, "com.example.app", []string{fpA}) {
				t.Fatal("non-qualifying statement must not authorize the app")
			}
		})
	}
}

func TestVerifyPackageUnionsStatementsForSamePackage(t *testing.T) {
	stmts := []Statement{
		stmt([]string{RelationHandleAllURLs}, NamespaceAndroidApp, "com.example.app", fpA),
		stmt([]string{RelationHandleAllURLs}, NamespaceAndroidApp, "com.example.app", fpB),
	}
	if !VerifyPackage(stmts, "com.example.app", []string{fpA}) {
		t.Fatal("fingerprint from first statement should verify")
	}
	if !VerifyPackage(stmts, "com.example.app", []string{fpB}) {
		t.Fatal("fingerprint from second statement should verify")
	}
	if VerifyPackage(stmts, "com.example.app", []string{fpC}) {
		t.Fatal("fingerprint declared by no statement must not verify")
	}
}

func TestVerifyPackageIgnoresMalformedFingerprints(t *testing.T) {
	stmts := []Statement{
		stmt([]string{RelationHandleAllURLs}, NamespaceAndroidApp, "com.example.app",
			"AA:BB",                          // too short
			"not-hex-at-all",                 // garbage
			strings.ReplaceAll(fpA, ":", ""), // missing colons
		),
	}
	if VerifyPackage(stmts, "com.example.app", []string{fpA}) {
		t.Fatal("malformed declared fingerprints must never authorize")
	}
}

func TestNormalizeFingerprint(t *testing.T) {
	if got, err := NormalizeFingerprint(strings.ToLower(fpA)); err != nil || got != fpA {
		t.Fatalf("NormalizeFingerprint(lowercase) = %q, %v; want %q, nil", got, err, fpA)
	}
	for _, bad := range []string{"", "AA", fpA + ":00", "GG" + fpA[2:], strings.ReplaceAll(fpA, ":", "-")} {
		if _, err := NormalizeFingerprint(bad); err == nil {
			t.Fatalf("NormalizeFingerprint(%q) should fail", bad)
		}
	}
}

// roundTripFunc adapts a function to http.RoundTripper so tests can stub
// HTTP responses without touching the network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubClient returns an http.Client whose requests are answered by respond.
func stubClient(respond func(*http.Request) *http.Response) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return respond(r), nil
		}),
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

const testURL = "https://example.com/.well-known/assetlinks.json"

const validDoc = `[
  {
    "relation": ["delegate_permission/common.handle_all_urls"],
    "target": {
      "namespace": "android_app",
      "package_name": "com.example.app",
      "sha256_cert_fingerprints": ["` + "AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA:AA" + `"]
    }
  }
]`

func TestFetchStatementsValidDocument(t *testing.T) {
	client := stubClient(func(r *http.Request) *http.Response {
		return jsonResponse(http.StatusOK, validDoc)
	})
	ok, err := VerifyURL(context.Background(), client, testURL, "com.example.app", []string{fpA})
	if err != nil {
		t.Fatalf("VerifyURL: %v", err)
	}
	if !ok {
		t.Fatal("expected fetched document to authorize the app")
	}
}

func TestFetchStatementsRejectsRedirect(t *testing.T) {
	// The server answers with a redirect; a client that follows redirects
	// would reach the valid document, so rejection proves redirects are
	// not followed.
	client := stubClient(func(r *http.Request) *http.Response {
		if r.URL.Path == "/.well-known/assetlinks.json" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"/elsewhere"}},
				Body:       io.NopCloser(strings.NewReader("")),
			}
		}
		return jsonResponse(http.StatusOK, validDoc)
	})

	_, err := FetchStatements(context.Background(), client, testURL)
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("redirect should fail with ErrUnexpectedStatus, got %v", err)
	}
}

func TestFetchStatementsRejectsOversizeBody(t *testing.T) {
	client := stubClient(func(r *http.Request) *http.Response {
		return jsonResponse(http.StatusOK, strings.Repeat(" ", MaxBodySize+1))
	})

	_, err := FetchStatements(context.Background(), client, testURL)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("oversize body should fail with ErrBodyTooLarge, got %v", err)
	}
}

func TestFetchStatementsAcceptsExactlyMaxBody(t *testing.T) {
	padding := strings.Repeat(" ", MaxBodySize-len(validDoc))
	client := stubClient(func(r *http.Request) *http.Response {
		return jsonResponse(http.StatusOK, validDoc+padding)
	})
	if _, err := FetchStatements(context.Background(), client, testURL); err != nil {
		t.Fatalf("body of exactly 1 MiB should be accepted, got %v", err)
	}
}

func TestFetchStatementsRejectsNon200(t *testing.T) {
	client := stubClient(func(r *http.Request) *http.Response {
		return jsonResponse(http.StatusNotFound, "")
	})

	if _, err := FetchStatements(context.Background(), client, testURL); !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("non-200 should fail with ErrUnexpectedStatus, got %v", err)
	}
}

func TestFetchStatementsRejectsWrongContentType(t *testing.T) {
	client := stubClient(func(r *http.Request) *http.Response {
		resp := jsonResponse(http.StatusOK, validDoc)
		resp.Header.Set("Content-Type", "text/plain")
		return resp
	})

	if _, err := FetchStatements(context.Background(), client, testURL); !errors.Is(err, ErrUnexpectedContentType) {
		t.Fatalf("text/plain should fail with ErrUnexpectedContentType, got %v", err)
	}
}

func TestFetchStatementsRejectsPlainHTTP(t *testing.T) {
	called := false
	client := stubClient(func(r *http.Request) *http.Response {
		called = true
		return jsonResponse(http.StatusOK, validDoc)
	})

	_, err := FetchStatements(context.Background(), client, "http://example.com/.well-known/assetlinks.json")
	if !errors.Is(err, ErrInsecureScheme) {
		t.Fatalf("plain http should fail with ErrInsecureScheme, got %v", err)
	}
	if called {
		t.Fatal("no request must be issued for a non-https URL")
	}
}

func TestFetchStatementsRejectsInvalidJSON(t *testing.T) {
	client := stubClient(func(r *http.Request) *http.Response {
		return jsonResponse(http.StatusOK, "{not json")
	})
	if _, err := FetchStatements(context.Background(), client, testURL); err == nil {
		t.Fatal("invalid JSON should fail")
	}
}
