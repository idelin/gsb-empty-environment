package presign

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var signedAt = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func validQuery() string {
	return "X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKID%2F20261113%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Date=20261113T120000Z" +
		"&X-Amz-Expires=600" +
		"&X-Amz-SignedHeaders=host" +
		"&X-Amz-Signature=deadbeef"
}

func newRequest(t *testing.T, target string, headers map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestValidateRequestValid(t *testing.T) {
	r := newRequest(t, "http://example.com/bucket/key.txt?"+validQuery(), nil)
	if err := ValidateRequest(r, signedAt.Add(time.Minute)); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestValidateRequestDuplicateAmzParam(t *testing.T) {
	cases := map[string]string{
		"appended expires":    validQuery() + "&X-Amz-Expires=31536000",
		"case variant":        validQuery() + "&x-amz-expires=60",
		"encoded key variant": validQuery() + "&X-%41mz-Expires=60",
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRequest(t, "http://example.com/b?"+query, nil)
			err := ValidateRequest(r, signedAt)
			if !errors.Is(err, ErrDuplicateQueryParam) {
				t.Fatalf("want ErrDuplicateQueryParam, got %v", err)
			}
		})
	}
}

func TestValidateRequestExpiresBounds(t *testing.T) {
	cases := []struct {
		expires string
		wantErr bool
	}{
		{"0", true},
		{"-5", true},
		{"1", false},
		{"600", false},
		{"604800", false},
		{"604801", true},
		{"31536000", true}, // one year, the attack from the threat model
		{"abc", true},
		{"60.5", true},
		{"", true},
	}
	for _, tc := range cases {
		t.Run("expires="+tc.expires, func(t *testing.T) {
			q := strings.Replace(validQuery(), "X-Amz-Expires=600", "X-Amz-Expires="+tc.expires, 1)
			r := newRequest(t, "http://example.com/b?"+q, nil)
			err := ValidateRequest(r, signedAt)
			if tc.wantErr && !errors.Is(err, ErrExpiresOutOfRange) {
				t.Fatalf("want ErrExpiresOutOfRange, got %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expires %s should be accepted, got %v", tc.expires, err)
			}
		})
	}
}

func TestValidateRequestSignedHeadersMustContainHost(t *testing.T) {
	q := strings.Replace(validQuery(), "X-Amz-SignedHeaders=host", "X-Amz-SignedHeaders=x-amz-acl", 1)
	r := newRequest(t, "http://example.com/b?"+q, map[string]string{"X-Amz-Acl": "public-read"})
	if err := ValidateRequest(r, signedAt); !errors.Is(err, ErrHostNotSigned) {
		t.Fatalf("want ErrHostNotSigned, got %v", err)
	}
}

func TestValidateRequestUnsignedAmzHeader(t *testing.T) {
	// Attacker appends an unsigned x-amz-acl header to a legitimately
	// signed URL.
	r := newRequest(t, "http://example.com/b?"+validQuery(), map[string]string{
		"X-Amz-Acl": "public-read",
	})
	if err := ValidateRequest(r, signedAt); !errors.Is(err, ErrUnsignedAmzHeader) {
		t.Fatalf("want ErrUnsignedAmzHeader, got %v", err)
	}

	// The same header is fine when covered by X-Amz-SignedHeaders.
	q := strings.Replace(validQuery(), "X-Amz-SignedHeaders=host", "X-Amz-SignedHeaders=host%3Bx-amz-acl", 1)
	r = newRequest(t, "http://example.com/b?"+q, map[string]string{
		"X-Amz-Acl": "public-read",
	})
	if err := ValidateRequest(r, signedAt); err != nil {
		t.Fatalf("signed x-amz-acl header rejected: %v", err)
	}
}

func TestValidateRequestExpiry(t *testing.T) {
	r := newRequest(t, "http://example.com/b?"+validQuery(), nil)

	// Boundary: now == signedAt + expires is still valid.
	if err := ValidateRequest(r, signedAt.Add(600*time.Second)); err != nil {
		t.Fatalf("boundary expiry rejected: %v", err)
	}
	// One second past the validity window must be rejected.
	if err := ValidateRequest(r, signedAt.Add(601*time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestValidateRequestMissingParams(t *testing.T) {
	for _, drop := range []string{"X-Amz-Signature", "X-Amz-Date", "X-Amz-Expires", "X-Amz-SignedHeaders", "X-Amz-Algorithm"} {
		var kept []string
		for _, p := range strings.Split(validQuery(), "&") {
			if !strings.HasPrefix(p, drop+"=") {
				kept = append(kept, p)
			}
		}
		r := newRequest(t, "http://example.com/b?"+strings.Join(kept, "&"), nil)
		if err := ValidateRequest(r, signedAt); !errors.Is(err, ErrMissingQueryParam) {
			t.Fatalf("dropping %s: want ErrMissingQueryParam, got %v", drop, err)
		}
	}
}

func TestCanonicalQueryStringSortsAndDropsSignature(t *testing.T) {
	raw := "X-Amz-Signature=abc&b=2&X-Amz-Expires=600&a=1&X-Amz-Date=20261113T120000Z"
	got, err := CanonicalQueryString(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "X-Amz-Date=20261113T120000Z&X-Amz-Expires=600&a=1&b=2"
	if got != want {
		t.Fatalf("canonical query mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestCanonicalQueryStringRejectsDuplicates(t *testing.T) {
	if _, err := CanonicalQueryString("X-Amz-Expires=1&X-Amz-Expires=2"); !errors.Is(err, ErrDuplicateQueryParam) {
		t.Fatalf("want ErrDuplicateQueryParam, got %v", err)
	}
}

func TestEncodedSlashInPath(t *testing.T) {
	// A key containing an encoded slash must keep %2F verbatim in the
	// canonical path: not decoded to '/', not double-encoded to %252F.
	r := newRequest(t, "http://example.com/bucket/a%2Fb.txt?"+validQuery(), nil)
	if got := CanonicalPath(r.URL.EscapedPath()); got != "/bucket/a%2Fb.txt" {
		t.Fatalf("canonical path mangled: %q", got)
	}
	if err := ValidateRequest(r, signedAt.Add(time.Minute)); err != nil {
		t.Fatalf("request with %%2F path rejected: %v", err)
	}
	if got := CanonicalPath(""); got != "/" {
		t.Fatalf("empty path should canonicalize to /, got %q", got)
	}
}
