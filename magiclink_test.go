package netsec

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func testService(t *testing.T, ttl time.Duration) (*Service, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	svc, err := NewService(Config{
		Secret:         []byte("0123456789abcdef0123456789abcdef"),
		TTL:            ttl,
		Now:            clock.Now,
		AllowedOrigins: []string{"https://auth.example.com"},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, clock
}

func trustedAction() Action {
	return Action{
		Method:       "POST",
		Origin:       "https://auth.example.com",
		SecFetchSite: "same-origin",
	}
}

func TestProbeNeverConsumesAndConsumeIsOneTime(t *testing.T) {
	ctx := context.Background()
	svc, _ := testService(t, 5*time.Minute)

	token, err := svc.Issue("user-123", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for i := 0; i < 3; i++ {
		claims, err := svc.Probe(ctx, token)
		if err != nil {
			t.Fatalf("Probe %d: %v", i, err)
		}
		if claims.UserID != "user-123" || claims.Intent != "login" {
			t.Fatalf("unexpected claims: %+v", claims)
		}
	}

	claims, err := svc.Consume(ctx, token, Binding{}, trustedAction())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if claims.UserID != "user-123" || claims.Intent != "login" {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	if _, err := svc.Consume(ctx, token, Binding{}, trustedAction()); !errors.Is(err, ErrTokenConsumed) {
		t.Fatalf("second Consume error = %v, want ErrTokenConsumed", err)
	}
	if _, err := svc.Probe(ctx, token); !errors.Is(err, ErrTokenConsumed) {
		t.Fatalf("Probe after consume error = %v, want ErrTokenConsumed", err)
	}
}

func TestConsumeRequiresUntamperedTrustedAction(t *testing.T) {
	ctx := context.Background()
	svc, _ := testService(t, 5*time.Minute)
	token, err := svc.Issue("user-123", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	evilActions := []Action{
		{Method: "GET", Origin: "https://auth.example.com", SecFetchSite: "same-origin"},
		{Method: "POST", Origin: "https://evil.example", SecFetchSite: "same-origin"},
		{Method: "POST", SecFetchSite: "same-origin"},
		{Method: "POST", Origin: "https://auth.example.com", SecFetchSite: "cross-site"},
		{Method: "POST", Origin: "null", SecFetchSite: "same-origin"},
	}
	for _, action := range evilActions {
		if _, err := svc.Consume(ctx, token, Binding{}, action); !errors.Is(err, ErrUntrustedAction) {
			t.Fatalf("Consume(%+v) error = %v, want ErrUntrustedAction", action, err)
		}
	}

	if _, err := svc.Consume(ctx, token, Binding{}, trustedAction()); err != nil {
		t.Fatalf("honest Consume after rejected actions: %v", err)
	}
}

func TestExpiredTokenIsRejectedWithoutConsumption(t *testing.T) {
	ctx := context.Background()
	svc, clock := testService(t, 2*time.Minute)
	token, err := svc.Issue("user-123", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	clock.Advance(2 * time.Minute)
	if _, err := svc.Probe(ctx, token); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("Probe error = %v, want ErrTokenExpired", err)
	}
	if _, err := svc.Consume(ctx, token, Binding{}, trustedAction()); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("Consume error = %v, want ErrTokenExpired", err)
	}
}

func TestBindingMismatchDoesNotSpendToken(t *testing.T) {
	ctx := context.Background()
	svc, _ := testService(t, 5*time.Minute)
	issued, err := svc.IssueFor(Binding{
		UserID:    "alice",
		Intent:    "login",
		SessionID: "pending-session-1",
	})
	if err != nil {
		t.Fatalf("IssueFor: %v", err)
	}

	wrongBindings := []Binding{
		{UserID: "bob"},
		{Intent: "password-reset"},
		{SessionID: "pending-session-2"},
	}
	for _, binding := range wrongBindings {
		if _, err := svc.Consume(ctx, issued.Value, binding, trustedAction()); !errors.Is(err, ErrBindingMismatch) {
			t.Fatalf("Consume(%+v) error = %v, want ErrBindingMismatch", binding, err)
		}
	}

	if _, err := svc.Consume(ctx, issued.Value, issued.Claims.Binding(), trustedAction()); err != nil {
		t.Fatalf("Consume with correct binding: %v", err)
	}
}

func TestTamperedTokenIsRejected(t *testing.T) {
	ctx := context.Background()
	svc, _ := testService(t, 5*time.Minute)
	token, err := svc.Issue("user-123", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	replaced := "ml1.A" + token[len("ml1.")+1:]
	for _, bad := range []string{"", "not-a-token", token + "x", replaced} {
		if _, err := svc.Probe(ctx, bad); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Probe(%q) error = %v, want ErrInvalidToken", bad, err)
		}
	}
}

func TestConsumptionIsSharedAcrossServiceInstances(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock()
	store := newMemoryStore(clock.Now)
	cfg := Config{
		Secret:         []byte("0123456789abcdef0123456789abcdef"),
		TTL:            time.Minute,
		Now:            clock.Now,
		Store:          store,
		AllowedOrigins: []string{"https://auth.example.com"},
	}
	first, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService first: %v", err)
	}
	second, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService second: %v", err)
	}

	token, err := first.Issue("user-123", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := first.Consume(ctx, token, Binding{}, trustedAction()); err != nil {
		t.Fatalf("Consume first: %v", err)
	}
	if _, err := second.Probe(ctx, token); !errors.Is(err, ErrTokenConsumed) {
		t.Fatalf("Probe second error = %v, want ErrTokenConsumed", err)
	}
}

func TestConcurrentConsumeHasSingleWinner(t *testing.T) {
	ctx := context.Background()
	svc, _ := testService(t, 5*time.Minute)
	token, err := svc.Issue("user-123", "login")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	const callers = 32
	var successes atomic.Int32
	var unexpected atomic.Int32
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.Consume(ctx, token, Binding{}, trustedAction())
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrTokenConsumed):
			default:
				unexpected.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful consumptions = %d, want 1", got)
	}
	if got := unexpected.Load(); got != 0 {
		t.Fatalf("unexpected errors = %d, want 0", got)
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := NewService(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty secret error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewService(Config{
		Secret:         []byte("secret"),
		TTL:            -time.Second,
		AllowedOrigins: []string{"https://auth.example.com"},
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative TTL error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewService(Config{
		Secret:         []byte("secret"),
		AllowedOrigins: []string{"https://auth.example.com/path"},
	}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("origin with path error = %v, want ErrInvalidConfig", err)
	}

	svc, err := NewService(Config{Secret: []byte("secret")})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.Issue("", "login"); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("empty user error = %v, want ErrInvalidBinding", err)
	}
}
