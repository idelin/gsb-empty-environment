package netsec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeValidTokenFlow(t *testing.T) {
	g := NewGuard()

	tokenReq := httptest.NewRequest(http.MethodPut, TokenPath, nil)
	tokenReq.Header.Set(TokenTTLHeader, "60")
	decision, err := g.Authorize(tokenReq, "container-a")
	if err != nil {
		t.Fatalf("Authorize token request: %v", err)
	}
	if decision.Action != ActionIssueToken {
		t.Fatalf("action = %v, want %v", decision.Action, ActionIssueToken)
	}
	if decision.Token == "" {
		t.Fatal("issued token is empty")
	}
	if decision.TTLSeconds() != 60 {
		t.Fatalf("TTL = %d seconds, want 60", decision.TTLSeconds())
	}
	if decision.ExpiresAt.IsZero() {
		t.Fatal("token expiry was not set")
	}

	metaReq := metadataRequest(decision.Token)
	metaDecision, err := g.Authorize(metaReq, "container-a")
	if err != nil {
		t.Fatalf("Authorize metadata request: %v", err)
	}
	if metaDecision.Action != ActionForwardMetadata {
		t.Fatalf("action = %v, want %v", metaDecision.Action, ActionForwardMetadata)
	}
}

func TestRejectsForwardingHeaders(t *testing.T) {
	g := NewGuard()
	tokenReq := httptest.NewRequest(http.MethodPut, TokenPath, nil)
	tokenReq.Header.Set(TokenTTLHeader, "60")
	decision, err := g.Authorize(tokenReq, "container-a")
	if err != nil {
		t.Fatalf("Authorize token request: %v", err)
	}

	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "x-forwarded-for", header: "X-Forwarded-For", value: "203.0.113.7"},
		{name: "forwarded", header: "Forwarded", value: "for=203.0.113.7"},
		{name: "via", header: "Via", value: "1.1 proxy"},
		{name: "empty x-forwarded-for", header: "X-Forwarded-For", value: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name+"/token", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, TokenPath, nil)
			req.Header.Set(TokenTTLHeader, "60")
			req.Header.Set(tc.header, tc.value)
			_, err := g.Authorize(req, "container-a")
			if !errors.Is(err, ErrForwardedHeader) {
				t.Fatalf("error = %v, want %v", err, ErrForwardedHeader)
			}
		})

		t.Run(tc.name+"/metadata", func(t *testing.T) {
			req := metadataRequest(decision.Token)
			req.Header.Set(tc.header, tc.value)
			_, err := g.Authorize(req, "container-a")
			if !errors.Is(err, ErrForwardedHeader) {
				t.Fatalf("error = %v, want %v", err, ErrForwardedHeader)
			}
		})
	}
}

func TestRejectsCrossContainerToken(t *testing.T) {
	g := NewGuard()

	tokenReq := httptest.NewRequest(http.MethodPut, TokenPath, nil)
	tokenReq.Header.Set(TokenTTLHeader, "60")
	decision, err := g.Authorize(tokenReq, "container-a")
	if err != nil {
		t.Fatalf("Authorize token request: %v", err)
	}

	_, err = g.Authorize(metadataRequest(decision.Token), "container-b")
	if !errors.Is(err, ErrTokenContainerMismatch) {
		t.Fatalf("cross-container error = %v, want %v", err, ErrTokenContainerMismatch)
	}

	ttl, err := g.ValidateTokenRequest(tokenReq)
	if err != nil {
		t.Fatalf("ValidateTokenRequest: %v", err)
	}
	if _, err := g.BindToken("upstream-token-a", "container-a", time.Duration(ttl)*time.Second); err != nil {
		t.Fatalf("BindToken: %v", err)
	}
	err = g.CheckMetadataRequest(tokenHeaderRequest("upstream-token-a"), "container-b")
	if !errors.Is(err, ErrTokenContainerMismatch) {
		t.Fatalf("bound cross-container error = %v, want %v", err, ErrTokenContainerMismatch)
	}
}

func TestForwardingFlowBindsIMDSToken(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	g := NewGuard(WithClock(func() time.Time { return now }))

	tokenReq := httptest.NewRequest(http.MethodPut, TokenPath, nil)
	tokenReq.Header.Set(TokenTTLHeader, "300")
	ttl, err := g.ValidateTokenRequest(tokenReq)
	if err != nil {
		t.Fatalf("ValidateTokenRequest: %v", err)
	}

	expiresAt, err := g.BindToken("imds-issued-token", "container-a", time.Duration(ttl)*time.Second)
	if err != nil {
		t.Fatalf("BindToken: %v", err)
	}
	if want := now.Add(300 * time.Second); !expiresAt.Equal(want) {
		t.Fatalf("expiry = %v, want %v", expiresAt, want)
	}
	if err := g.CheckMetadataRequest(tokenHeaderRequest("imds-issued-token"), "container-a"); err != nil {
		t.Fatalf("metadata with bound IMDS token: %v", err)
	}
}

func TestTokenTTLRange(t *testing.T) {
	tests := []struct {
		name      string
		values    []string
		want      int
		wantError error
	}{
		{name: "missing", values: nil, wantError: ErrTokenTTLMissing},
		{name: "zero", values: []string{"0"}, wantError: ErrTokenTTLOutOfRange},
		{name: "above maximum", values: []string{"21601"}, wantError: ErrTokenTTLOutOfRange},
		{name: "integer overflow", values: []string{"999999999999999999999999999"}, wantError: ErrTokenTTLOutOfRange},
		{name: "negative", values: []string{"-1"}, wantError: ErrTokenTTLMalformed},
		{name: "not an integer", values: []string{"1.5"}, wantError: ErrTokenTTLMalformed},
		{name: "signed integer", values: []string{"+60"}, wantError: ErrTokenTTLMalformed},
		{name: "exponent", values: []string{"1e2"}, wantError: ErrTokenTTLMalformed},
		{name: "empty", values: []string{""}, wantError: ErrTokenTTLMalformed},
		{name: "duplicate", values: []string{"60", "60"}, wantError: ErrTokenTTLMalformed},
		{name: "minimum", values: []string{"1"}, want: 1},
		{name: "maximum", values: []string{"21600"}, want: 21600},
		{name: "surrounding whitespace", values: []string{"  60  "}, want: 60},
	}

	g := NewGuard()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, TokenPath, nil)
			for _, value := range tc.values {
				req.Header.Add(TokenTTLHeader, value)
			}
			ttl, err := g.ValidateTokenRequest(req)
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("error = %v, want %v", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateTokenRequest: %v", err)
			}
			if ttl != tc.want {
				t.Fatalf("TTL = %d, want %d", ttl, tc.want)
			}
		})
	}
}

func TestGETMustNotCarryTokenTTLHeader(t *testing.T) {
	g := NewGuard()
	tokenReq := httptest.NewRequest(http.MethodPut, TokenPath, nil)
	tokenReq.Header.Set(TokenTTLHeader, "60")
	decision, err := g.Authorize(tokenReq, "container-a")
	if err != nil {
		t.Fatalf("Authorize token request: %v", err)
	}

	req := metadataRequest(decision.Token)
	req.Header.Set(TokenTTLHeader, "60")
	_, err = g.Authorize(req, "container-a")
	if !errors.Is(err, ErrTokenTTLOnRead) {
		t.Fatalf("GET with TTL error = %v, want %v", err, ErrTokenTTLOnRead)
	}

	headReq := httptest.NewRequest(http.MethodHead, "/latest/meta-data/", nil)
	headReq.Header.Set(TokenHeader, decision.Token)
	headReq.Header.Set(TokenTTLHeader, "60")
	_, err = g.Authorize(headReq, "container-a")
	if !errors.Is(err, ErrTokenTTLOnRead) {
		t.Fatalf("HEAD with TTL error = %v, want %v", err, ErrTokenTTLOnRead)
	}
}

func TestTokenExpiry(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	g := NewGuard(WithClock(func() time.Time { return now }))

	tokenReq := httptest.NewRequest(http.MethodPut, TokenPath, nil)
	tokenReq.Header.Set(TokenTTLHeader, "60")
	decision, err := g.Authorize(tokenReq, "container-a")
	if err != nil {
		t.Fatalf("Authorize token request: %v", err)
	}

	now = now.Add(59 * time.Second)
	if err := g.ValidateToken(decision.Token, "container-a"); err != nil {
		t.Fatalf("token rejected before expiry: %v", err)
	}

	now = now.Add(time.Second)
	if err := g.ValidateToken(decision.Token, "container-a"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("token at expiry error = %v, want %v", err, ErrTokenExpired)
	}

	_, err = g.Authorize(metadataRequest(decision.Token), "container-a")
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("metadata with expired token error = %v, want %v", err, ErrTokenExpired)
	}
}

func TestPathTraversal(t *testing.T) {
	tests := []struct {
		path      string
		wantError error
	}{
		{path: "/latest/meta-data/../api/token", wantError: ErrPathTraversal},
		{path: "/latest/meta-data/%2e%2e/api/token", wantError: ErrPathTraversal},
		{path: "/latest/meta-data/%2E%2E/api/token", wantError: ErrPathTraversal},
		{path: "/latest/meta-data/.%2e/api/token", wantError: ErrPathTraversal},
		{path: "/latest/meta-data/..%5capi/token", wantError: ErrEncodedSlash},
		{path: "/latest/meta-data/..%2fapi/token", wantError: ErrEncodedSlash},
		{path: "/latest/meta-data/%2fetc/passwd", wantError: ErrEncodedSlash},
		{path: "/latest/meta-data/%2Fetc/passwd", wantError: ErrEncodedSlash},
		{path: "/latest/meta-data/%252fetc/passwd", wantError: ErrEncodedSlash},
		{path: "/latest/meta-data/%25252fetc/passwd", wantError: ErrEncodedSlash},
		{path: "/latest/meta-data/%2e%2e%2fapi/token", wantError: ErrEncodedSlash},
		{path: "/latest/meta-data/%00", wantError: ErrUnsafePath},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			err := ValidatePath(tc.path)
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("ValidatePath(%q) = %v, want %v", tc.path, err, tc.wantError)
			}
		})
	}

	if err := ValidatePath("/latest/meta-data/iam/security-credentials/role%20name"); err != nil {
		t.Fatalf("safe encoded path rejected: %v", err)
	}
}

func TestUnsafePathCannotBeBypassedWithValidToken(t *testing.T) {
	g := NewGuard()
	tokenReq := httptest.NewRequest(http.MethodPut, TokenPath, nil)
	tokenReq.Header.Set(TokenTTLHeader, "60")
	decision, err := g.Authorize(tokenReq, "container-a")
	if err != nil {
		t.Fatalf("Authorize token request: %v", err)
	}

	for _, path := range []string{
		"/latest/meta-data/%2e%2e/api/token",
		"/latest/meta-data/..%2fapi/token",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(TokenHeader, decision.Token)
		_, err := g.Authorize(req, "container-a")
		if !errors.Is(err, ErrPathTraversal) && !errors.Is(err, ErrEncodedSlash) {
			t.Fatalf("path %q error = %v, want traversal/encoded-slash rejection", path, err)
		}
	}
}

func TestTokenHeaderValidation(t *testing.T) {
	g := NewGuard()
	tokenReq := httptest.NewRequest(http.MethodPut, TokenPath, nil)
	tokenReq.Header.Set(TokenTTLHeader, "60")
	decision, err := g.Authorize(tokenReq, "container-a")
	if err != nil {
		t.Fatalf("Authorize token request: %v", err)
	}

	missing := metadataRequest("")
	_, err = g.Authorize(missing, "container-a")
	if !errors.Is(err, ErrTokenMissing) {
		t.Fatalf("missing token error = %v, want %v", err, ErrTokenMissing)
	}

	duplicate := metadataRequest("")
	duplicate.Header.Add(TokenHeader, decision.Token)
	duplicate.Header.Add(TokenHeader, decision.Token)
	_, err = g.Authorize(duplicate, "container-a")
	if !errors.Is(err, ErrTokenMalformed) {
		t.Fatalf("duplicate token error = %v, want %v", err, ErrTokenMalformed)
	}

	_, err = g.Authorize(metadataRequest("not-a-live-token"), "container-a")
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("invalid token error = %v, want %v", err, ErrTokenInvalid)
	}

	_, err = g.Authorize(metadataRequest(strings.Repeat("a", maxTokenLength+1)), "container-a")
	if !errors.Is(err, ErrTokenMalformed) {
		t.Fatalf("oversized token error = %v, want %v", err, ErrTokenMalformed)
	}
}

func TestTokenRequestPolicy(t *testing.T) {
	g := NewGuard()

	getReq := httptest.NewRequest(http.MethodGet, TokenPath, nil)
	getReq.Header.Set(TokenTTLHeader, "60")
	_, err := g.ValidateTokenRequest(getReq)
	if !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("GET token request error = %v, want %v", err, ErrMethodNotAllowed)
	}

	postReq := httptest.NewRequest(http.MethodPost, TokenPath, nil)
	postReq.Header.Set(TokenTTLHeader, "60")
	_, err = g.ValidateTokenRequest(postReq)
	if !errors.Is(err, ErrMethodNotAllowed) {
		t.Fatalf("POST token request error = %v, want %v", err, ErrMethodNotAllowed)
	}

	wrongPath := httptest.NewRequest(http.MethodPut, "/latest/meta-data/", nil)
	wrongPath.Header.Set(TokenTTLHeader, "60")
	_, err = g.ValidateTokenRequest(wrongPath)
	if !errors.Is(err, ErrNotTokenRequest) {
		t.Fatalf("wrong token path error = %v, want %v", err, ErrNotTokenRequest)
	}

	_, err = g.Authorize(httptest.NewRequest(http.MethodPut, TokenPath, nil), "container-a")
	if !errors.Is(err, ErrTokenTTLMissing) {
		t.Fatalf("PUT without TTL error = %v, want %v", err, ErrTokenTTLMissing)
	}
}

func metadataRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/iam/security-credentials/", nil)
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	return req
}

func tokenHeaderRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/latest/meta-data/", nil)
	req.Header.Set(TokenHeader, token)
	return req
}
