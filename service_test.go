package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func newManualClock() *manualClock {
	return &manualClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestSetup() (*Service, *Gateway, *Gateway, *manualClock) {
	clock := newManualClock()
	svc := NewService(NewMemoryStore(), Config{}, WithClock(clock.Now))
	return svc, svc.Gateway("A"), svc.Gateway("B"), clock
}

func TestCrossGatewayReuse(t *testing.T) {
	_, gwA, gwB, _ := newTestSetup()

	res, err := gwA.Send("13800000000", "login")
	if err != nil {
		t.Fatalf("send via A: %v", err)
	}
	if err := gwA.Verify("13800000000", "login", res.Code); err != nil {
		t.Fatalf("first verify via A should succeed: %v", err)
	}
	if err := gwB.Verify("13800000000", "login", res.Code); !errors.Is(err, ErrCodeConsumed) {
		t.Fatalf("replay via B must fail with ErrCodeConsumed, got %v", err)
	}
	if err := gwA.Verify("13800000000", "login", res.Code); !errors.Is(err, ErrCodeConsumed) {
		t.Fatalf("replay via A must fail with ErrCodeConsumed, got %v", err)
	}
}

func TestConcurrentConsumeOnlyOneSuccess(t *testing.T) {
	_, gwA, gwB, _ := newTestSetup()

	res, err := gwA.Send("13800000001", "pay")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	const workers = 64
	gateways := []*Gateway{gwA, gwB}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	consumedErrs := 0
	otherErrs := map[string]int{}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := gateways[i%2].Verify("13800000001", "pay", res.Code)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrCodeConsumed):
				consumedErrs++
			default:
				otherErrs[err.Error()]++
			}
		}(i)
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("exactly one concurrent verify may succeed, got %d", successes)
	}
	if consumedErrs != workers-1 {
		t.Fatalf("all other verifies must see ErrCodeConsumed, got %d (others: %v)", consumedErrs, otherErrs)
	}
}

func TestRebindInvalidatesOldPhoneCodes(t *testing.T) {
	_, gwA, gwB, clock := newTestSetup()

	res, err := gwA.Send("13800000002", "bind")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := gwB.Rebind("acct-1", "13800000002", "13900000002"); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if err := gwA.Verify("13800000002", "bind", res.Code); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("code of old phone must be invalidated after rebind, got %v", err)
	}

	// Rebind is rate-limited per account (default 3 per 24h window).
	if err := gwA.Rebind("acct-1", "13900000002", "13900000003"); err != nil {
		t.Fatalf("second rebind: %v", err)
	}
	if err := gwA.Rebind("acct-1", "13900000003", "13900000004"); err != nil {
		t.Fatalf("third rebind: %v", err)
	}
	if err := gwA.Rebind("acct-1", "13900000004", "13900000005"); !errors.Is(err, ErrRebindLimitExceeded) {
		t.Fatalf("fourth rebind in window must be limited, got %v", err)
	}

	clock.Advance(24*time.Hour + time.Second)
	if err := gwA.Rebind("acct-1", "13900000004", "13900000005"); err != nil {
		t.Fatalf("rebind after window reset should succeed: %v", err)
	}
}

func TestLegitVerifyAndAttemptLockout(t *testing.T) {
	_, gwA, gwB, clock := newTestSetup()

	res, err := gwA.Send("13800000003", "login")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := gwB.Verify("13800000003", "login", "000000"); !errors.Is(err, ErrCodeMismatch) {
		t.Fatalf("wrong code must mismatch, got %v", err)
	}
	if err := gwB.Verify("13800000003", "login", res.Code); err != nil {
		t.Fatalf("legit verify via another gateway should succeed: %v", err)
	}
	if err := gwB.Verify("13800000003", "login", res.Code); !errors.Is(err, ErrCodeConsumed) {
		t.Fatalf("second verify must fail, got %v", err)
	}

	// Wrong-code attempts are bounded; the code is invalidated at the limit.
	clock.Advance(61 * time.Second)
	res2, err := gwA.Send("13800000003", "login")
	if err != nil {
		t.Fatalf("resend: %v", err)
	}
	for i := 0; i < 5; i++ {
		err := gwA.Verify("13800000003", "login", fmt.Sprintf("9%05d", i))
		if i < 4 && !errors.Is(err, ErrCodeMismatch) {
			t.Fatalf("attempt %d: want mismatch, got %v", i+1, err)
		}
		if i == 4 && !errors.Is(err, ErrTooManyAttempts) {
			t.Fatalf("attempt %d: want lockout, got %v", i+1, err)
		}
	}
	if err := gwA.Verify("13800000003", "login", res2.Code); !errors.Is(err, ErrCodeNotFound) {
		t.Fatalf("locked code must be invalidated, got %v", err)
	}
}

func TestCodeExpiryAcrossTimeSteps(t *testing.T) {
	_, gwA, _, clock := newTestSetup()

	res, err := gwA.Send("13800000004", "login")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	// Default: 300s steps with 1 step of skew -> valid up to ~600s.
	clock.Advance(601 * time.Second)
	if err := gwA.Verify("13800000004", "login", res.Code); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("code past its time step must expire, got %v", err)
	}
}

func TestSendRateLimit(t *testing.T) {
	_, gwA, gwB, clock := newTestSetup()

	if _, err := gwA.Send("13800000005", "login"); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if _, err := gwB.Send("13800000005", "login"); !errors.Is(err, ErrSendTooFrequent) {
		t.Fatalf("immediate resend via another gateway must be rejected, got %v", err)
	}

	// Default limit: 5 sends per phone+purpose per hour; one already used.
	for i := 0; i < 4; i++ {
		clock.Advance(61 * time.Second)
		if _, err := gwA.Send("13800000005", "login"); err != nil {
			t.Fatalf("send %d within limit: %v", i+2, err)
		}
	}
	clock.Advance(61 * time.Second)
	if _, err := gwB.Send("13800000005", "login"); !errors.Is(err, ErrSendLimitExceeded) {
		t.Fatalf("sixth send in window must be limited, got %v", err)
	}

	clock.Advance(time.Hour)
	if _, err := gwA.Send("13800000005", "login"); err != nil {
		t.Fatalf("send after window reset should succeed: %v", err)
	}
}
