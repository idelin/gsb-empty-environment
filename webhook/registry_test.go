package webhook

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type fakeVerifier struct {
	result Result
	err    error
	seen   []string
}

func (f *fakeVerifier) Verify(_ context.Context, callbackURL string) (Result, error) {
	f.seen = append(f.seen, callbackURL)
	if f.err != nil {
		return Result{}, f.err
	}
	result := f.result
	if result.URL == "" {
		result.URL = callbackURL
	}
	return result, nil
}

func newTestRegistry(now time.Time) (*Registry, func(time.Time)) {
	r := NewRegistry(24 * time.Hour)
	current := now
	r.now = func() time.Time { return current }
	advance := func(t time.Time) { current = t }
	return r, advance
}

func sampleResult(now time.Time) Result {
	return Result{
		URL:           "https://subscriber.example/hook",
		Host:          "subscriber.example",
		EffectiveRate: Rate{RequestsPerSecond: 30},
		VerifiedAt:    now,
	}
}

func TestRegistryExpirationPausesDelivery(t *testing.T) {
	base := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	registry, advance := newTestRegistry(base)

	sub, err := registry.Add(sampleResult(base))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	wantExpiry := base.Add(24 * time.Hour)
	if !sub.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v", sub.ExpiresAt, wantExpiry)
	}

	if _, err := registry.AllowedToDeliver(sub.URL); err != nil {
		t.Fatalf("fresh subscription should deliver: %v", err)
	}

	advance(base.Add(23*time.Hour + 59*time.Minute))
	if _, err := registry.AllowedToDeliver(sub.URL); err != nil {
		t.Fatalf("subscription within TTL should deliver: %v", err)
	}

	advance(base.Add(24 * time.Hour))
	paused, err := registry.AllowedToDeliver(sub.URL)
	if !errors.Is(err, ErrSubscriptionExpired) {
		t.Fatalf("got %v, want ErrSubscriptionExpired", err)
	}
	if paused.URL != sub.URL {
		t.Fatalf("expired subscription should still be returned, got %+v", paused)
	}

	due := registry.DueForReverify()
	if len(due) != 1 || due[0].URL != sub.URL {
		t.Fatalf("DueForReverify = %+v, want the single expired sub", due)
	}

	// 重新验证成功后续期，投递恢复。
	reverifiedAt := base.Add(25 * time.Hour)
	advance(reverifiedAt)
	updated, err := registry.Update(Result{
		URL:           sub.URL,
		Host:          sub.Host,
		EffectiveRate: Rate{RequestsPerSecond: 12},
		VerifiedAt:    reverifiedAt,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if !updated.ExpiresAt.Equal(reverifiedAt.Add(24 * time.Hour)) {
		t.Fatalf("renewed ExpiresAt = %v", updated.ExpiresAt)
	}
	if _, err := registry.AllowedToDeliver(sub.URL); err != nil {
		t.Fatalf("renewed subscription should deliver: %v", err)
	}
	if len(registry.DueForReverify()) != 0 {
		t.Fatal("no subscription should be due after renewal")
	}
}

func TestRegistryAddDuplicateAndRemove(t *testing.T) {
	base := time.Now().UTC()
	registry, _ := newTestRegistry(base)
	result := sampleResult(base)

	if _, err := registry.Add(result); err != nil {
		t.Fatalf("first Add failed: %v", err)
	}
	if _, err := registry.Add(result); !errors.Is(err, ErrExistingSubscription) {
		t.Fatalf("got %v, want ErrExistingSubscription", err)
	}
	got, err := registry.Get(result.URL)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Rate != result.EffectiveRate {
		t.Fatalf("stored rate = %+v", got.Rate)
	}

	if err := registry.Remove(result.URL); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if _, err := registry.Get(result.URL); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("got %v, want ErrSubscriptionNotFound", err)
	}
	if err := registry.Remove(result.URL); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("removing missing URL: got %v", err)
	}
	if _, err := registry.AllowedToDeliver(result.URL); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("delivery to unknown URL: got %v", err)
	}
}

func TestManagerRegisterFailureNotStored(t *testing.T) {
	base := time.Now().UTC()
	registry, _ := newTestRegistry(base)
	verifier := &fakeVerifier{err: ErrMissingAllowedRate}
	manager := NewManager(verifier, registry)

	callback := "https://subscriber.example/hook"
	if _, err := manager.Register(context.Background(), callback); !errors.Is(err, ErrMissingAllowedRate) {
		t.Fatalf("got %v, want ErrMissingAllowedRate", err)
	}
	if _, err := registry.Get(callback); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("failed registration must not be stored, got %v", err)
	}

	verifier.err = nil
	verifier.result = Result{
		URL:           callback,
		Host:          "subscriber.example",
		EffectiveRate: Rate{RequestsPerSecond: 30},
		VerifiedAt:    base,
	}
	if _, err := manager.Register(context.Background(), callback); err != nil {
		t.Fatalf("successful registration failed: %v", err)
	}
	if _, err := manager.AllowedToDeliver(callback); err != nil {
		t.Fatalf("registered target should deliver: %v", err)
	}
}

func TestManagerReverifyLifecycle(t *testing.T) {
	base := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	registry, advance := newTestRegistry(base)
	verifier := &fakeVerifier{
		result: Result{
			URL:           "https://subscriber.example/hook",
			Host:          "subscriber.example",
			EffectiveRate: Rate{RequestsPerSecond: 30},
			VerifiedAt:    base,
		},
	}
	manager := NewManager(verifier, registry)

	callback := verifier.result.URL
	if _, err := manager.Register(context.Background(), callback); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if _, err := manager.Reverify(context.Background(), "https://unknown.example/hook"); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("reverify unknown target: got %v", err)
	}

	advance(base.Add(24 * time.Hour))
	verifier.err = ErrUnexpectedStatus
	failures := manager.ReverifyDue(context.Background())
	if len(failures) != 1 || !errors.Is(failures[callback], ErrUnexpectedStatus) {
		t.Fatalf("ReverifyDue failures = %v", failures)
	}
	if _, err := manager.AllowedToDeliver(callback); !errors.Is(err, ErrSubscriptionExpired) {
		t.Fatalf("delivery should stay paused after failed reverify: %v", err)
	}

	verifier.err = nil
	verifier.result.VerifiedAt = base.Add(25 * time.Hour)
	advance(base.Add(25 * time.Hour))
	failures = manager.ReverifyDue(context.Background())
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", failures)
	}
	if _, err := manager.AllowedToDeliver(callback); err != nil {
		t.Fatalf("delivery should resume after successful reverify: %v", err)
	}
}

func TestManagerEndToEndExpiration(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodOptions {
			t.Errorf("method = %q, want OPTIONS", r.Method)
		}
		calls.Add(1)
		w.Header().Set(HeaderAllowedOrigin, testOrigin)
		w.Header().Set(HeaderAllowedRate, "20")
		w.WriteHeader(http.StatusOK)
	})
	srv := newInProcessTLSServer(t, mux)

	verifier := newTestVerifier(t, srv, Rate{RequestsPerSecond: 100})
	// 用短 TTL 与真实时钟验证“未重新验证即暂停投递”，避免与验证器的真实时钟错位。
	registry := NewRegistry(100 * time.Millisecond)
	manager := NewManager(verifier, registry)

	target := callbackURL(srv, "/hook")
	sub, err := manager.Register(context.Background(), target)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if sub.Rate != (Rate{RequestsPerSecond: 20}) {
		t.Fatalf("effective rate = %+v, want 20 (min of 100 and 20)", sub.Rate)
	}

	time.Sleep(150 * time.Millisecond)
	if _, err := manager.AllowedToDeliver(target); !errors.Is(err, ErrSubscriptionExpired) {
		t.Fatalf("got %v, want ErrSubscriptionExpired", err)
	}

	renewed, err := manager.Reverify(context.Background(), target)
	if err != nil {
		t.Fatalf("Reverify failed: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("handshake calls = %d, want 2", calls.Load())
	}
	if _, err := manager.AllowedToDeliver(target); err != nil {
		t.Fatalf("delivery should resume: %v", err)
	}
	if !renewed.ExpiresAt.After(sub.ExpiresAt) {
		t.Fatalf("ExpiresAt not extended: %v -> %v", sub.ExpiresAt, renewed.ExpiresAt)
	}
}
