package stepup

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Sentinel errors returned by the service. Callers use errors.Is to branch on
// these values.
var (
	// ErrNotFound is returned when a challenge or receipt id is unknown.
	ErrNotFound = errors.New("stepup: challenge or receipt not found")
	// ErrChallengeInvalid is returned when a challenge is not in a state that
	// permits the requested operation (redeemed or invalidated by rebind).
	ErrChallengeInvalid = errors.New("stepup: challenge is invalid")
	// ErrChallengeExpired is returned when the challenge TTL has elapsed.
	// Expiry is inclusive: a challenge expiring at time T is invalid at T.
	ErrChallengeExpired = errors.New("stepup: challenge has expired")
	// ErrGenerationMismatch is returned when the second-factor device bound
	// to the challenge is no longer the user's current device generation.
	ErrGenerationMismatch = errors.New("stepup: device generation mismatch")
	// ErrSessionMismatch is returned when a challenge/receipt is presented by
	// a different login session than the one that created it.
	ErrSessionMismatch = errors.New("stepup: session mismatch")
	// ErrOperationMismatch is returned when a receipt is redeemed for a
	// different target operation than the challenge was created for.
	ErrOperationMismatch = errors.New("stepup: operation mismatch")
	// ErrInvalidFactor is returned when the submitted second factor is wrong.
	ErrInvalidFactor = errors.New("stepup: second-factor verification failed")
	// ErrAlreadyRedeemed is returned when a one-time receipt is presented
	// again after a successful redemption.
	ErrAlreadyRedeemed = errors.New("stepup: receipt already redeemed")
)

// FactorVerifier validates a second-factor code against the user's currently
// bound device. Implementations must be safe for concurrent use.
type FactorVerifier interface {
	Verify(userID, code string) bool
}

// FactorVerifierFunc adapts a plain function to FactorVerifier.
type FactorVerifierFunc func(userID, code string) bool

// Verify calls f(userID, code).
func (f FactorVerifierFunc) Verify(userID, code string) bool { return f(userID, code) }

const (
	statusPending     = "pending"
	statusVerified    = "verified"
	statusRedeemed    = "redeemed"
	statusInvalidated = "invalidated"
)

// challenge is the single state-machine record backing both the verification
// challenge and the one-time receipt issued on success.
type challenge struct {
	id         string
	userID     string
	sessionID  string
	operation  string
	generation uint64
	expiresAt  time.Time
	status     string
	redeemedAt time.Time
}

// userState holds the current device generation and all challenges for a user.
type userState struct {
	generation uint64
	challenges map[string]*challenge
}

// Service is an in-memory session step-up authentication component.
//
// A challenge binds a login session, the second-factor device generation
// active at creation time, and a specific target operation. Rebinding the
// second-factor device bumps the generation and invalidates every challenge
// that has not been consumed, so no login session can cross the upgrade
// boundary while the device changes.
//
// All methods are safe for concurrent use. The whole service serializes on a
// single mutex; state transitions are performed under the lock so
// verification, rebind and redemption cannot interleave.
type Service struct {
	mu       sync.Mutex
	clock    Clock
	verifier FactorVerifier
	ttl      time.Duration
	users    map[string]*userState

	// OnBeforeRedeem, when non-nil, is invoked while holding the service lock,
	// immediately before a receipt redemption is finalized. It exists for
	// tests that need to interleave clock movement or a rebind against
	// redemption; production code leaves it nil.
	OnBeforeRedeem func(receiptID string)
}

// NewService creates a step-up service. ttl bounds the lifetime of every
// challenge and the receipt it produces.
func NewService(clock Clock, verifier FactorVerifier, ttl time.Duration) *Service {
	if clock == nil {
		clock = SystemClock{}
	}
	if verifier == nil {
		panic("stepup: nil FactorVerifier")
	}
	if ttl <= 0 {
		panic("stepup: ttl must be positive")
	}
	return &Service{
		clock:    clock,
		verifier: verifier,
		ttl:      ttl,
		users:    make(map[string]*userState),
	}
}

func (s *Service) stateFor(userID string) *userState {
	st := s.users[userID]
	if st == nil {
		st = &userState{challenges: make(map[string]*challenge)}
		s.users[userID] = st
	}
	return st
}

// Generation returns the user's current second-factor device generation. The
// generation starts at zero and is incremented by every successful RebindDevice.
func (s *Service) Generation(userID string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateFor(userID).generation
}

// BeginChallenge creates a pending step-up challenge bound to the login
// session, the user's current device generation and the target operation, and
// returns its opaque id.
func (s *Service) BeginChallenge(userID, sessionID, operation string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.stateFor(userID)
	ch := &challenge{
		id:         newID(),
		userID:     userID,
		sessionID:  sessionID,
		operation:  operation,
		generation: st.generation,
		expiresAt:  s.clock.Now().Add(s.ttl),
		status:     statusPending,
	}
	st.challenges[ch.id] = ch
	return ch.id
}

// SubmitFactor checks the second-factor code for a pending challenge. On
// success the challenge moves to verified and a one-time receipt id is
// returned. The receipt stays bound to the same session, device generation,
// operation and expiry as the challenge.
func (s *Service) SubmitFactor(challengeID, sessionID, code string) (receiptID string, err error) {
	// Validate inputs under the lock, verify the factor without the lock.
	s.mu.Lock()
	ch, err := s.liveChallenge(challengeID, sessionID)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if !s.verifier.Verify(ch.userID, code) {
		return "", ErrInvalidFactor
	}

	// Re-check under the lock: a rebind or expiry may have happened while the
	// factor check was running.
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.liveChallenge(challengeID, sessionID); err != nil {
		return "", err
	}
	ch.status = statusVerified
	return ch.id, nil
}

// liveChallenge resolves a challenge and enforces every binding rule. The
// caller must hold s.mu.
func (s *Service) liveChallenge(id, sessionID string) (*challenge, error) {
	ch := s.findChallenge(id)
	if ch == nil {
		return nil, ErrNotFound
	}
	if ch.status == statusRedeemed {
		return nil, ErrAlreadyRedeemed
	}
	if !s.clock.Now().Before(ch.expiresAt) {
		return nil, ErrChallengeExpired
	}
	st := s.stateFor(ch.userID)
	if ch.generation != st.generation {
		return nil, ErrGenerationMismatch
	}
	if ch.sessionID != sessionID {
		return nil, ErrSessionMismatch
	}
	// Defense in depth: a rebind always bumps the generation above, but the
	// explicit invalidated state blocks the challenge even if generations ever
	// compared equal again.
	if ch.status == statusInvalidated {
		return nil, ErrChallengeInvalid
	}
	return ch, nil
}

func (s *Service) findChallenge(id string) *challenge {
	for _, st := range s.users {
		if ch := st.challenges[id]; ch != nil {
			return ch
		}
	}
	return nil
}

// Redeem consumes a verified receipt for exactly the target operation. A
// receipt may be redeemed at most once, only by the session that created the
// challenge, only while the device generation is unchanged, and only before
// expiry. On success the receipt is atomically marked redeemed.
func (s *Service) Redeem(receiptID, sessionID, operation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := s.findChallenge(receiptID)
	if ch == nil {
		return ErrNotFound
	}
	if ch.status == statusRedeemed {
		return ErrAlreadyRedeemed
	}
	if ch.status == statusPending {
		return ErrChallengeInvalid
	}
	if !s.clock.Now().Before(ch.expiresAt) {
		return ErrChallengeExpired
	}
	st := s.stateFor(ch.userID)
	if ch.generation != st.generation {
		return ErrGenerationMismatch
	}
	if ch.sessionID != sessionID {
		return ErrSessionMismatch
	}
	if ch.operation != operation {
		return ErrOperationMismatch
	}
	if ch.status == statusInvalidated {
		return ErrChallengeInvalid
	}

	if s.OnBeforeRedeem != nil {
		s.OnBeforeRedeem(receiptID)
	}

	// Re-evaluate every rule after the barrier callback, which may advance the
	// clock or trigger a RebindDevice on the same goroutine.
	if ch.status == statusRedeemed {
		return ErrAlreadyRedeemed
	}
	if !s.clock.Now().Before(ch.expiresAt) {
		return ErrChallengeExpired
	}
	if ch.generation != st.generation {
		return ErrGenerationMismatch
	}
	if ch.status == statusInvalidated {
		return ErrChallengeInvalid
	}

	ch.status = statusRedeemed
	ch.redeemedAt = s.clock.Now()
	return nil
}

// RebindDevice replaces the user's second-factor device. The device
// generation is incremented and every challenge that has not been fully
// redeemed is invalidated, including verified receipts waiting to be
// exchanged. Already-redeemed receipts are historical records and are left
// untouched, so completed operations remain valid.
func (s *Service) RebindDevice(userID string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.stateFor(userID)
	st.generation++
	for _, ch := range st.challenges {
		if ch.status != statusRedeemed {
			ch.status = statusInvalidated
		}
	}
	return st.generation
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
