// Package timetrust maintains a trusted wall-clock interval on a device that
// has no authoritative clock of its own.
//
// Offline update hosts must decide whether signed update metadata has
// expired. The device wall clock is attacker-influenced (the user can set it
// to any value) and a reboot destroys monotonic-timer continuity. The only
// trustworthy input is a signed time proof issued in response to a
// single-use device challenge. From each accepted proof the package derives
// an interval [lower, upper] around the current time; only the lower bound is
// persisted, so a restart can prove "it is at least T" but never "it is at
// most T", which is exactly the direction expiry safety depends on.
package timetrust

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"time"
)

// DefaultChallengeTTL limits how long an issued challenge may be answered.
const DefaultChallengeTTL = 10 * time.Minute

// DefaultMaxDrift bounds the assumed relative frequency error between the
// monotonic timer and the signed clock. It is an open interval [0, 1).
const DefaultMaxDrift = 1e-4

// Decision is the result of evaluating an expiry deadline against the trusted
// interval.
type Decision int

const (
	// DecisionUnknown means no trustworthy interval exists yet; the caller
	// must block the install and obtain a signed proof.
	DecisionUnknown Decision = iota
	// DecisionValid means the whole interval is strictly before the deadline,
	// so non-expiry is guaranteed.
	DecisionValid
	// DecisionUncertain means the interval straddles (or touches) the
	// deadline; non-expiry cannot be proved, so the install must be blocked.
	DecisionUncertain
	// DecisionExpired means the whole interval is at or past the deadline.
	DecisionExpired
)

// String renders a Decision for logs.
func (d Decision) String() string {
	switch d {
	case DecisionValid:
		return "valid"
	case DecisionUncertain:
		return "uncertain"
	case DecisionExpired:
		return "expired"
	default:
		return "unknown"
	}
}

var (
	// ErrReplay means a proof answers a challenge that was already consumed.
	ErrReplay = errors.New("timetrust: proof nonce is not an outstanding challenge")
	// ErrStaleProof means a signed timestamp would move the lower bound
	// backwards and is therefore rejected.
	ErrStaleProof = errors.New("timetrust: proof timestamp predates trusted lower bound")
	// ErrChallengeExpired means the proof arrived after the challenge TTL.
	ErrChallengeExpired = errors.New("timetrust: challenge expired before proof arrived")
	// ErrNoMonotonicReading means the clock cannot provide a monotonic time.
	ErrNoMonotonicReading = errors.New("timetrust: clock has no monotonic reading")
)

type challengeState struct {
	issuedAt time.Duration
	ttl      time.Duration
}

// Trust owns the trusted time state for one update host.
type Trust struct {
	clock    Clock
	verifier ProofVerifier
	store    LowerBoundStore
	maxDrift float64

	// durableLower is the lower bound that has been committed to the store.
	// It is the only value that survives a restart.
	durableLower time.Time
	// memoryLower may run ahead of durableLower while a save is failing. It
	// never widens validity: an upper bound is only ever anchored when the
	// corresponding lower bound was durably committed.
	memoryLower time.Time

	// Monotonic anchor of the most recently accepted, durably committed proof.
	anchor     time.Time
	anchorMono time.Duration
	hasAnchor  bool
	lastMono   time.Duration

	challenges map[string]challengeState
}

// New constructs a Trust and loads the persisted lower bound. After a reboot
// hasAnchor is false even though the lower bound survives: no upper bound can
// be reconstructed until a fresh proof is observed.
func New(clock Clock, verifier ProofVerifier, store LowerBoundStore) (*Trust, error) {
	return newTrust(clock, verifier, store, DefaultMaxDrift)
}

// NewWithDrift is New with an explicit maximum relative drift in [0, 1).
func NewWithDrift(clock Clock, verifier ProofVerifier, store LowerBoundStore, maxDrift float64) (*Trust, error) {
	return newTrust(clock, verifier, store, maxDrift)
}

func newTrust(clock Clock, verifier ProofVerifier, store LowerBoundStore, maxDrift float64) (*Trust, error) {
	if clock == nil || verifier == nil || store == nil {
		return nil, errors.New("timetrust: clock, verifier and store are required")
	}
	if maxDrift < 0 || maxDrift >= 1 {
		return nil, errors.New("timetrust: maxDrift must be in [0, 1)")
	}
	lower, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &Trust{
		clock:        clock,
		verifier:     verifier,
		store:        store,
		maxDrift:     maxDrift,
		durableLower: lower,
		memoryLower:  lower,
		challenges:   make(map[string]challengeState),
	}, nil
}

// IssueChallenge creates a single-use 128-bit nonce bound to this device and
// the current monotonic reading. The nonce must be embedded in the server's
// signed proof.
func (t *Trust) IssueChallenge() ([]byte, error) {
	return t.IssueChallengeWithTTL(DefaultChallengeTTL)
}

// IssueChallengeWithTTL issues a challenge whose proof must arrive within ttl
// as measured by the monotonic timer.
func (t *Trust) IssueChallengeWithTTL(ttl time.Duration) ([]byte, error) {
	if ttl <= 0 {
		return nil, errors.New("timetrust: challenge TTL must be positive")
	}
	_, mono := t.clock.Now()
	if mono <= 0 {
		return nil, ErrNoMonotonicReading
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("timetrust: generating challenge: %w", err)
	}
	t.challenges[string(nonce)] = challengeState{issuedAt: mono, ttl: ttl}
	return nonce, nil
}

// ObserveProof authenticates a signed proof, binds it to an outstanding
// device challenge and, on success, durably advances the lower bound and
// re-anchors the trusted interval.
//
// The anchor is only installed after the store confirms the commit. If the
// commit fails, no upper bound is produced and the returned error must be
// surfaced: a storage failure can therefore never extend validity.
func (t *Trust) ObserveProof(proof TimeProof) error {
	if err := t.verifier.Verify(proof); err != nil {
		return err
	}
	if proof.Timestamp.IsZero() {
		return ErrMalformedProof
	}

	_, mono := t.clock.Now()
	if mono <= 0 {
		return ErrNoMonotonicReading
	}

	challenge, ok := t.challenges[string(proof.Nonce)]
	if !ok {
		// Unknown or already consumed nonce: a replayed proof ends here even
		// if its signature is still valid.
		return ErrReplay
	}
	if mono < challenge.issuedAt {
		// Monotonic timer reset since the challenge was issued: the response
		// cannot be measured against it, so invalidate every outstanding
		// challenge and demand a new round trip.
		t.invalidateChallenges()
		return ErrChallengeExpired
	}
	if mono-challenge.issuedAt > challenge.ttl {
		delete(t.challenges, string(proof.Nonce))
		return ErrChallengeExpired
	}
	// Consume the single-use nonce before any further work so a retry of the
	// same proof is a replay.
	delete(t.challenges, string(proof.Nonce))

	// Monotonic continuity since the previous anchor must hold; a reset means
	// the old upper bound is gone until this new proof anchors a fresh one.
	if t.hasAnchor && mono < t.lastMono {
		t.hasAnchor = false
		t.anchor = time.Time{}
		t.anchorMono = 0
	}
	t.lastMono = mono

	// Signed time can only move the lower bound forward.
	if !t.memoryLower.IsZero() && !proof.Timestamp.After(t.memoryLower) {
		return ErrStaleProof
	}

	// Durable commit first: the anchor exists only while its lower bound is
	// on disk. A failed save leaves the previous state intact and grants no
	// additional validity.
	if err := t.store.Save(proof.Timestamp); err != nil {
		return fmt.Errorf("timetrust: persisting lower bound: %w", err)
	}

	t.durableLower = proof.Timestamp
	t.memoryLower = proof.Timestamp
	t.anchor = proof.Timestamp
	t.anchorMono = mono
	t.hasAnchor = true
	return nil
}

// Checkpoint retries the durable commit of the current in-memory lower bound.
// It is useful after earlier ObserveProof storage failures once the store is
// healthy again; it never moves any bound backwards.
func (t *Trust) Checkpoint() error {
	if t.memoryLower.IsZero() || !t.memoryLower.After(t.durableLower) {
		return nil
	}
	if err := t.store.Save(t.memoryLower); err != nil {
		return fmt.Errorf("timetrust: persisting lower bound: %w", err)
	}
	t.durableLower = t.memoryLower
	return nil
}

// TrustedInterval reports the interval [lower, upper] that the current time
// is provably inside. ok is false when no bound exists at all. When
// hasUpper is false the upper bound is unknown (after a reboot, a monotonic
// reset, or a failed commit), so only "now >= lower" can be claimed.
func (t *Trust) TrustedInterval() (lower, upper time.Time, hasLower, hasUpper bool) {
	lowerBound := t.durableLower
	if t.memoryLower.After(lowerBound) {
		lowerBound = t.memoryLower
	}

	_, mono := t.clock.Now()
	if t.hasAnchor && mono >= t.anchorMono {
		elapsed := mono - t.anchorMono
		loDelta := time.Duration(math.Floor(float64(elapsed) * (1 - t.maxDrift)))
		hiDelta := time.Duration(math.Ceil(float64(elapsed) * (1 + t.maxDrift)))
		lowerBound = t.maxTime(lowerBound, t.anchor.Add(loDelta))
		upper = t.anchor.Add(hiDelta)
		hasUpper = true
	}
	return lowerBound, upper, !lowerBound.IsZero(), hasUpper
}

// Evaluate decides whether metadata expiring at deadline can still be
// trusted. Anything other than DecisionValid blocks the install: an unknown
// upper bound yields DecisionUncertain rather than a guess.
func (t *Trust) Evaluate(deadline time.Time) Decision {
	lower, upper, hasLower, hasUpper := t.TrustedInterval()
	if !hasLower {
		return DecisionUnknown
	}
	if !hasUpper {
		// Post-restart state (or post-reset / failed commit): only the lower
		// bound survived. If even the lower bound is past the deadline the
		// metadata is certainly expired; otherwise non-expiry is unprovable.
		if !lower.Before(deadline) {
			return DecisionExpired
		}
		return DecisionUncertain
	}
	switch {
	case !upper.Before(deadline) && lower.Before(deadline):
		return DecisionUncertain
	case !lower.Before(deadline):
		return DecisionExpired
	default:
		return DecisionValid
	}
}

// DurableLower returns the lower bound currently confirmed by the store.
func (t *Trust) DurableLower() time.Time {
	return t.durableLower
}

func (t *Trust) invalidateChallenges() {
	t.challenges = make(map[string]challengeState)
}

func (t *Trust) maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
