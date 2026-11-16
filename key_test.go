package netsec_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	netsec "example.org/netsec/q0268"
)

func TestBuildKeyBindsCredentialsAndRequestProperties(t *testing.T) {
	t.Parallel()

	base := newRequest(t, http.MethodGet, "https://Example.COM/resource?a=1&b=2", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=alice"},
	})
	baseKey := mustRequestKey(t, base)

	equivalent := newRequest(t, "get", "https://example.com/resource?a=1&b=2", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=alice"},
	})
	if got := mustRequestKey(t, equivalent).String(); got != baseKey.String() {
		t.Fatalf("equivalent request key = %s, want %s", got, baseKey.String())
	}

	bobAuthorization := newRequest(t, http.MethodGet, "https://example.com/resource?a=1&b=2", http.Header{
		"Authorization": {"Bearer bob"},
		"Cookie":        {"session=alice"},
	})
	assertDifferentKeys(t, baseKey, mustRequestKey(t, bobAuthorization), "Authorization value")

	bobCookie := newRequest(t, http.MethodGet, "https://example.com/resource?a=1&b=2", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=bob"},
	})
	assertDifferentKeys(t, baseKey, mustRequestKey(t, bobCookie), "Cookie value")

	post := newRequest(t, http.MethodPost, "https://example.com/resource?a=1&b=2", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=alice"},
	})
	assertDifferentKeys(t, baseKey, mustRequestKey(t, post), "method")

	otherHost := newRequest(t, http.MethodGet, "https://other.example/resource?a=1&b=2", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=alice"},
	})
	assertDifferentKeys(t, baseKey, mustRequestKey(t, otherHost), "host")

	otherQuery := newRequest(t, http.MethodGet, "https://example.com/resource?a=2&b=2", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=alice"},
	})
	assertDifferentKeys(t, baseKey, mustRequestKey(t, otherQuery), "query value")

	reorderedQuery := newRequest(t, http.MethodGet, "https://example.com/resource?b=2&a=1", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=alice"},
	})
	assertDifferentKeys(t, baseKey, mustRequestKey(t, reorderedQuery), "query order")

	encodedQuery := newRequest(t, http.MethodGet, "https://example.com/resource?%61=1&b=%32", http.Header{
		"Authorization": {"Bearer alice"},
		"Cookie":        {"session=alice"},
	})
	if got, want := mustRequestKey(t, encodedQuery).String(), baseKey.String(); got != want {
		t.Fatalf("encoded query key = %s, want normalized key %s", got, want)
	}
}

func TestBuildKeyRequiresACredential(t *testing.T) {
	t.Parallel()

	withoutCredentials := newRequest(t, http.MethodGet, "https://example.com/resource", nil)
	if _, err := netsec.KeyForRequest(withoutCredentials); !errors.Is(err, netsec.ErrNoCredentials) {
		t.Fatalf("KeyForRequest() error = %v, want ErrNoCredentials", err)
	}

	emptyCredentials := newRequest(t, http.MethodGet, "https://example.com/resource", http.Header{
		"Authorization": {" "},
		"Cookie":        {""},
	})
	if _, err := netsec.KeyForRequest(emptyCredentials); !errors.Is(err, netsec.ErrNoCredentials) {
		t.Fatalf("KeyForRequest() error = %v, want ErrNoCredentials", err)
	}

	cookieOnly := newRequest(t, http.MethodGet, "https://example.com/resource", http.Header{
		"Cookie": {"session=alice"},
	})
	if _, err := netsec.KeyForRequest(cookieOnly); err != nil {
		t.Fatalf("KeyForRequest() with Cookie error = %v", err)
	}
}

func TestNormalizePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: "/"},
		{name: "root", raw: "/", want: "/"},
		{name: "dot", raw: "/a/./b", want: "/a/b"},
		{name: "dotdot", raw: "/a/../b", want: "/b"},
		{name: "relative", raw: "a/../b", want: "/b"},
		{name: "encoded dot", raw: "/a/%2e/b", want: "/a/b"},
		{name: "encoded dotdot", raw: "/safe/%2E%2E/admin", want: "/admin"},
		{name: "double encoded dotdot", raw: "/safe/%252e%252e/admin", want: "/admin"},
		{name: "repeated slash", raw: "/a//b/", want: "/a/b"},
		{name: "above root", raw: "/../../admin", want: "/admin"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := netsec.NormalizePath(test.raw)
			if err != nil {
				t.Fatalf("NormalizePath(%q) error = %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("NormalizePath(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestNormalizeMethod(t *testing.T) {
	t.Parallel()

	if got := netsec.NormalizeMethod(" post "); got != http.MethodPost {
		t.Fatalf("NormalizeMethod() = %q, want POST", got)
	}
	if got := netsec.NormalizeMethod(""); got != http.MethodGet {
		t.Fatalf("NormalizeMethod(empty) = %q, want GET", got)
	}
}

func TestNormalizePathRejectsEncodedSlash(t *testing.T) {
	t.Parallel()

	tests := []string{
		"/a%2fb",
		"/a%2Fb",
		"/a/%252fb",
		"/a/%252Fb",
		"/a/%25%32%66b",
	}
	for _, raw := range tests {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := netsec.NormalizePath(raw); !errors.Is(err, netsec.ErrEncodedSlash) {
				t.Fatalf("NormalizePath(%q) error = %v, want ErrEncodedSlash", raw, err)
			}
		})
	}
}

func TestNormalizePathRejectsMalformedEscape(t *testing.T) {
	t.Parallel()

	if _, err := netsec.NormalizePath("/a/%zz"); !errors.Is(err, netsec.ErrInvalidPath) {
		t.Fatalf("NormalizePath() error = %v, want ErrInvalidPath", err)
	}
}

func TestEncodedDotSegmentsShareNormalizedKey(t *testing.T) {
	t.Parallel()

	direct := newRequest(t, http.MethodGet, "https://example.com/admin", http.Header{
		"Authorization": {"Bearer alice"},
	})
	bypass := newRequest(t, http.MethodGet, "https://example.com/public/%2e%2e/admin", http.Header{
		"Authorization": {"Bearer alice"},
	})
	if got, want := mustRequestKey(t, bypass).String(), mustRequestKey(t, direct).String(); got != want {
		t.Fatalf("encoded-dot key = %s, want normalized key %s", got, want)
	}
}

func TestRequestWithEncodedSlashCannotBuildKey(t *testing.T) {
	t.Parallel()

	req := newRequest(t, http.MethodGet, "https://example.com/a%2fb", http.Header{
		"Authorization": {"Bearer alice"},
	})
	if _, err := netsec.KeyForRequest(req); !errors.Is(err, netsec.ErrEncodedSlash) {
		t.Fatalf("KeyForRequest() error = %v, want ErrEncodedSlash", err)
	}
}

func mustRequestKey(t *testing.T, req *http.Request) netsec.Key {
	t.Helper()
	key, err := netsec.KeyForRequest(req)
	if err != nil {
		t.Fatalf("KeyForRequest() error = %v", err)
	}
	return key
}

func assertDifferentKeys(t *testing.T, left, right netsec.Key, property string) {
	t.Helper()
	if left.String() == right.String() {
		t.Fatalf("keys do not bind %s: both are %s", property, left.String())
	}
}

func newRequest(t *testing.T, method, target string, header http.Header) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	return req
}
