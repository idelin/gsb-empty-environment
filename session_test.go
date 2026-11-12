package netsec

import (
	"errors"
	"math"
	"testing"
	"time"
)

const testMaxReply = 5 * time.Millisecond

func newTestService(t *testing.T, at time.Time) (*Service, *Session) {
	t.Helper()
	svc := newServiceWithClock(func() time.Time { return at })
	sess := &Session{
		ID:            "sess-1",
		MaxReplyDelay: testMaxReply,
		StartTime:     at.Add(-time.Minute),
		Deadline:      at.Add(time.Minute),
	}
	if err := svc.Register(sess); err != nil {
		t.Fatalf("register: %v", err)
	}
	return svc, sess
}

func validResult(at time.Time, idx uint64) RangingResult {
	return RangingResult{
		SessionID:    "sess-1",
		STSIndex:     idx,
		STSQualityOK: true,
		Timestamp:    at.Add(-time.Millisecond),
		ReplyDelay:   time.Millisecond,
		Ra:           10 * time.Microsecond,
		Rb:           12 * time.Microsecond,
		Da:           2 * time.Microsecond,
		Db:           3 * time.Microsecond,
	}
}

func TestAdmitAcceptsStrictlyIncreasingIndices(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)

	for _, idx := range []uint64{1, 2, 100, 1 << 40} {
		r := validResult(at, idx)
		dec, err := svc.Admit(r)
		if err != nil {
			t.Fatalf("index %d rejected: %v", idx, err)
		}
		if dec.STSIndex != idx || dec.SessionID != "sess-1" {
			t.Fatalf("decision mismatch: %+v", dec)
		}
	}
}

func TestAdmitRejectsReplayedAndRolledBackIndex(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)

	if _, err := svc.Admit(validResult(at, 10)); err != nil {
		t.Fatalf("initial admit: %v", err)
	}

	// Replay: identical STS index.
	if _, err := svc.Admit(validResult(at, 10)); !errors.Is(err, ErrStaleIndex) {
		t.Fatalf("duplicate index: want ErrStaleIndex, got %v", err)
	}
	// Rollback: smaller STS index.
	if _, err := svc.Admit(validResult(at, 9)); !errors.Is(err, ErrStaleIndex) {
		t.Fatalf("rolled-back index: want ErrStaleIndex, got %v", err)
	}
	// A failed admission must not advance the high-water mark.
	if _, err := svc.Admit(validResult(at, 11)); err != nil {
		t.Fatalf("next valid index rejected after failed admits: %v", err)
	}
}

func TestAdmitRejectsUnknownOrMismatchedSession(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)

	r := validResult(at, 1)
	r.SessionID = "sess-other"
	if _, err := svc.Admit(r); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("foreign session: want ErrSessionUnknown, got %v", err)
	}

	if err := svc.Close("sess-1"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := svc.Admit(validResult(at, 1)); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session: want ErrSessionClosed, got %v", err)
	}
}

func TestAdmitRejectsFailedSTSQuality(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)

	r := validResult(at, 1)
	r.STSQualityOK = false
	if _, err := svc.Admit(r); !errors.Is(err, ErrQualityFailed) {
		t.Fatalf("bad quality: want ErrQualityFailed, got %v", err)
	}
	// Quality failure must not consume the STS index.
	r.STSQualityOK = true
	if _, err := svc.Admit(r); err != nil {
		t.Fatalf("same index after quality failure should be accepted: %v", err)
	}
}

func TestAdmitRejectsReplyDelayOverLimit(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)

	cases := map[string]func(*RangingResult){
		"observed reply delay": func(r *RangingResult) { r.ReplyDelay = testMaxReply + time.Nanosecond },
		"stretched Da":         func(r *RangingResult) { r.Da = testMaxReply + time.Nanosecond },
		"stretched Db":         func(r *RangingResult) { r.Db = testMaxReply + time.Nanosecond },
	}
	idx := uint64(1)
	for name, mutate := range cases {
		r := validResult(at, idx)
		mutate(&r)
		if _, err := svc.Admit(r); !errors.Is(err, ErrReplyDelay) {
			t.Fatalf("%s: want ErrReplyDelay, got %v", name, err)
		}
		idx++
	}
}

func TestAdmitRejectsIllegalTimestamps(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	svc, sess := newTestService(t, at)

	cases := map[string]time.Time{
		"zero timestamp":         time.Time{},
		"before session start":   sess.StartTime.Add(-time.Nanosecond),
		"after session deadline": sess.Deadline.Add(time.Nanosecond),
		"future beyond skew":     at.Add(futureSkew + time.Nanosecond),
	}
	idx := uint64(1)
	for name, ts := range cases {
		r := validResult(at, idx)
		r.Timestamp = ts
		if _, err := svc.Admit(r); !errors.Is(err, ErrInvalidTimestamp) {
			t.Fatalf("%s: want ErrInvalidTimestamp, got %v", name, err)
		}
		idx++
	}

	// A timestamp within the clock-skew allowance is acceptable.
	r := validResult(at, idx)
	r.Timestamp = at.Add(10 * time.Millisecond)
	if _, err := svc.Admit(r); err != nil {
		t.Fatalf("timestamp within skew rejected: %v", err)
	}
}

func TestAdmitRejectsNegativeAndInvalidTiming(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)

	// Da*Db > Ra*Rb makes the numerator negative => negative ToF/distance.
	r := validResult(at, 1)
	r.Ra, r.Rb, r.Da, r.Db = 100, 100, 200, 200
	if _, err := svc.Admit(r); !errors.Is(err, ErrNegativeDistance) {
		t.Fatalf("negative ToF: want ErrNegativeDistance, got %v", err)
	}

	// Zero denominator means the DS-TWR estimator is undefined.
	r = validResult(at, 2)
	r.Ra, r.Rb, r.Da, r.Db = 0, 0, 0, 0
	if _, err := svc.Admit(r); !errors.Is(err, ErrInvalidTiming) {
		t.Fatalf("zero denominator: want ErrInvalidTiming, got %v", err)
	}

	// Negative raw timing is never legal.
	r = validResult(at, 3)
	r.Rb = -1
	if _, err := svc.Admit(r); !errors.Is(err, ErrInvalidTiming) {
		t.Fatalf("negative timing: want ErrInvalidTiming, got %v", err)
	}
}

func TestDSTWRFormula(t *testing.T) {
	// Symmetric exchange: Ra=Rb=100 ns, Da=Db=20 ns.
	// ToF = (100*100 - 20*20) / (100+100+20+20) = 9600/240 = 40 ns.
	tof, err := dsTWRTimeOfFlight(100, 100, 20, 20)
	if err != nil {
		t.Fatalf("dsTWR: %v", err)
	}
	if tof != 40*time.Nanosecond {
		t.Fatalf("ToF = %v, want 40ns", tof)
	}

	// Asymmetric check against the same closed form.
	ra, rb, da, db := 120.0, 80.0, 30.0, 10.0
	wantNS := (ra*rb - da*db) / (ra + rb + da + db)
	tof, err = dsTWRTimeOfFlight(
		time.Duration(ra), time.Duration(rb),
		time.Duration(da), time.Duration(db),
	)
	if err != nil {
		t.Fatalf("asymmetric dsTWR: %v", err)
	}
	if math.Abs(float64(tof.Nanoseconds())-wantNS) > 1.0 {
		t.Fatalf("ToF = %v, want %vns", tof, wantNS)
	}
}

func TestAdmitComputesDistanceFromToF(t *testing.T) {
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	svc, _ := newTestService(t, at)

	r := validResult(at, 1)
	r.Ra, r.Rb, r.Da, r.Db = 100, 100, 20, 20
	dec, err := svc.Admit(r)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	wantMeters := 40.0 * SpeedOfLightMetersPerNanosecond
	if math.Abs(dec.DistanceMeters-wantMeters) > 1e-12 {
		t.Fatalf("distance = %v m, want %v m", dec.DistanceMeters, wantMeters)
	}
	if dec.TimeOfFlight != 40*time.Nanosecond {
		t.Fatalf("ToF = %v, want 40ns", dec.TimeOfFlight)
	}
}

func TestRegisterRejectsInvalidAndDuplicateSessions(t *testing.T) {
	svc := NewService()
	mk := func(id string, limit time.Duration, start, end time.Time) *Session {
		return &Session{
			ID:            id,
			MaxReplyDelay: limit,
			StartTime:     start,
			Deadline:      end,
		}
	}
	start := time.Now()
	base := Session{
		ID:            "s",
		MaxReplyDelay: time.Millisecond,
		StartTime:     start,
		Deadline:      start.Add(time.Minute),
	}
	if err := svc.Register(&base); err != nil {
		t.Fatalf("register base: %v", err)
	}
	if err := svc.Register(&base); !errors.Is(err, ErrSessionReused) {
		t.Fatalf("duplicate: want ErrSessionReused, got %v", err)
	}

	if err := svc.Register(mk("", time.Millisecond, start, start.Add(time.Minute))); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("empty id: want ErrInvalidSession, got %v", err)
	}
	if err := svc.Register(mk("s2", 0, start, start.Add(time.Minute))); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("zero limit: want ErrInvalidSession, got %v", err)
	}
	if err := svc.Register(mk("s3", time.Millisecond, start, start)); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("bad window: want ErrInvalidSession, got %v", err)
	}
}
