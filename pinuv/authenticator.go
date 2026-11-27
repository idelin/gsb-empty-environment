package pinuv

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

const (
	// MaxPINRetries is the total PIN retry budget (CTAP 2.1 maximum).
	MaxPINRetries = 8
	// MaxConsecutivePINFailures is the per-power-cycle consecutive
	// failure limit after which a reboot is required before any further
	// PIN attempt is even evaluated.
	MaxConsecutivePINFailures = 3
	// TokenLength is the byte length of a freshly issued pinUvAuthToken.
	TokenLength = 32
	// DefaultTokenTTL bounds token usability; an expired token is
	// discarded and must be re-obtained.
	DefaultTokenTTL = 10 * time.Minute
)

// Permission is a pinUvAuthToken permission bit (CTAP 2.1 §6.9.2).
type Permission byte

const (
	PermMakeCredential Permission = 0x01 // mc
	PermGetAssertion   Permission = 0x02 // ga
	PermCredentialMgmt Permission = 0x04 // cm
	PermBioEnrollment  Permission = 0x08 // be
	PermLargeBlobWrite Permission = 0x10 // lbw
)

var (
	ErrPINNotSet          = errors.New("pinuv: PIN not set")
	ErrPINInvalid         = errors.New("pinuv: PIN invalid")
	ErrPINBlocked         = errors.New("pinuv: PIN retries exhausted; hardware reset required")
	ErrPINAuthBlocked     = errors.New("pinuv: too many consecutive failures this power cycle; reboot required")
	ErrNoKeyAgreement     = errors.New("pinuv: getKeyAgreement has not been called")
	ErrTokenInvalid       = errors.New("pinuv: pinUvAuthToken unknown or invalidated")
	ErrTokenExpired       = errors.New("pinuv: pinUvAuthToken expired")
	ErrPermissionDenied   = errors.New("pinuv: requested operation outside token permissions")
	ErrRpIDMismatch       = errors.New("pinuv: token rpId does not match requested rpId")
	ErrInvalidPINHashAuth = errors.New("pinuv: pinUvAuthParam verification failed")
)

// PersistentState is the authenticator state that must survive power
// loss. The host's Store must commit Save calls to non-volatile memory
// before returning; the retry counter is decremented and persisted
// BEFORE the PIN is compared, so a power cut can never roll the
// counter back to grant extra attempts.
type PersistentState struct {
	PINHash []byte // LEFT(SHA-256(pin), 16)
	Retries int
}

// Store abstracts the authenticator's non-volatile storage.
type Store interface {
	Load() (PersistentState, error)
	Save(PersistentState) error
}

// MemStore is an in-memory Store suitable for tests and simulation.
type MemStore struct {
	state PersistentState
}

// NewMemStore returns a MemStore with no PIN and full retries.
func NewMemStore() *MemStore {
	return &MemStore{state: PersistentState{Retries: MaxPINRetries}}
}

func (m *MemStore) Load() (PersistentState, error) {
	s := m.state
	s.PINHash = append([]byte(nil), m.state.PINHash...)
	return s, nil
}

func (m *MemStore) Save(s PersistentState) error {
	m.state = s
	m.state.PINHash = append([]byte(nil), s.PINHash...)
	return nil
}

// PINHash16 derives the stored PIN hash LEFT(SHA-256(pin), 16).
func PINHash16(pin string) []byte {
	sum := sha256.Sum256([]byte(pin))
	return sum[:16]
}

// Token is an issued pinUvAuthToken with its permission scope.
type Token struct {
	Value       []byte
	Permissions Permission
	RpID        string // exact rpId the token is bound to ("" if unbound)
	IssuedAt    time.Time
}

// Authenticator is the CTAP 2.1 clientPin state machine for PIN/UV Auth
// Protocol Two.
type Authenticator struct {
	store               Store
	state               PersistentState
	consecutiveFailures int
	key                 *ecdh.PrivateKey // current key agreement; rotated by GetKeyAgreement
	token               *Token           // at most one live token
	tokenTTL            time.Duration
	now                 func() time.Time
}

// NewAuthenticator loads persistent state from store. tokenTTL <= 0
// selects DefaultTokenTTL.
func NewAuthenticator(store Store, tokenTTL time.Duration) (*Authenticator, error) {
	state, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("pinuv: load state: %w", err)
	}
	if tokenTTL <= 0 {
		tokenTTL = DefaultTokenTTL
	}
	return &Authenticator{store: store, state: state, tokenTTL: tokenTTL, now: time.Now}, nil
}

// persist commits the in-memory persistent state to non-volatile
// storage before any secret-dependent comparison runs.
func (a *Authenticator) persist() error {
	if err := a.store.Save(a.state); err != nil {
		return fmt.Errorf("pinuv: persist state: %w", err)
	}
	return nil
}

// SetPIN stores LEFT(SHA-256(pin), 16) as the reference PIN hash.
func (a *Authenticator) SetPIN(pin string) error {
	a.state.PINHash = PINHash16(pin)
	return a.persist()
}

// Retries reports the persisted remaining PIN retries.
func (a *Authenticator) Retries() int { return a.state.Retries }

// GetKeyAgreement rotates the ECDH key pair and returns the new
// uncompressed public point. Rotating the key invalidates any live
// pinUvAuthToken, as required to prevent token reuse across sessions.
func (a *Authenticator) GetKeyAgreement() ([]byte, error) {
	priv, pub, err := GenerateKeyAgreement()
	if err != nil {
		return nil, err
	}
	a.key = priv
	a.token = nil // key rotation invalidates outstanding tokens
	return pub, nil
}

// GetPinUvToken implements getPinUvAuthTokenFromPin for Protocol Two.
//
// Anti-brute-force ordering is strict: the persisted retry counter is
// decremented and committed to non-volatile storage BEFORE the PIN is
// compared, so cutting power after observing a failure can never replay
// the attempt. Only on success is the counter restored to full and a
// fresh random token issued.
func (a *Authenticator) GetPinUvToken(peerPub, pinHashEnc, pinUvAuthParam []byte, perms Permission, rpID string) (*Token, error) {
	if len(a.state.PINHash) == 0 {
		return nil, ErrPINNotSet
	}
	if a.state.Retries <= 0 {
		return nil, ErrPINBlocked
	}
	if a.consecutiveFailures >= MaxConsecutivePINFailures {
		return nil, ErrPINAuthBlocked
	}
	if a.key == nil {
		return nil, ErrNoKeyAgreement
	}

	// 1. Decrement and persist the retry counter FIRST. A power cut
	//    anywhere after this point still records the consumed attempt.
	a.state.Retries--
	if err := a.persist(); err != nil {
		return nil, err
	}

	// 2. Derive Protocol Two keys for this key agreement.
	secret, err := SharedSecret(a.key, peerPub)
	if err != nil {
		a.registerFailure()
		return nil, err
	}
	proto, err := NewProtocolV2(secret)
	if err != nil {
		a.registerFailure()
		return nil, err
	}

	// 3. Verify pinUvAuthParam = HMAC(hmacKey, pinHashEnc), full 32 bytes.
	if !proto.Verify(pinHashEnc, pinUvAuthParam) {
		a.registerFailure()
		return nil, ErrInvalidPINHashAuth
	}

	// 4. Decrypt the PIN hash and compare against the stored reference.
	pinHash, err := proto.Decrypt(pinHashEnc)
	if err != nil {
		a.registerFailure()
		return nil, ErrPINInvalid
	}
	if !hmac.Equal(pinHash, a.state.PINHash) {
		a.registerFailure()
		return nil, ErrPINInvalid
	}

	// 5. Success: restore the full retry budget, clear the consecutive
	//    failure counter, and issue a brand-new random token.
	a.state.Retries = MaxPINRetries
	if err := a.persist(); err != nil {
		return nil, err
	}
	a.consecutiveFailures = 0

	value := make([]byte, TokenLength)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("pinuv: token generation: %w", err)
	}
	a.token = &Token{
		Value:       value,
		Permissions: perms,
		RpID:        rpID,
		IssuedAt:    a.now(),
	}
	return a.token, nil
}

func (a *Authenticator) registerFailure() {
	a.consecutiveFailures++
}

// Consume authorizes an operation (mc/ga/cm/be/lbw) against a presented
// pinUvAuthToken. It enforces, in order: token identity (constant-time),
// freshness, permission-bit containment, and exact rpId equality for
// rpId-bound tokens.
func (a *Authenticator) Consume(tokenValue []byte, required Permission, rpID string) error {
	t := a.token
	if t == nil || len(tokenValue) != TokenLength || !hmac.Equal(t.Value, tokenValue) {
		return ErrTokenInvalid
	}
	if a.now().Sub(t.IssuedAt) > a.tokenTTL {
		a.token = nil
		return ErrTokenExpired
	}
	if t.Permissions&required != required {
		return ErrPermissionDenied
	}
	if t.RpID != "" && t.RpID != rpID {
		return ErrRpIDMismatch
	}
	return nil
}

// Invalidate discards any live token (e.g. on transport reset).
func (a *Authenticator) Invalidate() { a.token = nil }

// Reboot simulates a power cycle: the consecutive-failure counter and
// all volatile session state (key agreement, token) are cleared, while
// the persisted retry counter is untouched.
func (a *Authenticator) Reboot() {
	a.consecutiveFailures = 0
	a.key = nil
	a.token = nil
}

// HardReset models a factory reset: the retry budget is restored and
// all session state is cleared. This is the only recovery from
// ErrPINBlocked.
func (a *Authenticator) HardReset() error {
	a.state.Retries = MaxPINRetries
	a.consecutiveFailures = 0
	a.key = nil
	a.token = nil
	return a.persist()
}
