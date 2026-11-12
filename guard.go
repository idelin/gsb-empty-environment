package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"sync"
	"time"
)

const (
	defaultSessionTTL     = 5 * time.Minute
	defaultFailureLimit   = 3
	transactionIDBytes    = 32
	serverChallengeBytes  = 32
	nonceGenerationTrials = 8
)

// Order is the server-side view of an eSIM order. ConfirmationCode is
// hashed before storage; the plaintext is not retained by the guard.
type Order struct {
	MatchingID       string
	EID              string
	ConfirmationCode string
	Verifier         SignatureVerifier
}

// Challenge is the one-time authentication material returned to the ES9+
// session.
type Challenge struct {
	TransactionID   string
	ServerChallenge []byte
	ExpiresAt       time.Time
}

// AuthenticationResponse is the eUICC response presented to the SM-DP+.
// Signature must be over AuthenticationPayload(TransactionID, ServerChallenge).
type AuthenticationResponse struct {
	TransactionID   string
	ServerChallenge []byte
	EID             string
	Signature       []byte
}

// ProfileRequest is an ES9+ GetBoundProfilePackage request.
type ProfileRequest struct {
	TransactionID    string
	MatchingID       string
	EID              string
	ConfirmationCode string
}

// Option configures a Guard.
type Option func(*Guard)

// WithSessionTTL changes how long a transactionId and server challenge remain
// usable. Non-positive values are ignored.
func WithSessionTTL(ttl time.Duration) Option {
	return func(guard *Guard) {
		if ttl > 0 {
			guard.sessionTTL = ttl
		}
	}
}

// WithFailureLimit changes the number of wrong confirmation codes allowed
// before the order is locked. A limit of 3 locks on the fourth wrong code.
// Non-positive values are ignored.
func WithFailureLimit(limit int) Option {
	return func(guard *Guard) {
		if limit > 0 {
			guard.failureLimit = limit
		}
	}
}

// WithClock injects a clock, primarily for expiry tests.
func WithClock(now func() time.Time) Option {
	return func(guard *Guard) {
		if now != nil {
			guard.now = now
		}
	}
}

// WithRandom injects the randomness source used for transactionIds and
// challenges, primarily for deterministic tests.
func WithRandom(reader io.Reader) Option {
	return func(guard *Guard) {
		if reader != nil {
			guard.random = reader
		}
	}
}

// Guard enforces the ES9+ authentication and profile-download invariants.
// It is safe for concurrent use.
type Guard struct {
	mu sync.Mutex

	sessionTTL   time.Duration
	failureLimit int
	now          func() time.Time
	random       io.Reader

	orders   map[string]*order
	sessions map[string]*session
}

type order struct {
	matchingID    string
	eid           string
	confirmation  [sha256.Size]byte
	verifier      SignatureVerifier
	failures      int
	locked        bool
	consumed      bool
	consumedByEID string
}

type sessionState uint8

const (
	sessionPending sessionState = iota
	sessionClaimed
	sessionAuthenticated
	sessionConsumed
)

type session struct {
	transactionID string
	challenge     []byte
	expiresAt     time.Time
	order         *order
	state         sessionState
	superseded    bool
}

// NewGuard creates a guard. The defaults are a five-minute session TTL and a
// confirmation-code limit of three allowed failures.
func NewGuard(options ...Option) *Guard {
	guard := &Guard{
		sessionTTL:   defaultSessionTTL,
		failureLimit: defaultFailureLimit,
		now:          time.Now,
		random:       rand.Reader,
		orders:       make(map[string]*order),
		sessions:     make(map[string]*session),
	}
	for _, option := range options {
		if option != nil {
			option(guard)
		}
	}
	return guard
}

// RegisterOrder adds an order and its eUICC signature verifier. The
// confirmation code is reduced to SHA256(confirmationCode) before storage.
func (guard *Guard) RegisterOrder(candidate Order) error {
	if candidate.MatchingID == "" || candidate.EID == "" || candidate.Verifier == nil {
		return ErrInvalidOrder
	}

	guard.mu.Lock()
	defer guard.mu.Unlock()
	if _, exists := guard.orders[candidate.MatchingID]; exists {
		return ErrOrderExists
	}
	inner := sha256.Sum256([]byte(candidate.ConfirmationCode))
	guard.orders[candidate.MatchingID] = &order{
		matchingID:   candidate.MatchingID,
		eid:          candidate.EID,
		confirmation: inner,
		verifier:     candidate.Verifier,
	}
	return nil
}

// InitiateAuthentication issues a fresh transactionId and server challenge
// for an unconsumed, unlocked matchingId. Any older challenge for the same
// order is superseded.
func (guard *Guard) InitiateAuthentication(matchingID string) (Challenge, error) {
	guard.mu.Lock()
	defer guard.mu.Unlock()

	now := guard.now()
	guard.expireLocked(now)

	ord := guard.orders[matchingID]
	if ord == nil {
		return Challenge{}, ErrOrderNotFound
	}
	if ord.consumed {
		return Challenge{}, ErrMatchingIDConsumed
	}
	if ord.locked {
		return Challenge{}, ErrOrderLocked
	}

	transactionID, challenge, err := guard.newChallengeLocked()
	if err != nil {
		return Challenge{}, err
	}

	for _, existing := range guard.sessions {
		if existing.order == ord &&
			existing.state != sessionConsumed {
			existing.superseded = true
		}
	}

	expiresAt := now.Add(guard.sessionTTL)
	guard.sessions[transactionID] = &session{
		transactionID: transactionID,
		challenge:     challenge,
		expiresAt:     expiresAt,
		order:         ord,
		state:         sessionPending,
	}

	return Challenge{
		TransactionID:   transactionID,
		ServerChallenge: append([]byte(nil), challenge...),
		ExpiresAt:       expiresAt,
	}, nil
}

// AuthenticateClient consumes a session's one-time authentication attempt.
// The attempt is consumed before signature verification, so a failed or
// replayed response cannot be retried against the same transactionId.
func (guard *Guard) AuthenticateClient(response AuthenticationResponse) error {
	guard.mu.Lock()

	now := guard.now()
	guard.expireLocked(now)

	sess := guard.sessions[response.TransactionID]
	if sess == nil {
		guard.mu.Unlock()
		return ErrSessionNotFound
	}
	if !now.Before(sess.expiresAt) {
		guard.mu.Unlock()
		return ErrSessionExpired
	}
	if sess.state != sessionPending {
		guard.mu.Unlock()
		return ErrAuthenticationReplay
	}
	if sess.superseded {
		guard.mu.Unlock()
		return ErrSessionSuperseded
	}
	if sess.order.locked {
		guard.mu.Unlock()
		return ErrOrderLocked
	}
	if sess.order.consumed {
		guard.mu.Unlock()
		return ErrMatchingIDConsumed
	}

	// Consume the response slot before doing any cryptographic work.
	sess.state = sessionClaimed
	challenge := append([]byte(nil), sess.challenge...)
	transactionID := sess.transactionID
	expectedEID := sess.order.eid
	verifier := sess.order.verifier
	payload := AuthenticationPayload(transactionID, challenge)
	guard.mu.Unlock()

	if !constantTimeEqual(challenge, response.ServerChallenge) {
		return ErrChallengeMismatch
	}
	if response.EID != expectedEID {
		return ErrEIDMismatch
	}
	if verifier == nil || verifier.Verify(payload, response.Signature) != nil {
		return ErrSignatureInvalid
	}

	guard.mu.Lock()
	defer guard.mu.Unlock()

	now = guard.now()
	if !now.Before(sess.expiresAt) {
		return ErrSessionExpired
	}
	if sess.superseded {
		return ErrSessionSuperseded
	}
	if sess.state != sessionClaimed {
		return ErrAuthenticationReplay
	}
	if sess.order.locked {
		return ErrOrderLocked
	}
	// The session's order can only become consumed after this state check by
	// another authenticated session, which is not possible.
	sess.state = sessionAuthenticated
	return nil
}

// Authenticate is an alias for AuthenticateClient.
func (guard *Guard) Authenticate(response AuthenticationResponse) error {
	return guard.AuthenticateClient(response)
}

// GetBoundProfilePackage validates the authenticated transaction, matches the
// matchingId and EID to the session's order, and checks
//
//	SHA256(SHA256(confirmationCode) || transactionId).
//
// On success the matchingId is consumed and permanently bound to the EID.
// Wrong confirmation codes count toward the order's failure limit.
func (guard *Guard) GetBoundProfilePackage(request ProfileRequest) error {
	guard.mu.Lock()
	defer guard.mu.Unlock()

	now := guard.now()
	guard.expireLocked(now)

	sess := guard.sessions[request.TransactionID]
	if sess == nil {
		return ErrSessionNotFound
	}
	if !now.Before(sess.expiresAt) {
		return ErrSessionExpired
	}
	if sess.state == sessionConsumed {
		return ErrMatchingIDConsumed
	}
	if sess.superseded {
		return ErrSessionSuperseded
	}
	if sess.state != sessionAuthenticated {
		return ErrAuthenticationRequired
	}

	ord := sess.order
	if ord.locked {
		return ErrOrderLocked
	}
	if ord.consumed {
		return ErrMatchingIDConsumed
	}
	if request.MatchingID != ord.matchingID {
		return ErrMatchingIDMismatch
	}
	if request.EID != ord.eid || request.EID != sess.order.eid {
		return ErrEIDMismatch
	}

	expected := confirmationCodeHashFromInner(ord.confirmation, sess.transactionID)
	actual := ConfirmationCodeHash(request.ConfirmationCode, sess.transactionID)
	if !confirmationHashesEqual(expected, actual) {
		ord.failures++
		if ord.failures > guard.failureLimit {
			ord.locked = true
			return ErrOrderLocked
		}
		return ErrConfirmationCodeInvalid
	}

	ord.consumed = true
	ord.consumedByEID = request.EID
	sess.state = sessionConsumed
	for _, other := range guard.sessions {
		if other.order == ord && other.state == sessionAuthenticated {
			other.superseded = true
		}
	}
	return nil
}

// GetProfile is an alias for GetBoundProfilePackage.
func (guard *Guard) GetProfile(request ProfileRequest) error {
	return guard.GetBoundProfilePackage(request)
}

// MatchingIDBinding reports whether matchingID has been consumed and, if so,
// the EID to which it is bound.
func (guard *Guard) MatchingIDBinding(matchingID string) (eid string, consumed bool, found bool) {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	ord := guard.orders[matchingID]
	if ord == nil {
		return "", false, false
	}
	return ord.consumedByEID, ord.consumed, true
}

func (guard *Guard) newChallengeLocked() (string, []byte, error) {
	for range nonceGenerationTrials {
		transactionBytes := make([]byte, transactionIDBytes)
		if _, err := io.ReadFull(guard.random, transactionBytes); err != nil {
			return "", nil, ErrNonceGeneration
		}
		transactionID := hex.EncodeToString(transactionBytes)
		if _, exists := guard.sessions[transactionID]; exists {
			continue
		}

		challenge := make([]byte, serverChallengeBytes)
		if _, err := io.ReadFull(guard.random, challenge); err != nil {
			return "", nil, ErrNonceGeneration
		}
		return transactionID, challenge, nil
	}
	return "", nil, ErrNonceGeneration
}

func (guard *Guard) expireLocked(now time.Time) {
	for transactionID, sess := range guard.sessions {
		// Keep expired sessions long enough to return ErrSessionExpired,
		// then release them.
		if now.After(sess.expiresAt.Add(guard.sessionTTL)) {
			delete(guard.sessions, transactionID)
		}
	}
}

func constantTimeEqual(first, second []byte) bool {
	return len(first) == len(second) && subtle.ConstantTimeCompare(first, second) == 1
}
