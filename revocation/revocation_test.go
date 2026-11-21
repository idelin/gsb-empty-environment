package revocation

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

type stubFetcher struct {
	calls   atomic.Int32
	resp    *Response
	err     error
	mutable func(call int) (*Response, error)
}

func (f *stubFetcher) Fetch(_ context.Context, issuer string) (*Response, error) {
	n := int(f.calls.Add(1))
	if f.mutable != nil {
		return f.mutable(n)
	}
	return f.resp, f.err
}

func testConfig(clock *fakeClock) Config {
	return Config{
		MaxCacheAge:    30 * time.Minute,
		MaxResponseAge: 2 * time.Hour,
		ClockSkew:      time.Minute,
		FailClosed:     true,
		Now:            clock.Now,
	}
}

func freshResponse(clock *fakeClock, issuer string) *Response {
	return &Response{
		Issuer:     issuer,
		ThisUpdate: clock.now,
		NextUpdate: clock.now.Add(time.Hour),
		Entries: map[string]Entry{
			"0A": {Serial: "0A"},
			"0B": {Serial: "0B", Revoked: true, RevokedAt: clock.now.Add(-time.Minute)},
		},
	}
}

func TestStatusDistinguishesNotIssuedFromRevoked(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	fetcher := &stubFetcher{resp: freshResponse(clock, "ca-1")}
	checker := NewChecker(fetcher, testConfig(clock))

	ctx := context.Background()
	cases := map[string]Status{
		"0A": StatusGood,
		"0B": StatusRevoked,
		"0C": StatusNotIssued,
	}
	for serial, want := range cases {
		got, err := checker.Check(ctx, "ca-1", serial)
		if err != nil {
			t.Fatalf("Check(%s) error: %v", serial, err)
		}
		if got != want {
			t.Errorf("Check(%s) = %v, want %v", serial, got, want)
		}
	}
}

func TestCacheHitAvoidsRefetch(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	fetcher := &stubFetcher{resp: freshResponse(clock, "ca-1")}
	checker := NewChecker(fetcher, testConfig(clock))

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := checker.Check(ctx, "ca-1", "0A"); err != nil {
			t.Fatalf("Check error: %v", err)
		}
		clock.Advance(5 * time.Minute)
	}
	if got := fetcher.calls.Load(); got != 1 {
		t.Errorf("fetch calls = %d, want 1 (cache should serve repeats)", got)
	}
}

func TestCacheExpiryByNextUpdateTriggersRefetch(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	fetcher := &stubFetcher{
		mutable: func(call int) (*Response, error) {
			return freshResponse(clock, "ca-1"), nil
		},
	}
	checker := NewChecker(fetcher, testConfig(clock))

	ctx := context.Background()
	if _, err := checker.Check(ctx, "ca-1", "0A"); err != nil {
		t.Fatalf("Check error: %v", err)
	}

	// Move past NextUpdate + ClockSkew: the cached response must expire.
	clock.Advance(time.Hour + 2*time.Minute)
	if _, err := checker.Check(ctx, "ca-1", "0A"); err != nil {
		t.Fatalf("Check after expiry error: %v", err)
	}
	if got := fetcher.calls.Load(); got != 2 {
		t.Errorf("fetch calls = %d, want 2 after cache expiry", got)
	}
}

func TestCacheExpiryByMaxCacheAgeTriggersRefetch(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	fetcher := &stubFetcher{
		mutable: func(call int) (*Response, error) {
			resp := freshResponse(clock, "ca-1")
			resp.NextUpdate = clock.now.Add(24 * time.Hour)
			return resp, nil
		},
	}
	checker := NewChecker(fetcher, testConfig(clock))

	ctx := context.Background()
	if _, err := checker.Check(ctx, "ca-1", "0A"); err != nil {
		t.Fatalf("Check error: %v", err)
	}

	// NextUpdate is far away, but MaxCacheAge (30m) is exceeded.
	clock.Advance(31 * time.Minute)
	if _, err := checker.Check(ctx, "ca-1", "0A"); err != nil {
		t.Fatalf("Check after MaxCacheAge error: %v", err)
	}
	if got := fetcher.calls.Load(); got != 2 {
		t.Errorf("fetch calls = %d, want 2 after MaxCacheAge exceeded", got)
	}
}

func TestStaleResponseRejected(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	stale := freshResponse(clock, "ca-1")
	stale.ThisUpdate = clock.now.Add(-3 * time.Hour) // beyond MaxResponseAge of 2h
	fetcher := &stubFetcher{resp: stale}
	checker := NewChecker(fetcher, testConfig(clock))

	_, err := checker.Check(context.Background(), "ca-1", "0A")
	if !errors.Is(err, ErrStaleResponse) {
		t.Errorf("err = %v, want ErrStaleResponse", err)
	}
}

func TestUnreachableFailsClosed(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	fetcher := &stubFetcher{err: errors.New("connection refused")}
	checker := NewChecker(fetcher, testConfig(clock))

	_, err := checker.Check(context.Background(), "ca-1", "0A")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable (fail closed)", err)
	}
}

func TestUnreachableAfterCacheExpiryFailsClosed(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	fetcher := &stubFetcher{
		mutable: func(call int) (*Response, error) {
			if call == 1 {
				return freshResponse(clock, "ca-1"), nil
			}
			return nil, errors.New("backend down")
		},
	}
	checker := NewChecker(fetcher, testConfig(clock))

	ctx := context.Background()
	if _, err := checker.Check(ctx, "ca-1", "0A"); err != nil {
		t.Fatalf("initial Check error: %v", err)
	}

	// Expire the cache, then the backend becomes unreachable: fail closed
	// rather than serving the expired entry.
	clock.Advance(time.Hour + 2*time.Minute)
	_, err := checker.Check(ctx, "ca-1", "0A")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable after cache expiry", err)
	}
}

func TestCacheIsPerIssuer(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	fetcher := &stubFetcher{
		mutable: func(call int) (*Response, error) {
			return freshResponse(clock, "ca-1"), nil
		},
	}
	checker := NewChecker(fetcher, testConfig(clock))

	ctx := context.Background()
	if _, err := checker.Check(ctx, "ca-1", "0A"); err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if _, err := checker.Check(ctx, "ca-2", "0A"); err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if got := fetcher.calls.Load(); got != 2 {
		t.Errorf("fetch calls = %d, want 2 (one per issuer)", got)
	}
}
