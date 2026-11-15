package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

const testSession = "sess-abc"

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time { return c.t }

func newTestManager(t *testing.T) (*Manager, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
	mgr := NewManager(WithClock(clock.now))
	err := mgr.OpenSession(SessionConfig{
		SessionID:     testSession,
		MaxReplyDelay: 5 * time.Millisecond,
		MaxClockSkew:  100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	return mgr, clock
}

func validResult(mgr *Manager, index uint64) RangingResult {
	return RangingResult{
		SessionID:    testSession,
		STSIndex:     index,
		STSQualityOK: true,
		Ra:           1000 * time.Nanosecond,
		Rb:           1200 * time.Nanosecond,
		Da:           200 * time.Nanosecond,
		Db:           300 * time.Nanosecond,
		ReplyDelay:   2 * time.Millisecond,
		Timestamp:    mgr.now(),
	}
}

func TestAdmitAcceptsAndComputesDSTWRDistance(t *testing.T) {
	mgr, _ := newTestManager(t)

	distance, err := mgr.Admit(validResult(mgr, 1))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}

	// ToF = (1000*1200 - 200*300) / (1000+1200+200+300)
	//     = 1_140_000 / 2700 = 422.222... ns.
	wantToFNanoseconds := 1140000.0 / 2700.0
	want := (wantToFNanoseconds / float64(time.Second)) * SpeedOfLightMetersPerSecond
	if diff := distance - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("distance = %.9f m, want %.9f m", distance, want)
	}

	last, ok := mgr.LastSTSIndex(testSession)
	if !ok || last != 1 {
		t.Fatalf("LastSTSIndex = (%d, %v), want (1, true)", last, ok)
	}

	// Strictly increasing indices keep being accepted.
	if _, err := mgr.Admit(validResult(mgr, 2)); err != nil {
		t.Fatalf("Admit index 2: %v", err)
	}
	last, _ = mgr.LastSTSIndex(testSession)
	if last != 2 {
		t.Fatalf("LastSTSIndex = %d, want 2", last)
	}
}

func TestAdmitRejectsSTSDuplicateAndRollback(t *testing.T) {
	mgr, _ := newTestManager(t)

	if _, err := mgr.Admit(validResult(mgr, 10)); err != nil {
		t.Fatalf("Admit index 10: %v", err)
	}

	duplicate := validResult(mgr, 10)
	if _, err := mgr.Admit(duplicate); !errors.Is(err, ErrSTSDuplicate) {
		t.Fatalf("duplicate index error = %v, want ErrSTSDuplicate", err)
	}

	rollback := validResult(mgr, 9)
	if _, err := mgr.Admit(rollback); !errors.Is(err, ErrSTSRollback) {
		t.Fatalf("rollback index error = %v, want ErrSTSRollback", err)
	}

	// Rejected results must not advance the session sequence.
	last, ok := mgr.LastSTSIndex(testSession)
	if !ok || last != 10 {
		t.Fatalf("LastSTSIndex = (%d, %v), want (10, true)", last, ok)
	}

	// A replayed old payload carrying a fresh index is still bound to the
	// current session and can be admitted, proving the index is the replay gate.
	if _, err := mgr.Admit(validResult(mgr, 11)); err != nil {
		t.Fatalf("Admit index 11 after rejected replays: %v", err)
	}
}

func TestAdmitRejectsReplyDelayOverLimit(t *testing.T) {
	mgr, _ := newTestManager(t)

	result := validResult(mgr, 1)
	result.ReplyDelay = 6 * time.Millisecond
	if _, err := mgr.Admit(result); !errors.Is(err, ErrReplyDelay) {
		t.Fatalf("over-limit delay error = %v, want ErrReplyDelay", err)
	}

	if _, ok := mgr.LastSTSIndex(testSession); ok {
		t.Fatal("rejected result must not record an STS index")
	}

	// The same index remains admissible once the delay is within the limit.
	result.ReplyDelay = 5 * time.Millisecond
	if _, err := mgr.Admit(result); err != nil {
		t.Fatalf("Admit within limit: %v", err)
	}
}

func TestAdmitRejectsIllegalTimestamps(t *testing.T) {
	mgr, clock := newTestManager(t)

	missing := validResult(mgr, 1)
	missing.Timestamp = time.Time{}
	if _, err := mgr.Admit(missing); !errors.Is(err, ErrBadTimestamp) {
		t.Fatalf("zero timestamp error = %v, want ErrBadTimestamp", err)
	}

	stale := validResult(mgr, 2)
	stale.Timestamp = clock.t.Add(-200 * time.Millisecond)
	if _, err := mgr.Admit(stale); !errors.Is(err, ErrBadTimestamp) {
		t.Fatalf("stale timestamp error = %v, want ErrBadTimestamp", err)
	}

	future := validResult(mgr, 3)
	future.Timestamp = clock.t.Add(200 * time.Millisecond)
	if _, err := mgr.Admit(future); !errors.Is(err, ErrBadTimestamp) {
		t.Fatalf("future timestamp error = %v, want ErrBadTimestamp", err)
	}

	// A timestamp inside the skew tolerance is accepted.
	nearby := validResult(mgr, 4)
	nearby.Timestamp = clock.t.Add(50 * time.Millisecond)
	if _, err := mgr.Admit(nearby); err != nil {
		t.Fatalf("Admit within skew: %v", err)
	}
}

func TestAdmitRejectsUnknownAndClosedSession(t *testing.T) {
	mgr, _ := newTestManager(t)

	unknown := validResult(mgr, 1)
	unknown.SessionID = "other-session"
	if _, err := mgr.Admit(unknown); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("unknown session error = %v, want ErrUnknownSession", err)
	}

	mgr.CloseSession(testSession)
	if _, err := mgr.Admit(validResult(mgr, 1)); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("closed session error = %v, want ErrUnknownSession", err)
	}
}

func TestAdmitRejectsExpiredSession(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
	mgr := NewManager(WithClock(clock.now))
	if err := mgr.OpenSession(SessionConfig{
		SessionID:     testSession,
		MaxReplyDelay: 5 * time.Millisecond,
		MaxClockSkew:  100 * time.Millisecond,
		Deadline:      clock.t.Add(time.Second),
	}); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	clock.t = clock.t.Add(2 * time.Second)
	if _, err := mgr.Admit(validResult(mgr, 1)); !errors.Is(err, ErrExpiredSession) {
		t.Fatalf("expired session error = %v, want ErrExpiredSession", err)
	}
}

func TestAdmitRejectsFailedSTSQuality(t *testing.T) {
	mgr, _ := newTestManager(t)

	result := validResult(mgr, 1)
	result.STSQualityOK = false
	if _, err := mgr.Admit(result); !errors.Is(err, ErrSTSQuality) {
		t.Fatalf("quality failure error = %v, want ErrSTSQuality", err)
	}
}

func TestAdmitRejectsNegativeRangeAndInvalidTiming(t *testing.T) {
	mgr, _ := newTestManager(t)

	// Reply product dominates the round-trip product, so ToF goes negative.
	negative := validResult(mgr, 1)
	negative.Ra = 300 * time.Nanosecond
	negative.Rb = 300 * time.Nanosecond
	negative.Da = 500 * time.Nanosecond
	negative.Db = 500 * time.Nanosecond
	if _, err := mgr.Admit(negative); !errors.Is(err, ErrNegativeRange) {
		t.Fatalf("negative range error = %v, want ErrNegativeRange", err)
	}

	if _, err := ComputeDistance(-time.Nanosecond, time.Nanosecond, 0, 0); !errors.Is(err, ErrInvalidTiming) {
		t.Fatalf("negative timing error = %v, want ErrInvalidTiming", err)
	}
	if _, err := ComputeDistance(0, 0, 0, 0); !errors.Is(err, ErrInvalidTiming) {
		t.Fatalf("zero timing error = %v, want ErrInvalidTiming", err)
	}

	// No state may advance after any rejection.
	if _, ok := mgr.LastSTSIndex(testSession); ok {
		t.Fatal("session must remain empty after rejected measurements")
	}
}

func TestAdmitConcurrentRejectsDuplicateIndices(t *testing.T) {
	mgr, _ := newTestManager(t)

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			// Every goroutine races the same STS index; only one may win.
			_, errs[i] = mgr.Admit(validResult(mgr, 1))
		}(i)
	}
	wg.Wait()

	accepted, rejected := 0, 0
	for _, err := range errs {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrSTSDuplicate) {
			rejected++
		} else {
			t.Fatalf("unexpected error %v", err)
		}
	}
	if accepted != 1 || rejected != goroutines-1 {
		t.Fatalf("accepted=%d rejected=%d, want 1 and %d", accepted, rejected, goroutines-1)
	}
}
