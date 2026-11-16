package netsec

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	t time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func testConfig(id string) SessionConfig {
	return SessionConfig{
		SessionID:     id,
		MaxReplyDelay: 2 * time.Millisecond,
		MaxClockSkew:  10 * time.Millisecond,
	}
}

// validResult builds a symmetric DS-TWR result whose true ToF is 100 ns.
// For symmetric timings ToF = (R-D)/2, so (1000-800)/2 = 100.
func validResult(sessionID string, sts uint64, ts time.Time) RangingResult {
	return RangingResult{
		SessionID: sessionID,
		STSIndex:  sts,
		STSOK:     true,
		Timestamp: ts,
		Ra:        1000 * time.Nanosecond,
		Rb:        1000 * time.Nanosecond,
		Da:        800 * time.Nanosecond,
		Db:        800 * time.Nanosecond,
	}
}

func newTestSession(t *testing.T, clock *fakeClock) *Session {
	t.Helper()
	session, err := newSessionAt(testConfig("sess-1"), clock.now)
	if err != nil {
		t.Fatalf("newSessionAt: %v", err)
	}
	return session
}

func TestAdmitHappyPathDSTWR(t *testing.T) {
	clock := newFakeClock()
	session := newTestSession(t, clock)

	verified, err := session.Admit(validResult("sess-1", 1, clock.now()))
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if verified.SessionID != "sess-1" || verified.STSIndex != 1 {
		t.Fatalf("unexpected verified result: %+v", verified)
	}
	if verified.ToF != 100*time.Nanosecond {
		t.Errorf("ToF = %s, want 100ns", verified.ToF)
	}
	wantDistance := 100.0 * 1e-9 * SpeedOfLight
	if math.Abs(verified.Distance-wantDistance) > 1e-12 {
		t.Errorf("Distance = %v, want %v", verified.Distance, wantDistance)
	}

	last, ok := session.LastSTS()
	if !ok || last != 1 {
		t.Errorf("LastSTS = (%d,%v), want (1,true)", last, ok)
	}
}

func TestSTSIndexStrictlyIncreasing(t *testing.T) {
	clock := newFakeClock()
	session := newTestSession(t, clock)

	if _, err := session.Admit(validResult("sess-1", 5, clock.now())); err != nil {
		t.Fatalf("first Admit: %v", err)
	}

	duplicate := validResult("sess-1", 5, clock.now())
	if _, err := session.Admit(duplicate); !errors.Is(err, ErrDuplicateSTS) {
		t.Errorf("duplicate index: err = %v, want ErrDuplicateSTS", err)
	}

	rollback := validResult("sess-1", 4, clock.now())
	if _, err := session.Admit(rollback); !errors.Is(err, ErrSTSRollback) {
		t.Errorf("rollback index: err = %v, want ErrSTSRollback", err)
	}

	// Rejected indices must not consume the sequence: 6 still follows 5.
	if _, err := session.Admit(validResult("sess-1", 6, clock.now())); err != nil {
		t.Errorf("increasing index after rejection: %v", err)
	}

	last, _ := session.LastSTS()
	if last != 6 {
		t.Errorf("LastSTS = %d, want 6", last)
	}
}

func TestSessionIDBinding(t *testing.T) {
	clock := newFakeClock()
	session := newTestSession(t, clock)

	stale := validResult("old-session", 1, clock.now())
	if _, err := session.Admit(stale); !errors.Is(err, ErrSessionMismatch) {
		t.Errorf("foreign session id: err = %v, want ErrSessionMismatch", err)
	}

	empty := validResult("", 1, clock.now())
	if _, err := session.Admit(empty); !errors.Is(err, ErrSessionMismatch) {
		t.Errorf("empty session id: err = %v, want ErrSessionMismatch", err)
	}
}

func TestServiceRotationRejectsReplay(t *testing.T) {
	clock := newFakeClock()
	svc := newServiceWithClock(clock.now)

	if _, err := svc.Admit(validResult("a", 1, clock.now())); !errors.Is(err, ErrNoActiveSession) {
		t.Errorf("admit with no session: err = %v, want ErrNoActiveSession", err)
	}

	if _, err := svc.StartSession(testConfig("a")); err != nil {
		t.Fatalf("StartSession a: %v", err)
	}
	if id := svc.CurrentSessionID(); id != "a" {
		t.Fatalf("CurrentSessionID = %q, want a", id)
	}
	if _, err := svc.Admit(validResult("a", 1, clock.now())); err != nil {
		t.Fatalf("admit on session a: %v", err)
	}

	// Rotating the session invalidates every result still bound to the old id.
	if _, err := svc.StartSession(testConfig("b")); err != nil {
		t.Fatalf("StartSession b: %v", err)
	}
	replayed := validResult("a", 1, clock.now())
	if _, err := svc.Admit(replayed); !errors.Is(err, ErrSessionMismatch) {
		t.Errorf("replayed old session result: err = %v, want ErrSessionMismatch", err)
	}
	if _, err := svc.Admit(validResult("b", 1, clock.now())); err != nil {
		t.Errorf("fresh session result rejected: %v", err)
	}
}

func TestReplyDelayExceeded(t *testing.T) {
	clock := newFakeClock()
	session := newTestSession(t, clock)

	tooSlowA := validResult("sess-1", 1, clock.now())
	tooSlowA.Da = 3 * time.Millisecond
	if _, err := session.Admit(tooSlowA); !errors.Is(err, ErrReplyDelayExceeded) {
		t.Errorf("Da over limit: err = %v, want ErrReplyDelayExceeded", err)
	}

	tooSlowB := validResult("sess-1", 1, clock.now())
	tooSlowB.Db = 3 * time.Millisecond
	if _, err := session.Admit(tooSlowB); !errors.Is(err, ErrReplyDelayExceeded) {
		t.Errorf("Db over limit: err = %v, want ErrReplyDelayExceeded", err)
	}

	// At the limit is allowed; the rule rejects only strictly greater delays.
	// Round trips stay above the reply time so the distance stays positive.
	atLimit := RangingResult{
		SessionID: "sess-1",
		STSIndex:  1,
		STSOK:     true,
		Timestamp: clock.now(),
		Ra:        2*time.Millisecond + 200*time.Nanosecond,
		Rb:        2*time.Millisecond + 200*time.Nanosecond,
		Da:        2 * time.Millisecond,
		Db:        2 * time.Millisecond,
	}
	if _, err := session.Admit(atLimit); err != nil {
		t.Errorf("delay at limit rejected: %v", err)
	}
}

func TestIllegalTimestamp(t *testing.T) {
	clock := newFakeClock()
	session := newTestSession(t, clock)

	zeroTS := validResult("sess-1", 1, time.Time{})
	if _, err := session.Admit(zeroTS); !errors.Is(err, ErrIllegalTimestamp) {
		t.Errorf("zero timestamp: err = %v, want ErrIllegalTimestamp", err)
	}

	future := validResult("sess-1", 1, clock.now().Add(time.Hour))
	if _, err := session.Admit(future); !errors.Is(err, ErrIllegalTimestamp) {
		t.Errorf("future timestamp: err = %v, want ErrIllegalTimestamp", err)
	}

	past := validResult("sess-1", 1, session.createdAt.Add(-time.Hour))
	if _, err := session.Admit(past); !errors.Is(err, ErrIllegalTimestamp) {
		t.Errorf("pre-session timestamp: err = %v, want ErrIllegalTimestamp", err)
	}

	// Within the configured skew is accepted.
	withinSkew := validResult("sess-1", 1, clock.now().Add(-5*time.Millisecond))
	if _, err := session.Admit(withinSkew); err != nil {
		t.Errorf("timestamp within skew rejected: %v", err)
	}
}

func TestSTSQualityFailure(t *testing.T) {
	clock := newFakeClock()
	session := newTestSession(t, clock)

	badQuality := validResult("sess-1", 1, clock.now())
	badQuality.STSOK = false
	if _, err := session.Admit(badQuality); !errors.Is(err, ErrSTSQuality) {
		t.Errorf("STS quality failure: err = %v, want ErrSTSQuality", err)
	}

	// A failed quality check must not consume the STS index.
	if _, err := session.Admit(validResult("sess-1", 1, clock.now())); err != nil {
		t.Errorf("index reused after quality failure should be admittable: %v", err)
	}
}

func TestNegativeDistanceRejected(t *testing.T) {
	clock := newFakeClock()
	session := newTestSession(t, clock)

	// Reply times larger than round trips make the DS-TWR numerator negative.
	negative := RangingResult{
		SessionID: "sess-1",
		STSIndex:  1,
		STSOK:     true,
		Timestamp: clock.now(),
		Ra:        500 * time.Nanosecond,
		Rb:        500 * time.Nanosecond,
		Da:        2000 * time.Nanosecond,
		Db:        2000 * time.Nanosecond,
	}
	_, distance, err := dsTWR(negative.Ra, negative.Rb, negative.Da, negative.Db)
	if err != nil {
		t.Fatalf("dsTWR setup error: %v", err)
	}
	if distance >= 0 {
		t.Fatalf("setup distance = %v, want negative", distance)
	}
	if _, err := session.Admit(negative); !errors.Is(err, ErrNegativeDistance) {
		t.Errorf("negative distance: err = %v, want ErrNegativeDistance", err)
	}
}

func TestInvalidTiming(t *testing.T) {
	clock := newFakeClock()
	session := newTestSession(t, clock)

	negativeTiming := validResult("sess-1", 1, clock.now())
	negativeTiming.Ra = -1 * time.Nanosecond
	if _, err := session.Admit(negativeTiming); !errors.Is(err, ErrInvalidTiming) {
		t.Errorf("negative timing: err = %v, want ErrInvalidTiming", err)
	}

	allZero := validResult("sess-1", 1, clock.now())
	allZero.Ra, allZero.Rb, allZero.Da, allZero.Db = 0, 0, 0, 0
	if _, err := session.Admit(allZero); !errors.Is(err, ErrInvalidTiming) {
		t.Errorf("all-zero timing: err = %v, want ErrInvalidTiming", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	if _, err := NewSession(SessionConfig{MaxReplyDelay: time.Millisecond}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("empty id: err = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewSession(SessionConfig{SessionID: "x", MaxReplyDelay: 0}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("zero reply limit: err = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewSession(SessionConfig{SessionID: "x", MaxReplyDelay: time.Millisecond, MaxClockSkew: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("negative skew: err = %v, want ErrInvalidConfig", err)
	}
}

func TestConcurrentAdmit(t *testing.T) {
	clock := newFakeClock()
	session := newTestSession(t, clock)

	const goroutines = 32
	var wg sync.WaitGroup
	var successes, rejected int32
	var mu sync.Mutex

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, err := session.Admit(validResult("sess-1", 7, clock.now()))
			mu.Lock()
			if err == nil {
				successes++
			} else if errors.Is(err, ErrDuplicateSTS) {
				rejected++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Errorf("successes = %d, want exactly 1", successes)
	}
	if rejected != goroutines-1 {
		t.Errorf("rejected = %d, want %d", rejected, goroutines-1)
	}
	last, ok := session.LastSTS()
	if !ok || last != 7 {
		t.Errorf("LastSTS = (%d,%v), want (7,true)", last, ok)
	}
}
