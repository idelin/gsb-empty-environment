package netsec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testAuthReqID = "auth-req-1234"
	testToken     = "0123456789abcdef0123456789ab" // 30 chars, inside [22, 1024]
)

type fakeFetcher struct {
	mu        sync.Mutex
	calls     []string
	failWith  error
	responses map[string]*TokenResponse
}

func (f *fakeFetcher) FetchToken(_ context.Context, authReqID string) (*TokenResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, authReqID)
	if f.failWith != nil {
		return nil, f.failWith
	}
	if response, ok := f.responses[authReqID]; ok {
		copy := *response
		return &copy, nil
	}
	return nil, errors.New("unexpected auth_req_id: " + authReqID)
}

func (f *fakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestClient(t *testing.T) (*Client, *fakeFetcher) {
	t.Helper()
	fake := &fakeFetcher{
		responses: map[string]*TokenResponse{
			testAuthReqID: {
				AccessToken: "token-from-token-endpoint",
				TokenType:   "Bearer",
				ExpiresIn:   time.Hour,
			},
		},
	}
	return NewClient(fake), fake
}

func pingRequest(method, authReqID, bearer string, body any) *http.Request {
	var reader io.Reader
	switch value := body.(type) {
	case nil:
		reader = strings.NewReader("")
	case string:
		reader = strings.NewReader(value)
	default:
		raw, _ := json.Marshal(value)
		reader = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, "/ciba-ping", reader)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

func servePing(client *Client, req *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	client.ServeHTTP(recorder, req)
	return recorder
}

func TestValidateNotificationTokenLength(t *testing.T) {
	cases := []struct {
		name  string
		token string
		valid bool
	}{
		{"empty", "", false},
		{"too short by one", strings.Repeat("a", 21), false},
		{"minimum length", strings.Repeat("a", 22), true},
		{"maximum length", strings.Repeat("a", 1024), true},
		{"too long by one", strings.Repeat("a", 1025), false},
		// Length is counted in characters (runes), not bytes.
		{"multibyte minimum length", strings.Repeat("中", 22), true},
		{"multibyte too short", strings.Repeat("中", 21), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNotificationToken(tc.token)
			if tc.valid && err != nil {
				t.Fatalf("expected token to be accepted, got %v", err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalidNotificationToken) {
				t.Fatalf("expected ErrInvalidNotificationToken, got %v", err)
			}
		})
	}
}

func TestPrepareAuthRequestRejectsInvalidTokenBeforeRegistration(t *testing.T) {
	client, _ := newTestClient(t)
	err := client.PrepareAuthRequest(testAuthReqID, strings.Repeat("x", 21), time.Minute)
	if !errors.Is(err, ErrInvalidNotificationToken) {
		t.Fatalf("expected ErrInvalidNotificationToken, got %v", err)
	}
	if _, exists := client.requests[testAuthReqID]; exists {
		t.Fatal("invalid token must not register any request state")
	}

	if err := client.PrepareAuthRequest("", testToken, time.Minute); !errors.Is(err, ErrInvalidAuthReqID) {
		t.Fatalf("expected ErrInvalidAuthReqID, got %v", err)
	}
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, 0); !errors.Is(err, ErrInvalidExpiresIn) {
		t.Fatalf("expected ErrInvalidExpiresIn, got %v", err)
	}
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatalf("valid registration failed: %v", err)
	}
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); !errors.Is(err, ErrAuthRequestExists) {
		t.Fatalf("expected ErrAuthRequestExists, got %v", err)
	}
}

func TestOnlyPOSTAccepted(t *testing.T) {
	client, _ := newTestClient(t)
	recorder := servePing(client, pingRequest(http.MethodGet, testAuthReqID, testToken, nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", recorder.Code)
	}
	if recorder.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("expected Allow: POST, got %q", recorder.Header().Get("Allow"))
	}
}

func TestForeignAuthReqIDRejected(t *testing.T) {
	client, fake := newTestClient(t)
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}

	recorder := servePing(client, pingRequest(http.MethodPost, "someone-elses-auth-req-id", testToken,
		map[string]string{"auth_req_id": "someone-elses-auth-req-id"}))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for foreign auth_req_id, got %d", recorder.Code)
	}
	if fake.callCount() != 0 {
		t.Fatalf("token endpoint must not be called, calls=%v", fake.calls)
	}
}

func TestWrongNotificationTokenRejected(t *testing.T) {
	client, fake := newTestClient(t)
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		header string
	}{
		{"wrong bearer", "Bearer definitely-the-wrong-token-value"},
		{"basic scheme", "Basic " + base64.StdEncoding.EncodeToString([]byte("a:b"))},
		{"missing", ""},
		{"empty bearer", "Bearer "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := pingRequest(http.MethodPost, testAuthReqID, "", map[string]string{"auth_req_id": testAuthReqID})
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			recorder := servePing(client, req)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", recorder.Code)
			}
			if client.IsFinalized(testAuthReqID) {
				t.Fatal("request must not be finalized on a forged notification")
			}
		})
	}
	if fake.callCount() != 0 {
		t.Fatalf("token endpoint must not be called on bad token, calls=%v", fake.calls)
	}
}

func TestMalformedBodyRejected(t *testing.T) {
	client, fake := newTestClient(t)
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}

	cases := []string{
		"not json at all",
		`{"auth_req_id":""}`,
		`{}`,
	}
	for _, body := range cases {
		recorder := servePing(client, pingRequest(http.MethodPost, testAuthReqID, testToken, body))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %q: expected 400, got %d", body, recorder.Code)
		}
	}
	if fake.callCount() != 0 {
		t.Fatalf("token endpoint must not be called on bad body, calls=%v", fake.calls)
	}
}

func TestValidPingFetchesTokenAndIgnoresBodyTokenFields(t *testing.T) {
	client, fake := newTestClient(t)
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}

	// An attacker injects token material into the notification body; it
	// must never be trusted.
	body := map[string]any{
		"auth_req_id":  testAuthReqID,
		"access_token": "EVIL-ACCESS-TOKEN",
		"id_token":     "EVIL-ID-TOKEN",
		"token_type":   "Bearer",
		"expires_in":   1,
	}
	recorder := servePing(client, pingRequest(http.MethodPost, testAuthReqID, testToken, body))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if fake.callCount() != 1 || fake.calls[0] != testAuthReqID {
		t.Fatalf("expected exactly one token fetch for %q, got %v", testAuthReqID, fake.calls)
	}

	token, ok := client.Token(testAuthReqID)
	if !ok {
		t.Fatal("expected stored token response")
	}
	if token.AccessToken != "token-from-token-endpoint" {
		t.Fatalf("token must come from the token endpoint, got %q", token.AccessToken)
	}
	if !client.IsFinalized(testAuthReqID) {
		t.Fatal("request should be finalized after a successful fetch")
	}
}

func TestDuplicateNotificationIgnored(t *testing.T) {
	client, fake := newTestClient(t)
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}

	body := map[string]string{"auth_req_id": testAuthReqID}
	first := servePing(client, pingRequest(http.MethodPost, testAuthReqID, testToken, body))
	if first.Code != http.StatusNoContent {
		t.Fatalf("first ping: expected 204, got %d", first.Code)
	}
	second := servePing(client, pingRequest(http.MethodPost, testAuthReqID, testToken, body))
	if second.Code != http.StatusNoContent {
		t.Fatalf("replayed ping: expected 204, got %d", second.Code)
	}
	if fake.callCount() != 1 {
		t.Fatalf("duplicate notification must not trigger a second fetch, calls=%v", fake.calls)
	}
}

func TestLateNotificationAfterExpiryRejected(t *testing.T) {
	client, fake := newTestClient(t)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return now }
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}

	// One nanosecond before expiry: still acceptable.
	now = now.Add(time.Minute - time.Nanosecond)
	recorder := servePing(client, pingRequest(http.MethodPost, testAuthReqID, testToken,
		map[string]string{"auth_req_id": testAuthReqID}))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("pre-expiry ping: expected 204, got %d", recorder.Code)
	}

	// A fresh request whose notification arrives after its expiry.
	const lateID = "late-auth-req"
	if err := client.PrepareAuthRequest(lateID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute) // past the late request's expiry too
	recorder = servePing(client, pingRequest(http.MethodPost, lateID, testToken,
		map[string]string{"auth_req_id": lateID}))
	if recorder.Code != http.StatusGone {
		t.Fatalf("late notification: expected 410, got %d", recorder.Code)
	}
	if fake.callCount() != 1 {
		t.Fatalf("expired notification must not reach the token endpoint, calls=%v", fake.calls)
	}
	if client.IsFinalized(lateID) {
		t.Fatal("expired request must not be finalized")
	}
}

func TestTokenFetchFailureAllowsRetry(t *testing.T) {
	failErr := errors.New("network unavailable")
	fake := &fakeFetcher{
		failWith: failErr,
		responses: map[string]*TokenResponse{
			testAuthReqID: {AccessToken: "recovered-token"},
		},
	}
	client := NewClient(fake)
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}

	body := map[string]string{"auth_req_id": testAuthReqID}
	first := servePing(client, pingRequest(http.MethodPost, testAuthReqID, testToken, body))
	if first.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on fetch failure, got %d", first.Code)
	}
	err, ok := client.FetchError(testAuthReqID)
	if !ok || !errors.Is(err, failErr) {
		t.Fatalf("expected stored fetch error, got %v (ok=%v)", err, ok)
	}

	// The failed attempt must not count as an accepted notification.
	fake.failWith = nil
	second := servePing(client, pingRequest(http.MethodPost, testAuthReqID, testToken, body))
	if second.Code != http.StatusNoContent {
		t.Fatalf("retry ping: expected 204, got %d", second.Code)
	}
	token, ok := client.Token(testAuthReqID)
	if !ok || token.AccessToken != "recovered-token" {
		t.Fatalf("expected recovered token, got %+v (ok=%v)", token, ok)
	}
}

func TestMalformedBearerSchemeRejected(t *testing.T) {
	client, fake := newTestClient(t)
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}

	for _, header := range []string{
		"Basic " + base64.StdEncoding.EncodeToString([]byte("a:b")),
		"Bearer",
		"Bearer\t" + testToken,
		"Token " + testToken,
	} {
		req := pingRequest(http.MethodPost, testAuthReqID, "", map[string]string{"auth_req_id": testAuthReqID})
		req.Header.Set("Authorization", header)
		if recorder := servePing(client, req); recorder.Code != http.StatusUnauthorized {
			t.Fatalf("header %q: expected 401, got %d", header, recorder.Code)
		}
	}
	if fake.callCount() != 0 {
		t.Fatalf("no token fetch expected, calls=%v", fake.calls)
	}
}

func TestNilFetcherRejectedAndRetriable(t *testing.T) {
	client := NewClient(nil)
	if err := client.PrepareAuthRequest(testAuthReqID, testToken, time.Minute); err != nil {
		t.Fatal(err)
	}

	recorder := servePing(client, pingRequest(http.MethodPost, testAuthReqID, testToken,
		map[string]string{"auth_req_id": testAuthReqID}))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 without a fetcher, got %d", recorder.Code)
	}
	if client.IsFinalized(testAuthReqID) {
		t.Fatal("request must not be finalized without a fetcher")
	}
	if _, ok := client.FetchError(testAuthReqID); !ok {
		t.Fatal("expected a stored fetch error")
	}

	// Wiring a fetcher afterwards must make a retransmitted ping work.
	recovered := &fakeFetcher{responses: map[string]*TokenResponse{
		testAuthReqID: {AccessToken: "late-bound-token"},
	}}
	client.fetcher = recovered
	recorder = servePing(client, pingRequest(http.MethodPost, testAuthReqID, testToken,
		map[string]string{"auth_req_id": testAuthReqID}))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("retry: expected 204, got %d", recorder.Code)
	}
	token, ok := client.Token(testAuthReqID)
	if !ok || token.AccessToken != "late-bound-token" {
		t.Fatalf("expected late-bound token, got %+v (ok=%v)", token, ok)
	}
}

func TestAccessorsForUnknownAuthReqID(t *testing.T) {
	client, _ := newTestClient(t)
	if client.IsFinalized("unknown") {
		t.Fatal("unknown request must not be finalized")
	}
	if token, ok := client.Token("unknown"); ok || token != nil {
		t.Fatalf("unknown request must have no token, got %+v", token)
	}
	if err, ok := client.FetchError("unknown"); ok || err != nil {
		t.Fatalf("unknown request must have no fetch error, got %v", err)
	}
}

func TestParseBearerEdgeCases(t *testing.T) {
	t.Run("lowercase scheme accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Authorization", "bearer "+testToken)
		token, err := parseBearer(req)
		if err != nil || token != testToken {
			t.Fatalf("expected token %q, got %q (err=%v)", testToken, token, err)
		}
	})

	t.Run("token with internal whitespace rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Authorization", "Bearer part one")
		if _, err := parseBearer(req); !errors.Is(err, ErrMalformedAuthorization) {
			t.Fatalf("expected ErrMalformedAuthorization, got %v", err)
		}
	})

	t.Run("missing header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if _, err := parseBearer(req); !errors.Is(err, ErrMissingBearerToken) {
			t.Fatalf("expected ErrMissingBearerToken, got %v", err)
		}
	})
}
