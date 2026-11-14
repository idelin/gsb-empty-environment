package netsec_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0467"
)

func TestConcurrentReservationsNeverExceedTotal(t *testing.T) {
	engine := mustEngine(t, netsec.Config{
		Accounts: map[string]netsec.AccountPolicy{
			"acct-1": {
				DailyLimit:   100,
				MonthlyLimit: 1_000,
			},
		},
	})

	at := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	const attempts = 100
	const amount = int64(10)

	start := make(chan struct{})
	var wg sync.WaitGroup
	var accepted atomic.Int64
	var rejected atomic.Int64
	var unexpected atomic.Value

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start

			_, err := engine.Reserve(context.Background(), netsec.ReserveRequest{
				IdempotencyKey: fmt.Sprintf("request-%d", id),
				AccountID:      "acct-1",
				Channel:        "web",
				Amount:         amount,
				At:             at,
			})
			switch {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, netsec.ErrQuotaExceeded):
				rejected.Add(1)
			default:
				unexpected.Store(err)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if err, _ := unexpected.Load().(error); err != nil {
		t.Fatalf("unexpected reservation error: %v", err)
	}
	if got, want := accepted.Load(), int64(10); got != want {
		t.Fatalf("accepted reservations = %d, want %d", got, want)
	}
	if got, want := rejected.Load(), int64(90); got != want {
		t.Fatalf("rejected reservations = %d, want %d", got, want)
	}

	total := mustUsage(t, engine, "acct-1", "", at)
	if total.DailyUsed != 100 || total.MonthlyUsed != 100 {
		t.Fatalf(
			"usage after concurrent requests = daily:%d monthly:%d, want 100/100",
			total.DailyUsed,
			total.MonthlyUsed,
		)
	}
}

func TestLegalReservationsCanUseExactRemainingQuota(t *testing.T) {
	engine := mustEngine(t, netsec.Config{
		Accounts: map[string]netsec.AccountPolicy{
			"acct-1": {DailyLimit: 100, MonthlyLimit: 100},
		},
	})

	at := time.Date(2026, time.January, 20, 12, 0, 0, 0, time.UTC)
	mustReserve(t, engine, "legal-1", "web", 40, at)
	mustReserve(t, engine, "legal-2", "app", 60, at)
	assertExceeded(t, engine, "legal-over", "web", 1, at, netsec.ScopeAccount, netsec.WindowDaily)

	usage := mustUsage(t, engine, "acct-1", "", at)
	if usage.DailyUsed != 100 || usage.MonthlyUsed != 100 {
		t.Fatalf("usage = daily:%d monthly:%d, want exact limits 100/100", usage.DailyUsed, usage.MonthlyUsed)
	}
}

func TestCrossChannelRequestsShareAccountTotal(t *testing.T) {
	engine := mustEngine(t, netsec.Config{
		Accounts: map[string]netsec.AccountPolicy{
			"acct-1": {
				DailyLimit:   100,
				MonthlyLimit: 1_000,
				ChannelLimits: map[string]netsec.Limits{
					"web": {Daily: 60, Monthly: 500},
					"app": {Daily: 75, Monthly: 500},
				},
			},
		},
	})

	at := time.Date(2026, time.February, 2, 8, 0, 0, 0, time.UTC)
	mustReserve(t, engine, "web-1", "web", 40, at)
	mustReserve(t, engine, "app-1", "app", 30, at)

	assertExceeded(t, engine, "web-2", "web", 30, at, netsec.ScopeChannel, netsec.WindowDaily)
	mustReserve(t, engine, "app-2", "app", 30, at)
	assertExceeded(t, engine, "app-3", "app", 1, at, netsec.ScopeAccount, netsec.WindowDaily)

	if err := engine.Release(context.Background(), "web-1"); err != nil {
		t.Fatalf("release web reservation: %v", err)
	}
	mustReserve(t, engine, "web-2", "web", 40, at)

	total := mustUsage(t, engine, "acct-1", "", at)
	if total.DailyUsed != 100 {
		t.Fatalf("cross-channel total = %d, want 100", total.DailyUsed)
	}
	web := mustUsage(t, engine, "acct-1", "web", at)
	if web.DailyUsed != 40 {
		t.Fatalf("web usage after replacement reservation = %d, want 40", web.DailyUsed)
	}
	app := mustUsage(t, engine, "acct-1", "app", at)
	if app.DailyUsed != 60 {
		t.Fatalf("app usage = %d, want 60", app.DailyUsed)
	}
}

func TestFailedReservationAndReleaseKeepQuotaConsistent(t *testing.T) {
	engine := mustEngine(t, netsec.Config{
		Accounts: map[string]netsec.AccountPolicy{
			"acct-1": {DailyLimit: 100, MonthlyLimit: 100},
		},
	})

	at := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	reservation := mustReserve(t, engine, "first", "web", 100, at)
	assertExceeded(t, engine, "too-late", "web", 1, at, netsec.ScopeAccount, netsec.WindowDaily)

	usage := mustUsage(t, engine, "acct-1", "", at)
	if usage.DailyUsed != 100 {
		t.Fatalf("failed reservation changed daily usage to %d", usage.DailyUsed)
	}

	if err := engine.ReleaseReservation(context.Background(), reservation); err != nil {
		t.Fatalf("release reservation: %v", err)
	}
	if err := engine.Release(context.Background(), "first"); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
	if _, err := engine.Reserve(context.Background(), netsec.ReserveRequest{
		IdempotencyKey: "first",
		AccountID:      "acct-1",
		Channel:        "web",
		Amount:         100,
		At:             at,
	}); !errors.Is(err, netsec.ErrReservationReleased) {
		t.Fatalf("reusing released key error = %v, want ErrReservationReleased", err)
	}

	usage = mustUsage(t, engine, "acct-1", "", at)
	if usage.DailyUsed != 0 || usage.MonthlyUsed != 0 {
		t.Fatalf("usage after release = daily:%d monthly:%d, want 0/0", usage.DailyUsed, usage.MonthlyUsed)
	}

	mustReserve(t, engine, "second", "web", 100, at)
}

func TestIdempotentConcurrentRetriesDeductOnce(t *testing.T) {
	engine := mustEngine(t, netsec.Config{
		Accounts: map[string]netsec.AccountPolicy{
			"acct-1": {DailyLimit: 100, MonthlyLimit: 100},
		},
	})

	at := time.Date(2026, time.April, 4, 10, 0, 0, 0, time.UTC)
	const retries = 32

	start := make(chan struct{})
	var wg sync.WaitGroup
	var errCount atomic.Int64
	for i := 0; i < retries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := engine.Reserve(context.Background(), netsec.ReserveRequest{
				IdempotencyKey: "same-business-request",
				AccountID:      "acct-1",
				Channel:        "web",
				Amount:         40,
				At:             at,
			})
			if err != nil {
				errCount.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	if got := errCount.Load(); got != 0 {
		t.Fatalf("%d idempotent retries failed", got)
	}
	usage := mustUsage(t, engine, "acct-1", "", at)
	if usage.DailyUsed != 40 {
		t.Fatalf("daily usage = %d after idempotent retries, want 40", usage.DailyUsed)
	}

	if _, err := engine.Reserve(context.Background(), netsec.ReserveRequest{
		IdempotencyKey: "same-business-request",
		AccountID:      "acct-1",
		Channel:        "web",
		Amount:         41,
		At:             at,
	}); !errors.Is(err, netsec.ErrIdempotencyConflict) {
		t.Fatalf("conflicting retry error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestDailyAndMonthlyWindowsSwitchAndLateRelease(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	engine := mustEngine(t, netsec.Config{
		Location: location,
		Accounts: map[string]netsec.AccountPolicy{
			"acct-1": {DailyLimit: 100, MonthlyLimit: 200},
		},
	})

	jan30 := time.Date(2026, time.January, 30, 10, 0, 0, 0, location)
	jan31 := time.Date(2026, time.January, 31, 9, 0, 0, 0, location)
	feb1 := time.Date(2026, time.February, 1, 0, 0, 0, 0, location)

	old := mustReserve(t, engine, "jan-30", "web", 80, jan30)
	if old.DayStart.Day() != 30 || old.MonthStart.Month() != time.January {
		t.Fatalf("unexpected reservation windows: day=%v month=%v", old.DayStart, old.MonthStart)
	}
	assertExceeded(t, engine, "jan-30-over", "web", 30, jan30, netsec.ScopeAccount, netsec.WindowDaily)

	mustReserve(t, engine, "jan-31-a", "web", 80, jan31)
	assertExceeded(t, engine, "jan-31-over", "web", 30, jan31, netsec.ScopeAccount, netsec.WindowDaily)

	if err := engine.Release(context.Background(), "jan-30"); err != nil {
		t.Fatalf("late release: %v", err)
	}
	mustReserve(t, engine, "jan-31-b", "web", 20, jan31)

	jan31Usage := mustUsage(t, engine, "acct-1", "", jan31)
	if jan31Usage.DailyUsed != 100 || jan31Usage.MonthlyUsed != 100 {
		t.Fatalf(
			"Jan 31 usage = daily:%d monthly:%d, want 100/100",
			jan31Usage.DailyUsed,
			jan31Usage.MonthlyUsed,
		)
	}

	mustReserve(t, engine, "feb-1", "app", 100, feb1)
	assertExceeded(t, engine, "feb-1-over", "app", 1, feb1, netsec.ScopeAccount, netsec.WindowDaily)

	febUsage := mustUsage(t, engine, "acct-1", "", feb1)
	if febUsage.DailyUsed != 100 || febUsage.MonthlyUsed != 100 {
		t.Fatalf(
			"Feb 1 usage = daily:%d monthly:%d, want 100/100",
			febUsage.DailyUsed,
			febUsage.MonthlyUsed,
		)
	}
	if febUsage.DayStart.Day() != 1 || febUsage.MonthStart.Month() != time.February {
		t.Fatalf("unexpected February windows: day=%v month=%v", febUsage.DayStart, febUsage.MonthStart)
	}
}

func TestMonthlyLimitPersistsAcrossDays(t *testing.T) {
	engine := mustEngine(t, netsec.Config{
		Accounts: map[string]netsec.AccountPolicy{
			"acct-1": {DailyLimit: 100, MonthlyLimit: 120},
		},
	})

	dayOne := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	dayTwo := time.Date(2026, time.May, 11, 12, 0, 0, 0, time.UTC)

	mustReserve(t, engine, "day-one", "web", 70, dayOne)
	assertExceeded(t, engine, "day-two-over", "web", 60, dayTwo, netsec.ScopeAccount, netsec.WindowMonthly)
	mustReserve(t, engine, "day-two", "web", 50, dayTwo)
	assertExceeded(t, engine, "day-two-total", "web", 1, dayTwo, netsec.ScopeAccount, netsec.WindowMonthly)
}

func mustEngine(t *testing.T, config netsec.Config) *netsec.Engine {
	t.Helper()
	engine, err := netsec.New(config)
	if err != nil {
		t.Fatalf("create engine: %v", err)
	}
	return engine
}

func mustReserve(
	t *testing.T,
	engine *netsec.Engine,
	key string,
	channel string,
	amount int64,
	at time.Time,
) netsec.Reservation {
	t.Helper()
	reservation, err := engine.Reserve(context.Background(), netsec.ReserveRequest{
		IdempotencyKey: key,
		AccountID:      "acct-1",
		Channel:        channel,
		Amount:         amount,
		At:             at,
	})
	if err != nil {
		t.Fatalf("reserve %s: %v", key, err)
	}
	return reservation
}

func assertExceeded(
	t *testing.T,
	engine *netsec.Engine,
	key string,
	channel string,
	amount int64,
	at time.Time,
	scope netsec.Scope,
	window netsec.WindowKind,
) {
	t.Helper()
	_, err := engine.Reserve(context.Background(), netsec.ReserveRequest{
		IdempotencyKey: key,
		AccountID:      "acct-1",
		Channel:        channel,
		Amount:         amount,
		At:             at,
	})
	if !errors.Is(err, netsec.ErrQuotaExceeded) {
		t.Fatalf("reserve %s error = %v, want ErrQuotaExceeded", key, err)
	}
	var exceeded *netsec.QuotaExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("reserve %s error type = %T, want *QuotaExceededError", key, err)
	}
	if exceeded.Scope != scope || exceeded.Window != window {
		t.Fatalf(
			"reserve %s rejection = %s/%s, want %s/%s",
			key,
			exceeded.Scope,
			exceeded.Window,
			scope,
			window,
		)
	}
}

func mustUsage(
	t *testing.T,
	engine *netsec.Engine,
	accountID string,
	channel string,
	at time.Time,
) netsec.Usage {
	t.Helper()
	usage, err := engine.Usage(context.Background(), accountID, channel, at)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	return usage
}
