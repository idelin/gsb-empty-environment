package timetrust

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestNewValidation(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	verifier := Ed25519Verifier{PublicKey: keys.public}
	store := &memoryStore{}

	if _, err := New(nil, verifier, store); err == nil {
		t.Fatalf("New accepted nil clock")
	}
	if _, err := New(clock, nil, store); err == nil {
		t.Fatalf("New accepted nil verifier")
	}
	if _, err := New(clock, verifier, nil); err == nil {
		t.Fatalf("New accepted nil store")
	}
	if _, err := NewWithDrift(clock, verifier, store, -0.1); err == nil {
		t.Fatalf("NewWithDrift accepted negative drift")
	}
	if _, err := NewWithDrift(clock, verifier, store, 1); err == nil {
		t.Fatalf("NewWithDrift accepted drift >= 1")
	}

	// A store whose Load fails must surface the error during construction.
	loadErrStore := &loadErrorStore{}
	if _, err := New(clock, verifier, loadErrStore); !errors.Is(err, errFaultyStore) {
		t.Fatalf("New load error = %v, want injected failure", err)
	}
}

type loadErrorStore struct{}

func (loadErrorStore) Load() (time.Time, error) { return time.Time{}, errFaultyStore }
func (loadErrorStore) Save(time.Time) error     { return nil }

// zeroMonoClock reports a wall time but never a monotonic reading.
type zeroMonoClock struct{ wall time.Time }

func (c zeroMonoClock) Now() (time.Time, time.Duration) { return c.wall, 0 }

func TestMissingMonotonicReadingRejected(t *testing.T) {
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, zeroMonoClock{wall: baseTime()}, &memoryStore{}, keys)

	if _, err := trust.IssueChallenge(); !errors.Is(err, ErrNoMonotonicReading) {
		t.Fatalf("IssueChallenge error = %v, want ErrNoMonotonicReading", err)
	}

	// Issue on a working clock, then swap in a clock that lost monotonicity.
	good := NewFakeClock(baseTime())
	trust = newTestTrust(t, good, &memoryStore{}, keys)
	nonce, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	proof := SignProof(keys.private, baseTime(), nonce)
	trust.clock = zeroMonoClock{wall: baseTime()}
	if err := trust.ObserveProof(proof); !errors.Is(err, ErrNoMonotonicReading) {
		t.Fatalf("ObserveProof error = %v, want ErrNoMonotonicReading", err)
	}
}

func TestChallengeIssueValidation(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)

	if _, err := trust.IssueChallengeWithTTL(0); err == nil {
		t.Fatalf("accepted zero TTL")
	}

	nonce1, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	nonce2, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("second IssueChallenge: %v", err)
	}
	if string(nonce1) == string(nonce2) {
		t.Fatalf("two challenges returned the same nonce")
	}
}

func TestObserveProofRejectsZeroTimestamp(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)

	nonce, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	zero := TimeProof{
		Timestamp: time.Time{},
		Nonce:     nonce,
		Signature: make([]byte, ed25519.SignatureSize),
	}
	if err := trust.ObserveProof(zero); !errors.Is(err, ErrMalformedProof) {
		t.Fatalf("zero-timestamp proof error = %v, want ErrMalformedProof", err)
	}
}

func TestFileStoreRejectsZeroBound(t *testing.T) {
	store := NewFileStore(t.TempDir() + "/lower-bound")
	if err := store.Save(time.Time{}); err == nil {
		t.Fatalf("Save accepted a zero bound")
	}
}

func TestDecisionStringAndFakeClockControls(t *testing.T) {
	if DecisionUnknown.String() != "unknown" ||
		DecisionValid.String() != "valid" ||
		DecisionUncertain.String() != "uncertain" ||
		DecisionExpired.String() != "expired" {
		t.Fatalf("unexpected Decision string values")
	}

	clock := NewFakeClock(baseTime())
	wall, mono := clock.Now()
	if !wall.Equal(baseTime()) || mono != time.Second {
		t.Fatalf("fake clock initial reading = %v / %v", wall, mono)
	}
	clock.SetWall(baseTime().Add(-time.Hour)).
		Advance(time.Minute).
		AdvanceMonotonic(2 * time.Minute).
		ResetMonotonic()
	wall, mono = clock.Now()
	if !wall.Equal(baseTime().Add(-time.Hour).Add(time.Minute)) {
		t.Fatalf("fake wall = %v", wall)
	}
	if mono != time.Millisecond {
		t.Fatalf("monotonic after reset = %v, want 1ms", mono)
	}

	systemWall, systemMono := SystemClock{}.Now()
	if systemWall.IsZero() || systemMono < 0 {
		t.Fatalf("system clock reading = %v / %v", systemWall, systemMono)
	}
}

func TestUnknownProofNonceWithoutChallenge(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)

	proof := SignProof(keys.private, baseTime(), []byte("aaaaaaaaaaaaaaaa"))
	if err := trust.ObserveProof(proof); !errors.Is(err, ErrReplay) {
		t.Fatalf("error = %v, want ErrReplay", err)
	}
}
