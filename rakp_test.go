package netsec

import (
	"errors"
	"testing"
	"time"
)

const testBucket = 50 * time.Millisecond

func newTestAuthenticator(reg *Registry) *Authenticator {
	users := MapUserStore{"root": []byte("s3cret")}
	clock := NewManualClock(time.Unix(1_700_000_000, 0))
	return NewAuthenticator(reg, users, clock, testBucket)
}

func rakp1(source string, session uint32, username string) *RAKP1Request {
	return &RAKP1Request{
		SourceAddr: source,
		SuiteID:    3,
		SessionID:  session,
		Username:   username,
	}
}

func TestUserEnumerationResistance(t *testing.T) {
	auth := newTestAuthenticator(registeredRegistry(t))
	clock := auth.clock.(*ManualClock)

	// Known user, wrong password later.
	startKnown := clock.Now()
	rakp2Known, err := auth.HandleRAKP1(rakp1("10.0.0.1", 100, "root"))
	if err != nil {
		t.Fatalf("RAKP1 known user: %v", err)
	}
	knownElapsed := clock.Now().Sub(startKnown)

	// Unknown user: must not error early, must match length and time bucket.
	startUnknown := clock.Now()
	rakp2Unknown, err := auth.HandleRAKP1(rakp1("10.0.0.1", 101, "ghost"))
	if err != nil {
		t.Fatalf("RAKP1 unknown user leaked error: %v", err)
	}
	unknownElapsed := clock.Now().Sub(startUnknown)

	if len(rakp2Known) != RAKP2PayloadLen || len(rakp2Unknown) != RAKP2PayloadLen {
		t.Fatalf("RAKP2 lengths differ: known=%d unknown=%d", len(rakp2Known), len(rakp2Unknown))
	}
	if knownElapsed != unknownElapsed {
		t.Fatalf("response time buckets differ: known=%v unknown=%v", knownElapsed, unknownElapsed)
	}
	if knownElapsed%testBucket != 0 || knownElapsed == 0 {
		t.Fatalf("response time %v not padded to whole buckets of %v", knownElapsed, testBucket)
	}

	// Wrong password for the known user and any password for the unknown
	// user must fail RAKP3 with the identical error.
	badCode := make([]byte, 32)
	errKnown := auth.HandleRAKP3(&RAKP3Request{SourceAddr: "10.0.0.1", SessionID: 100, AuthCode: badCode})
	errUnknown := auth.HandleRAKP3(&RAKP3Request{SourceAddr: "10.0.0.1", SessionID: 101, AuthCode: badCode})
	if !errors.Is(errKnown, ErrAuthFailed) || !errors.Is(errUnknown, ErrAuthFailed) {
		t.Fatalf("RAKP3 errors differ: known=%v unknown=%v", errKnown, errUnknown)
	}
}

func TestSuccessfulAuthentication(t *testing.T) {
	auth := newTestAuthenticator(registeredRegistry(t))
	req := rakp1("10.0.0.2", 200, "root")
	rakp2, err := auth.HandleRAKP1(req)
	if err != nil {
		t.Fatalf("RAKP1: %v", err)
	}
	// Client side: parse BMC random and GUID out of the RAKP2 payload and
	// prove password knowledge with RAKP3AuthCode.
	var bmcRand, guid [16]byte
	copy(bmcRand[:], rakp2[4:20])
	copy(guid[:], rakp2[20:36])
	code := RAKP3AuthCode([]byte("s3cret"), req.SessionID, req.ConsoleRand, bmcRand, guid)
	if err := auth.HandleRAKP3(&RAKP3Request{
		SourceAddr: "10.0.0.2",
		SessionID:  req.SessionID,
		AuthCode:   code,
	}); err != nil {
		t.Fatalf("RAKP3 with correct proof: %v", err)
	}
}

func TestAnonymousLoginRejected(t *testing.T) {
	auth := newTestAuthenticator(registeredRegistry(t))
	_, err := auth.HandleRAKP1(rakp1("10.0.0.3", 300, ""))
	if !errors.Is(err, ErrAnonymous) {
		t.Fatalf("anonymous login: got %v, want ErrAnonymous", err)
	}
}

func TestFailureThrottle(t *testing.T) {
	auth := newTestAuthenticator(registeredRegistry(t))
	clock := auth.clock.(*ManualClock)
	src := "10.0.0.4"

	// 11 failures (> 10) inside the window.
	for i := 0; i < 11; i++ {
		if _, err := auth.HandleRAKP1(rakp1(src, uint32(400+i), "")); !errors.Is(err, ErrAnonymous) {
			t.Fatalf("failure %d: got %v, want ErrAnonymous", i, err)
		}
	}

	// Subsequent requests from the same source are dropped, even valid ones.
	if _, err := auth.HandleRAKP1(rakp1(src, 500, "root")); !errors.Is(err, ErrDropped) {
		t.Fatalf("throttled source: got %v, want ErrDropped", err)
	}

	// A different source is unaffected.
	if _, err := auth.HandleRAKP1(rakp1("10.0.0.5", 501, "root")); err != nil {
		t.Fatalf("other source must not be throttled: %v", err)
	}

	// After the 60s window slides past, the source is admitted again.
	clock.Advance(61 * time.Second)
	if _, err := auth.HandleRAKP1(rakp1(src, 502, "root")); err != nil {
		t.Fatalf("source after window expiry: %v", err)
	}
}
