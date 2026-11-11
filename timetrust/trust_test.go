package timetrust

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestInitialStateIsUnknownAndBlocks(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)

	if got := trust.Evaluate(baseTime().Add(time.Hour)); got != DecisionUnknown {
		t.Fatalf("fresh trust decision = %s, want unknown", got)
	}
	if _, _, hasLower, hasUpper := trust.TrustedInterval(); hasLower || hasUpper {
		t.Fatalf("fresh trust unexpectedly reports bounds")
	}
}

func TestValidUncertainExpiredDecisions(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)

	observeSigned(t, trust, keys, baseTime())

	// Advance one hour; with drift ~100 ppm the interval is roughly
	// [now-360ms, now+360ms] around base+1h.
	clock.Advance(time.Hour)
	lower, upper, hasLower, hasUpper := trust.TrustedInterval()
	if !hasLower || !hasUpper {
		t.Fatalf("expected a full interval, got hasLower=%v hasUpper=%v", hasLower, hasUpper)
	}
	if !lower.Before(upper) {
		t.Fatalf("interval inverted: %v .. %v", lower, upper)
	}

	now := baseTime().Add(time.Hour)
	cases := []struct {
		name     string
		deadline time.Time
		want     Decision
	}{
		{"deadline clearly after interval", now.Add(time.Minute), DecisionValid},
		{"deadline inside interval", lower.Add(time.Nanosecond), DecisionUncertain},
		{"deadline at upper bound", upper, DecisionUncertain},
		{"deadline at lower bound", lower, DecisionExpired},
		{"deadline before interval", now.Add(-time.Minute), DecisionExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := trust.Evaluate(tc.deadline); got != tc.want {
				t.Fatalf("Evaluate(%v) = %s, want %s", tc.deadline, got, tc.want)
			}
		})
	}
}

func TestWallClockRollbackCannotExtend(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	store := &memoryStore{}
	trust := newTestTrust(t, clock, store, keys)
	observeSigned(t, trust, keys, baseTime())

	clock.Advance(30 * time.Minute)
	beforeRollback, _, _, _ := trust.TrustedInterval()

	// Attacker winds the wall clock a full day backwards; monotonic time is
	// untouched, so every trust output must be identical.
	clock.SetWall(baseTime().Add(-24 * time.Hour))
	afterRollback, _, hasLower, hasUpper := trust.TrustedInterval()
	if !hasLower || !hasUpper {
		t.Fatalf("rollback destroyed the interval")
	}
	if !afterRollback.Equal(beforeRollback) {
		t.Fatalf("lower bound moved after wall rollback: %v -> %v", beforeRollback, afterRollback)
	}

	deadline := baseTime().Add(31 * time.Minute)
	first := trust.Evaluate(deadline)
	clock.SetWall(baseTime().Add(-365 * 24 * time.Hour))
	clock.SetWall(baseTime().Add(365 * 24 * time.Hour))
	if got := trust.Evaluate(deadline); got != first {
		t.Fatalf("decision changed across wall-clock manipulation: %s -> %s", first, got)
	}

	if got := trust.DurableLower(); !got.Equal(baseTime()) {
		t.Fatalf("durable lower bound = %v, want %v", got, baseTime())
	}
}

func TestMonotonicResetDropsUpperBoundAndBlocks(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	store := &memoryStore{}
	trust := newTestTrust(t, clock, store, keys)
	observeSigned(t, trust, keys, baseTime())
	clock.Advance(time.Hour)

	// Before the reset a deadline safely after the interval is decidable.
	deadline := baseTime().Add(2 * time.Hour)
	if got := trust.Evaluate(deadline); got != DecisionValid {
		t.Fatalf("pre-reset decision = %s, want valid", got)
	}

	clock.ResetMonotonic()

	// The lower bound survives; the upper bound is gone.
	lower, _, hasLower, hasUpper := trust.TrustedInterval()
	if !hasLower || hasUpper {
		t.Fatalf("after reset: hasLower=%v hasUpper=%v, want true/false", hasLower, hasUpper)
	}
	if !lower.Equal(baseTime()) {
		t.Fatalf("lower bound after reset = %v, want %v", lower, baseTime())
	}

	// Any non-expired deadline is now uncertain and must block the install.
	if got := trust.Evaluate(deadline); got != DecisionUncertain {
		t.Fatalf("post-reset decision = %s, want uncertain", got)
	}
	// A deadline already behind the surviving lower bound is provably expired.
	if got := trust.Evaluate(baseTime().Add(-time.Second)); got != DecisionExpired {
		t.Fatalf("post-reset past deadline = %s, want expired", got)
	}

	// A fresh proof after the reset re-establishes the full interval.
	clock.Advance(time.Minute)
	observeSigned(t, trust, keys, baseTime().Add(2*time.Hour))
	if _, _, hasLower, hasUpper := trust.TrustedInterval(); !hasLower || !hasUpper {
		t.Fatalf("fresh proof did not restore the interval")
	}
	if got := trust.Evaluate(baseTime().Add(3 * time.Hour)); got != DecisionValid {
		t.Fatalf("post-recovery decision = %s, want valid", got)
	}
}

func TestRestartKeepsOnlyPersistedLowerBound(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	store := &memoryStore{}
	trust := newTestTrust(t, clock, store, keys)
	observeSigned(t, trust, keys, baseTime())
	clock.Advance(2 * time.Hour)

	// Simulate a full process restart against the same durable store.
	reloaded := newTestTrust(t, clock, store, keys)
	lower, upper, hasLower, hasUpper := reloaded.TrustedInterval()
	if !hasLower || hasUpper {
		t.Fatalf("restart: hasLower=%v hasUpper=%v, want true/false", hasLower, hasUpper)
	}
	if !lower.Equal(baseTime()) {
		t.Fatalf("restart lower bound = %v, want %v", lower, baseTime())
	}
	if !upper.IsZero() {
		t.Fatalf("restart leaked an upper bound: %v", upper)
	}

	// Even with the wall clock set far into the future, no install is allowed.
	clock.SetWall(baseTime().Add(1000 * 24 * time.Hour))
	if got := reloaded.Evaluate(baseTime().Add(3 * time.Hour)); got != DecisionUncertain {
		t.Fatalf("restart decision = %s, want uncertain", got)
	}

	// A lower bound past the deadline still proves expiry after restart.
	if got := reloaded.Evaluate(baseTime().Add(-time.Minute)); got != DecisionExpired {
		t.Fatalf("restart expired decision = %s, want expired", got)
	}

	// A fresh signed proof restores decision-making.
	observeSigned(t, reloaded, keys, baseTime().Add(3*time.Hour))
	if got := reloaded.Evaluate(baseTime().Add(4 * time.Hour)); got != DecisionValid {
		t.Fatalf("post-restart proof decision = %s, want valid", got)
	}
}

func TestReplayRejected(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)

	nonce, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	proof := SignProof(keys.private, baseTime(), nonce)
	if err := trust.ObserveProof(proof); err != nil {
		t.Fatalf("first ObserveProof: %v", err)
	}

	// The exact same signed bytes must never be accepted again.
	err = trust.ObserveProof(proof)
	if !errors.Is(err, ErrReplay) {
		t.Fatalf("replay error = %v, want ErrReplay", err)
	}

	// A proof for an unknown nonce (captured from another device or an
	// earlier run) is rejected the same way.
	foreign := SignProof(keys.private, baseTime().Add(time.Hour), []byte("never-issued-nonce!!"))
	if err := trust.ObserveProof(foreign); !errors.Is(err, ErrReplay) {
		t.Fatalf("foreign nonce error = %v, want ErrReplay", err)
	}
}

func TestForgedAndTamperedProofsRejected(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)

	nonce, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	good := SignProof(keys.private, baseTime(), nonce)

	t.Run("wrong key", func(t *testing.T) {
		other := newTestKeyPair(t)
		forged := SignProof(other.private, baseTime(), nonce)
		if err := trust.ObserveProof(forged); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("forged signature error = %v, want ErrBadSignature", err)
		}
	})

	t.Run("tampered timestamp", func(t *testing.T) {
		tampered := good
		tampered.Timestamp = baseTime().Add(time.Hour)
		if err := trust.ObserveProof(tampered); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("tampered timestamp error = %v, want ErrBadSignature", err)
		}
	})

	t.Run("malformed proof", func(t *testing.T) {
		malformed := good
		malformed.Signature = malformed.Signature[:ed25519.SignatureSize-1]
		if err := trust.ObserveProof(malformed); !errors.Is(err, ErrMalformedProof) {
			t.Fatalf("malformed proof error = %v, want ErrMalformedProof", err)
		}
	})

	// None of the rejects consumed the original nonce.
	if err := trust.ObserveProof(good); err != nil {
		t.Fatalf("legitimate proof after rejects: %v", err)
	}
}

func TestChallengeTTLAndMonotonicReset(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)

	t.Run("expired challenge", func(t *testing.T) {
		nonce, err := trust.IssueChallengeWithTTL(time.Minute)
		if err != nil {
			t.Fatalf("IssueChallengeWithTTL: %v", err)
		}
		clock.Advance(time.Minute + time.Nanosecond)
		proof := SignProof(keys.private, baseTime().Add(time.Minute), nonce)
		if err := trust.ObserveProof(proof); !errors.Is(err, ErrChallengeExpired) {
			t.Fatalf("expired challenge error = %v, want ErrChallengeExpired", err)
		}
		// Replay of the late proof is not given a second chance.
		if err := trust.ObserveProof(proof); !errors.Is(err, ErrReplay) {
			t.Fatalf("late proof replay error = %v, want ErrReplay", err)
		}
	})

	t.Run("reset during outstanding challenge", func(t *testing.T) {
		nonce, err := trust.IssueChallengeWithTTL(time.Hour)
		if err != nil {
			t.Fatalf("IssueChallengeWithTTL: %v", err)
		}
		clock.ResetMonotonic()
		proof := SignProof(keys.private, baseTime().Add(time.Second), nonce)
		if err := trust.ObserveProof(proof); !errors.Is(err, ErrChallengeExpired) {
			t.Fatalf("post-reset challenge error = %v, want ErrChallengeExpired", err)
		}
	})
}

func TestStaleProofCannotMoveLowerBoundBackwards(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)
	observeSigned(t, trust, keys, baseTime())

	// An older signed timestamp (for example a delayed stale server response)
	// must not roll the lower bound back.
	nonce, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	stale := SignProof(keys.private, baseTime().Add(-time.Hour), nonce)
	if err := trust.ObserveProof(stale); !errors.Is(err, ErrStaleProof) {
		t.Fatalf("stale proof error = %v, want ErrStaleProof", err)
	}
	if got := trust.DurableLower(); !got.Equal(baseTime()) {
		t.Fatalf("lower bound changed after stale proof: %v", got)
	}

	// The nonce was consumed by the stale attempt, so replaying it is a
	// replay rather than a second stale check.
	if err := trust.ObserveProof(stale); !errors.Is(err, ErrReplay) {
		t.Fatalf("stale replay error = %v, want ErrReplay", err)
	}
}

func TestMonotonicOnlyAdvanceMovesInterval(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	trust := newTestTrust(t, clock, &memoryStore{}, keys)
	observeSigned(t, trust, keys, baseTime())

	// Elapsed time measured by the monotonic timer alone (wall clock frozen)
	// still advances the trusted interval.
	clock.AdvanceMonotonic(90 * time.Minute)
	lower, upper, hasLower, hasUpper := trust.TrustedInterval()
	if !hasLower || !hasUpper {
		t.Fatalf("missing interval after monotonic-only advance")
	}
	expected := baseTime().Add(90 * time.Minute)
	if lower.Before(expected.Add(-time.Second)) || upper.After(expected.Add(time.Second)) {
		t.Fatalf("interval %v..%v not centered on %v", lower, upper, expected)
	}

	// A deadline far enough beyond the drifted upper bound stays installable.
	if got := trust.Evaluate(expected.Add(time.Hour)); got != DecisionValid {
		t.Fatalf("decision = %s, want valid", got)
	}
}

func TestDriftWidensIntervalConservatively(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	store := &memoryStore{}
	trust, err := NewWithDrift(clock, Ed25519Verifier{PublicKey: keys.public}, store, 0.5)
	if err != nil {
		t.Fatalf("NewWithDrift: %v", err)
	}
	observeSigned(t, trust, keys, baseTime())

	clock.Advance(100 * time.Second)
	lower, upper, _, _ := trust.TrustedInterval()
	// 50% relative drift: elapsed 100s means true elapsed time is in [50s, 150s].
	if got := lower.Sub(baseTime()); got != 50*time.Second {
		t.Fatalf("lower drift bound = %v, want 50s", got)
	}
	if got := upper.Sub(baseTime()); got != 150*time.Second {
		t.Fatalf("upper drift bound = %v, want 150s", got)
	}

	if _, err := NewWithDrift(clock, Ed25519Verifier{PublicKey: keys.public}, store, 1); err == nil {
		t.Fatalf("expected error for drift >= 1")
	}
}
