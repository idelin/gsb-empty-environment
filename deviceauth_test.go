package netsec

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Unix(1_700_000_000, 0).UTC()}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestManager(t *testing.T, cfg Config) (*Manager, *testClock) {
	t.Helper()
	clock := newTestClock()
	cfg.Now = clock.Now
	manager, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return manager, clock
}

func mustAuthorize(t *testing.T, manager *Manager, clientID string) Authorization {
	t.Helper()
	authorization, err := manager.Authorize(context.Background(), clientID)
	if err != nil {
		t.Fatalf("Authorize(%q) error = %v", clientID, err)
	}
	return authorization
}

func validSession(clientID string) ApprovalSession {
	return ApprovalSession{
		ID:       "session-1",
		Subject:  "alice",
		ClientID: clientID,
		Scope:    []string{"openid", "profile"},
		Claims:   map[string]string{"email": "alice@example.com"},
	}
}

func changedUserCode(t *testing.T, formatted string) string {
	t.Helper()
	normalized, err := NormalizeUserCode(formatted)
	if err != nil {
		t.Fatalf("NormalizeUserCode(%q) error = %v", formatted, err)
	}
	replacement := byte('0')
	if normalized[0] == '0' {
		replacement = '1'
	}
	return FormatUserCode(string(replacement) + normalized[1:])
}

func TestAuthorizationCodesAreSeparatedAndHighEntropy(t *testing.T) {
	manager, _ := newTestManager(t, Config{
		VerificationURI: "https://login.example/device",
	})

	seenDevice := make(map[string]struct{})
	seenUser := make(map[string]struct{})
	for i := 0; i < 8; i++ {
		authorization := mustAuthorize(t, manager, "client-a")

		rawDevice, err := base64.RawURLEncoding.DecodeString(authorization.DeviceCode)
		if err != nil {
			t.Fatalf("device code is not base64url: %v", err)
		}
		if len(rawDevice) != deviceCodeBytes {
			t.Fatalf("device code entropy = %d bytes, want %d", len(rawDevice), deviceCodeBytes)
		}

		normalized, err := NormalizeUserCode(authorization.UserCode)
		if err != nil {
			t.Fatalf("NormalizeUserCode() error = %v", err)
		}
		if len(normalized) != UserCodeLength {
			t.Fatalf("user code length = %d, want %d", len(normalized), UserCodeLength)
		}
		if len(authorization.UserCode) != 19 {
			t.Fatalf("displayed user code length = %d, want 19", len(authorization.UserCode))
		}
		if authorization.DeviceCode == normalized {
			t.Fatal("device code and user code must be separate values")
		}
		if authorization.ExpiresIn <= 0 || authorization.Interval <= 0 {
			t.Fatalf("invalid response timing: expires_in=%d interval=%d", authorization.ExpiresIn, authorization.Interval)
		}
		if !strings.Contains(authorization.VerificationURIComplete, "user_code=") {
			t.Fatalf("verification_uri_complete = %q, want user_code query", authorization.VerificationURIComplete)
		}

		if _, exists := seenDevice[authorization.DeviceCode]; exists {
			t.Fatal("duplicate device code")
		}
		seenDevice[authorization.DeviceCode] = struct{}{}
		if _, exists := seenUser[normalized]; exists {
			t.Fatal("duplicate user code")
		}
		seenUser[normalized] = struct{}{}
	}

	authorization := mustAuthorize(t, manager, "client-a")
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.UserCode); !errors.Is(err, ErrInvalidDeviceCode) {
		t.Fatalf("user code used as device code: error = %v, want ErrInvalidDeviceCode", err)
	}

	if _, err := manager.Approve(context.Background(), authorization.DeviceCode, validSession("client-a")); !errors.Is(err, ErrInvalidUserCode) {
		t.Fatalf("device code used as user code: error = %v, want ErrInvalidUserCode", err)
	}
}

func TestLegitimateFlowIssuesTokenOnlyToOriginalDevice(t *testing.T) {
	manager, clock := newTestManager(t, Config{
		PollInterval: 2 * time.Second,
		MaxPolls:     10,
	})

	original := mustAuthorize(t, manager, "client-a")
	otherDevice := mustAuthorize(t, manager, "client-a")

	if _, err := manager.Redeem(context.Background(), "client-a", original.DeviceCode); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("first poll error = %v, want ErrAuthorizationPending", err)
	}

	approval, err := manager.Approve(context.Background(), original.UserCode, validSession("client-a"))
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if approval.Subject != "alice" || approval.SessionID != "session-1" {
		t.Fatalf("approval metadata = %#v", approval)
	}

	token, err := manager.Redeem(context.Background(), "client-a", original.DeviceCode)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	if token.AccessToken == "" || token.TokenType != "Bearer" {
		t.Fatalf("token = %#v", token)
	}
	if token.ClientID != "client-a" || token.Subject != "alice" || token.SessionID != "session-1" {
		t.Fatalf("token binding = client:%q subject:%q session:%q", token.ClientID, token.Subject, token.SessionID)
	}
	if len(token.Scope) != 2 || token.Scope[0] != "openid" || token.Scope[1] != "profile" {
		t.Fatalf("token scope = %#v", token.Scope)
	}

	if _, err := manager.Redeem(context.Background(), "client-a", original.DeviceCode); !errors.Is(err, ErrAlreadyUsed) {
		t.Fatalf("second redemption error = %v, want ErrAlreadyUsed", err)
	}
	if _, err := manager.Redeem(context.Background(), "client-a", otherDevice.DeviceCode); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("other device received an unexpected result: %v", err)
	}

	clock.Advance(2 * time.Second)
	if _, err := manager.Redeem(context.Background(), "client-a", otherDevice.DeviceCode); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("unapproved other device error = %v, want ErrAuthorizationPending", err)
	}
}

func TestPollIntervalAndPollLimitAreEnforced(t *testing.T) {
	manager, clock := newTestManager(t, Config{
		PollInterval: time.Second,
		MaxPolls:     3,
	})
	authorization := mustAuthorize(t, manager, "client-a")

	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("first poll error = %v, want ErrAuthorizationPending", err)
	}
	clock.Advance(500 * time.Millisecond)
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrSlowDown) {
		t.Fatalf("early poll error = %v, want ErrSlowDown", err)
	}
	clock.Advance(time.Second)
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("poll after interval error = %v, want ErrAuthorizationPending", err)
	}

	// Poll 4 exceeds MaxPolls and permanently burns the device code.
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrPollLimitExceeded) {
		t.Fatalf("over-budget poll error = %v, want ErrPollLimitExceeded", err)
	}
	if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); !errors.Is(err, ErrPollLimitExceeded) {
		t.Fatalf("approval after poll limit error = %v, want ErrPollLimitExceeded", err)
	}
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrPollLimitExceeded) {
		t.Fatalf("redemption after poll limit error = %v, want ErrPollLimitExceeded", err)
	}
	if code := CodeOf(ErrPollLimitExceeded); code != CodeExpiredToken {
		t.Fatalf("poll-limit OAuth code = %q, want %q", code, CodeExpiredToken)
	}
}

func TestApprovalAtPollingBudgetStillRedeems(t *testing.T) {
	manager, clock := newTestManager(t, Config{
		PollInterval: time.Second,
		MaxPolls:     3,
	})
	authorization := mustAuthorize(t, manager, "client-a")

	for i := 0; i < 3; i++ {
		if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrAuthorizationPending) {
			t.Fatalf("poll %d error = %v, want ErrAuthorizationPending", i+1, err)
		}
		clock.Advance(time.Second)
	}

	if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); err != nil {
		t.Fatalf("redemption at the polling budget error = %v", err)
	}
}

func TestUserCodeGuessingIsRateLimited(t *testing.T) {
	manager, clock := newTestManager(t, Config{
		PollInterval:          time.Second,
		UserCodeFailureLimit:  3,
		UserCodeFailureWindow: time.Minute,
	})
	authorization := mustAuthorize(t, manager, "client-a")
	wrongCode := changedUserCode(t, authorization.UserCode)

	for i := 1; i <= 3; i++ {
		_, err := manager.Approve(context.Background(), wrongCode, validSession("client-a"))
		if i < 3 && !errors.Is(err, ErrInvalidUserCode) {
			t.Fatalf("guess %d error = %v, want ErrInvalidUserCode", i, err)
		}
		if i == 3 && !errors.Is(err, ErrTooManyAttempts) {
			t.Fatalf("guess %d error = %v, want ErrTooManyAttempts", i, err)
		}
	}

	if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("valid approval during lockout error = %v, want ErrTooManyAttempts", err)
	}
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("guessing changed device state: %v", err)
	}

	clock.Advance(time.Minute + time.Second)
	if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); err != nil {
		t.Fatalf("valid approval after lockout error = %v", err)
	}
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); err != nil {
		t.Fatalf("Redeem() after lockout error = %v", err)
	}
}

func TestCrossClientRedemptionIsRejected(t *testing.T) {
	manager, _ := newTestManager(t, Config{PollInterval: time.Second})

	authorization := mustAuthorize(t, manager, "client-a")
	if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}

	if _, err := manager.Redeem(context.Background(), "client-b", authorization.DeviceCode); !errors.Is(err, ErrClientMismatch) {
		t.Fatalf("cross-client redemption error = %v, want ErrClientMismatch", err)
	}

	token, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode)
	if err != nil {
		t.Fatalf("original client redemption error = %v", err)
	}
	if token.ClientID != "client-a" {
		t.Fatalf("token client = %q, want client-a", token.ClientID)
	}

	other := mustAuthorize(t, manager, "client-c")
	session := validSession("client-d")
	if _, err := manager.Approve(context.Background(), other.UserCode, session); !errors.Is(err, ErrClientMismatch) {
		t.Fatalf("cross-client approval session error = %v, want ErrClientMismatch", err)
	}
	if _, err := manager.Redeem(context.Background(), "client-d", other.DeviceCode); !errors.Is(err, ErrClientMismatch) {
		t.Fatalf("unapproved cross-client device error = %v, want ErrClientMismatch", err)
	}
	if _, err := manager.Redeem(context.Background(), "client-c", other.DeviceCode); !errors.Is(err, ErrAuthorizationPending) {
		t.Fatalf("original client pending error = %v, want ErrAuthorizationPending", err)
	}
}

func TestExpiredDeviceCodeAndApprovalAreRejected(t *testing.T) {
	t.Run("device code", func(t *testing.T) {
		manager, clock := newTestManager(t, Config{DeviceCodeTTL: 10 * time.Minute})
		authorization := mustAuthorize(t, manager, "client-a")

		clock.Advance(10*time.Minute + time.Second)
		if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); !errors.Is(err, ErrExpiredToken) {
			t.Fatalf("expired approval error = %v, want ErrExpiredToken", err)
		}
		if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); err == nil {
			t.Fatal("expired device code produced a token")
		}
	})

	t.Run("approval session", func(t *testing.T) {
		manager, clock := newTestManager(t, Config{
			DeviceCodeTTL: time.Hour,
			ApprovalTTL:   10 * time.Minute,
		})
		authorization := mustAuthorize(t, manager, "client-a")
		if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); err != nil {
			t.Fatalf("Approve() error = %v", err)
		}

		clock.Advance(10*time.Minute + time.Second)
		if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrExpiredToken) {
			t.Fatalf("expired approval redemption error = %v, want ErrExpiredToken", err)
		}
	})

	t.Run("issuer completes after expiry", func(t *testing.T) {
		clock := newTestClock()
		manager, err := New(Config{
			DeviceCodeTTL: 10 * time.Minute,
			ApprovalTTL:   10 * time.Minute,
			Now:           clock.Now,
			IssueToken: func(_ context.Context, _ Grant) (string, error) {
				clock.Advance(11 * time.Minute)
				return "late-token", nil
			},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		authorization := mustAuthorize(t, manager, "client-a")
		if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); err != nil {
			t.Fatalf("Approve() error = %v", err)
		}
		if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrExpiredToken) {
			t.Fatalf("late issuer redemption error = %v, want ErrExpiredToken", err)
		}
	})
}

func TestConcurrentRedemptionIssuesExactlyOneToken(t *testing.T) {
	manager, _ := newTestManager(t, Config{
		PollInterval: time.Second,
		MaxPolls:     1000,
	})
	authorization := mustAuthorize(t, manager, "client-a")
	if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}

	const goroutines = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var tokens []Token
	var errorsSeen []error

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			token, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errorsSeen = append(errorsSeen, err)
				return
			}
			tokens = append(tokens, token)
		}()
	}
	close(start)
	wg.Wait()

	if len(tokens) != 1 {
		t.Fatalf("issued %d tokens, want exactly 1", len(tokens))
	}
	if len(errorsSeen) != goroutines-1 {
		t.Fatalf("saw %d errors, want %d", len(errorsSeen), goroutines-1)
	}
	for _, err := range errorsSeen {
		if !errors.Is(err, ErrAlreadyUsed) {
			t.Fatalf("concurrent redemption error = %v, want ErrAlreadyUsed", err)
		}
	}
	if tokens[0].Subject != "alice" || tokens[0].ClientID != "client-a" {
		t.Fatalf("issued token binding = %#v", tokens[0])
	}
}

func TestDenyAndTokenIssuerFailureDoNotIssueTokens(t *testing.T) {
	manager, _ := newTestManager(t, Config{PollInterval: time.Second})
	authorization := mustAuthorize(t, manager, "client-a")

	if err := manager.Deny(context.Background(), authorization.UserCode); err != nil {
		t.Fatalf("Deny() error = %v", err)
	}
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("denied redemption error = %v, want ErrAccessDenied", err)
	}

	issuerCalls := 0
	manager, _ = newTestManager(t, Config{
		PollInterval: time.Second,
		IssueToken: func(_ context.Context, _ Grant) (string, error) {
			issuerCalls++
			if issuerCalls == 1 {
				return "", errors.New("temporary issuer failure")
			}
			return "custom-token", nil
		},
	})
	authorization = mustAuthorize(t, manager, "client-a")
	if _, err := manager.Approve(context.Background(), authorization.UserCode, validSession("client-a")); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}

	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrTokenGeneration) {
		t.Fatalf("issuer failure error = %v, want ErrTokenGeneration", err)
	}
	token, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode)
	if err != nil {
		t.Fatalf("retry after issuer failure error = %v", err)
	}
	if token.AccessToken != "custom-token" {
		t.Fatalf("token = %q, want custom-token", token.AccessToken)
	}
	if _, err := manager.Redeem(context.Background(), "client-a", authorization.DeviceCode); !errors.Is(err, ErrAlreadyUsed) {
		t.Fatalf("post-issuer redemption error = %v, want ErrAlreadyUsed", err)
	}
}

func TestInvalidConfigIsRejected(t *testing.T) {
	_, err := New(Config{PollInterval: -time.Second})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("New() error = %v, want ErrInvalidRequest", err)
	}

	if _, err := New(Config{}); err != nil {
		t.Fatalf("New(Config{}) error = %v", err)
	}
}

func ExampleManager() {
	manager, err := New(Config{
		DeviceCodeTTL: 10 * time.Minute,
		PollInterval:  5 * time.Second,
		MaxPolls:      30,
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	authorization, err := manager.Authorize(context.Background(), "mobile-client")
	if err != nil {
		fmt.Println(err)
		return
	}

	_, _ = manager.Approve(context.Background(), authorization.UserCode, ApprovalSession{
		ID:      "authenticated-session",
		Subject: "alice",
	})

	token, err := manager.Redeem(context.Background(), "mobile-client", authorization.DeviceCode)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(token.TokenType)
	// Output: Bearer
}
