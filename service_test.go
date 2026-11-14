package netsec_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"example.org/netsec/q0274"
)

type fakeClock struct {
	mutex sync.Mutex
	now   time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)}
}

func (clock *fakeClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.now = clock.now.Add(duration)
}

func newTestService(clock *fakeClock) *netsec.Service {
	return netsec.New(netsec.Config{
		PollInterval:  2 * time.Second,
		DeviceCodeTTL: time.Minute,
		TokenTTL:      15 * time.Minute,
		Now:           clock.Now,
	})
}

func begin(t *testing.T, service *netsec.Service, clientID string, scopes ...string) *netsec.Authorization {
	t.Helper()
	authorization, err := service.Begin(context.Background(), clientID, scopes)
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	return authorization
}

func approve(t *testing.T, service *netsec.Service, userCode, subject string) {
	t.Helper()
	if err := service.Approve(context.Background(), userCode, subject); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
}

func approveScopes(t *testing.T, service *netsec.Service, userCode, subject string, scopes ...string) {
	t.Helper()
	if err := service.ApproveScopes(context.Background(), userCode, subject, scopes); err != nil {
		t.Fatalf("ApproveScopes() error = %v", err)
	}
}

func assertInvalidGrant(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an invalid_grant error, got nil")
	}
	if !errors.Is(err, netsec.ErrInvalidGrant) {
		t.Fatalf("error = %v, want ErrInvalidGrant", err)
	}
	if !errors.Is(err, netsec.ErrAccessDenied) {
		t.Fatalf("error = %v, want it to compare as ErrAccessDenied", err)
	}
}

func assertScopes(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("scopes = %v, want %v", got, want)
	}
}

func TestScopeEscalationIsRejectedWithoutConsumingCode(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read")
	approveScopes(t, service, authorization.UserCode, "alice", "read")

	token, err := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
		Scopes:     []string{"read", "admin"},
	})
	if token != nil {
		t.Fatalf("escalated exchange returned token %+v", token)
	}
	assertInvalidGrant(t, err)

	token, err = service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
		Scopes:     []string{"write"},
	})
	if token != nil {
		t.Fatalf("unapproved scope exchange returned token %+v", token)
	}
	assertInvalidGrant(t, err)

	token, err = service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
		Scopes:     []string{"read"},
	})
	if err != nil {
		t.Fatalf("legitimate low-scope exchange error = %v", err)
	}
	assertScopes(t, token.Scopes, []string{"read"})
	if token.Subject != "alice" || token.ClientID != "client-a" {
		t.Fatalf("token binding = subject %q client %q", token.Subject, token.ClientID)
	}
	if token.AccessToken == "" || token.TokenType != "Bearer" {
		t.Fatalf("token payload = %+v", token)
	}
	if !token.ExpiresAt.Equal(clock.Now().Add(15 * time.Minute)) {
		t.Fatalf("token expiry = %v", token.ExpiresAt)
	}
}

func TestApprovalCannotExceedRequestedScopesOrChangeSubject(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read")

	if err := service.ApproveScopes(context.Background(), authorization.UserCode, "alice", []string{"admin"}); !errors.Is(err, netsec.ErrScopeNotRequested) {
		t.Fatalf("scope-expanding approval error = %v, want ErrScopeNotRequested", err)
	}
	approve(t, service, authorization.UserCode, "alice")

	if err := service.Approve(context.Background(), authorization.UserCode, "bob"); !errors.Is(err, netsec.ErrAlreadyDecided) {
		t.Fatalf("subject-changing approval error = %v, want ErrAlreadyDecided", err)
	}

	token, err := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "bob",
	})
	if token != nil {
		t.Fatalf("subject-mismatched exchange returned token %+v", token)
	}
	assertInvalidGrant(t, err)

	token, err = service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if err != nil {
		t.Fatalf("original subject exchange error = %v", err)
	}
	assertScopes(t, token.Scopes, []string{"read"})
}

func TestCrossClientPollingIsRejectedAndDoesNotConsumeCode(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read")
	approve(t, service, authorization.UserCode, "alice")

	token, err := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-b",
		Subject:    "alice",
	})
	if token != nil {
		t.Fatalf("cross-client exchange returned token %+v", token)
	}
	assertInvalidGrant(t, err)

	token, err = service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if err != nil {
		t.Fatalf("bound client exchange error = %v", err)
	}
	if token.ClientID != "client-a" {
		t.Fatalf("token client = %q, want client-a", token.ClientID)
	}
}

func TestConcurrentExchangeConsumesExactlyOnce(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read")
	approve(t, service, authorization.UserCode, "alice")

	const workers = 32
	type result struct {
		token *netsec.Token
		err   error
	}
	results := make(chan result, workers)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for index := 0; index < workers; index++ {
		go func() {
			defer waitGroup.Done()
			<-start
			token, err := service.Exchange(context.Background(), netsec.TokenRequest{
				DeviceCode: authorization.DeviceCode,
				ClientID:   "client-a",
				Subject:    "alice",
				Scopes:     []string{"read"},
			})
			results <- result{token: token, err: err}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)

	successes := 0
	failures := 0
	for result := range results {
		if result.err == nil {
			successes++
			if result.token == nil {
				t.Fatal("successful exchange returned a nil token")
			}
			continue
		}
		failures++
		assertInvalidGrant(t, result.err)
	}
	if successes != 1 {
		t.Fatalf("successful exchanges = %d, want 1", successes)
	}
	if failures != workers-1 {
		t.Fatalf("failed exchanges = %d, want %d", failures, workers-1)
	}

	token, err := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if token != nil {
		t.Fatalf("post-consumption exchange returned token %+v", token)
	}
	assertInvalidGrant(t, err)
}

func TestDeniedCodeCannotBeApprovedOrRedeemed(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read")

	if err := service.Deny(context.Background(), authorization.UserCode, "alice"); err != nil {
		t.Fatalf("Deny() error = %v", err)
	}

	err := service.Approve(context.Background(), authorization.UserCode, "alice")
	assertInvalidGrant(t, err)
	err = service.Approve(context.Background(), authorization.UserCode, "bob")
	assertInvalidGrant(t, err)

	token, err := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if token != nil {
		t.Fatalf("denied exchange returned token %+v", token)
	}
	assertInvalidGrant(t, err)

	token, err = service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if token != nil {
		t.Fatalf("retried denied exchange returned token %+v", token)
	}
	assertInvalidGrant(t, err)
}

func TestSlowDownDoesNotConsumeCodeOrFailFutureRedemption(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read")

	token, err := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if token != nil || !errors.Is(err, netsec.ErrAuthorizationPending) {
		t.Fatalf("first poll = (%+v, %v), want authorization_pending", token, err)
	}

	for attempt := 0; attempt < 5; attempt++ {
		clock.Advance(100 * time.Millisecond)
		token, err = service.Exchange(context.Background(), netsec.TokenRequest{
			DeviceCode: authorization.DeviceCode,
			ClientID:   "client-a",
			Subject:    "the-wrong-subject",
			Scopes:     []string{"admin"},
		})
		if token != nil || !errors.Is(err, netsec.ErrSlowDown) {
			t.Fatalf("fast poll %d = (%+v, %v), want slow_down", attempt, token, err)
		}
	}

	approve(t, service, authorization.UserCode, "alice")
	clock.Advance(1499 * time.Millisecond)
	token, err = service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if token != nil || !errors.Is(err, netsec.ErrSlowDown) {
		t.Fatalf("early post-approval poll = (%+v, %v), want slow_down", token, err)
	}

	clock.Advance(time.Millisecond)
	token, err = service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if err != nil {
		t.Fatalf("poll at interval boundary error = %v", err)
	}
	assertScopes(t, token.Scopes, []string{"read"})
}

func TestFrozenApprovalReturnsOnlyFrozenLowScopes(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read", "write", "admin")
	approveScopes(t, service, authorization.UserCode, "alice", "read")

	token, err := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	assertScopes(t, token.Scopes, []string{"read"})
}

func TestFormScopeEscalationIsRejected(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read")
	approveScopes(t, service, authorization.UserCode, "alice", "read")

	token, err := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
		Scope:      "regular plus a high-privilege-looking-token",
	})
	if token != nil {
		t.Fatalf("form scope escalation returned token %+v", token)
	}
	assertInvalidGrant(t, err)

	token, err = service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
		Scope:      "read",
	})
	if err != nil {
		t.Fatalf("valid form scope exchange error = %v", err)
	}
	assertScopes(t, token.Scopes, []string{"read"})
}

func TestExplicitEmptyApprovalDoesNotDefaultToRequestedScopes(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read", "write")

	if err := service.ApproveScopes(context.Background(), authorization.UserCode, "alice", nil); err != nil {
		t.Fatalf("ApproveScopes(nil) error = %v", err)
	}
	token, err := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	assertScopes(t, token.Scopes, []string{})
}

func TestExpiredAndUnknownCodesReturnTheSameError(t *testing.T) {
	clock := newFakeClock()
	service := netsec.New(netsec.Config{
		PollInterval:  2 * time.Second,
		DeviceCodeTTL: 30 * time.Second,
		Now:           clock.Now,
	})
	authorization := begin(t, service, "client-a", "read")
	approve(t, service, authorization.UserCode, "alice")
	clock.Advance(30 * time.Second)

	token, expiredError := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if token != nil {
		t.Fatalf("expired exchange returned token %+v", token)
	}

	token, unknownError := service.Exchange(context.Background(), netsec.TokenRequest{
		DeviceCode: "unknown-device-code",
		ClientID:   "client-a",
		Subject:    "alice",
	})
	if token != nil {
		t.Fatalf("unknown-code exchange returned token %+v", token)
	}

	assertInvalidGrant(t, expiredError)
	assertInvalidGrant(t, unknownError)
	if expiredError.Error() != unknownError.Error() {
		t.Fatalf("errors reveal code state: %q != %q", expiredError, unknownError)
	}
}

func TestUserCodeIsNormalizedForApproval(t *testing.T) {
	clock := newFakeClock()
	service := newTestService(clock)
	authorization := begin(t, service, "client-a", "read")

	if authorization.DeviceCode == "" || authorization.UserCode == "" {
		t.Fatalf("authorization codes = %+v", authorization)
	}
	if authorization.PollInterval != 2*time.Second {
		t.Fatalf("poll interval = %v", authorization.PollInterval)
	}
	if authorization.ExpiresIn != time.Minute {
		t.Fatalf("expires in = %v", authorization.ExpiresIn)
	}
	assertScopes(t, authorization.Scopes, []string{"read"})

	normalized := ""
	for _, character := range authorization.UserCode {
		switch character {
		case '-':
			normalized += " "
		default:
			normalized += string(character)
		}
	}
	normalized = "  " + normalized + "  "
	if err := service.Approve(context.Background(), normalized, "alice"); err != nil {
		t.Fatalf("Approve(normalized user code) error = %v", err)
	}

	token, err := service.Poll(context.Background(), authorization.DeviceCode, "client-a", "", nil)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	assertScopes(t, token.Scopes, []string{"read"})
	if token.Subject != "alice" {
		t.Fatalf("token subject = %q, want alice", token.Subject)
	}
}
