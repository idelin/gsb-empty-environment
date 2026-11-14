package netsec

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	defaultProtocolVersion uint16 = 1
	defaultCookieTTL              = 5 * time.Minute
	defaultMaxSessions            = 1 << 14
)

// Config configures a KDC-frontend FAST session manager.
type Config struct {
	// FrontendPartition is a stable name for this KDC frontend partition. It is
	// authenticated as AEAD associated data and therefore must be identical
	// across every frontend that opens the same cookie.
	FrontendPartition string

	// KDCInstance names the KDC instance. It is bound into every session and
	// must match the Keyring instance.
	KDCInstance string

	// ProtocolVersion is bound as AEAD associated data and in the cookie
	// payload. Zero selects the package default.
	ProtocolVersion uint16

	// Keyring supplies the active cookie key generation.
	Keyring *Keyring

	// FinishedVerifier authenticates the finished checksum using the armor
	// ticket session. It is required.
	FinishedVerifier FinishedVerifier

	// CookieTTL controls the absolute lifetime of newly created states. Zero
	// selects the package default. Advancing an existing state never extends
	// the original absolute expiry.
	CookieTTL time.Duration

	// Clock supports deterministic tests. Nil selects time.Now.
	Clock func() time.Time

	// Rand supports deterministic tests. Nil selects crypto/rand.Reader.
	Rand io.Reader

	// MaxSessions bounds retained frontend state. Zero selects the package
	// default. Expired sessions are opportunistically removed before rejecting
	// an insertion due to capacity.
	MaxSessions int
}

// Cookie is an opaque AEAD-protected FAST cookie. Its contents are available
// only to a Manager and must not be interpreted by callers.
type Cookie []byte

// Bytes returns a defensive copy of the opaque cookie bytes.
func (c Cookie) Bytes() []byte {
	return cloneBytes(c)
}

// Manager owns the frontend-side FAST session state and performs atomic
// transitions. It is safe for concurrent use.
type Manager struct {
	keyring         *Keyring
	verifier        FinishedVerifier
	partition       string
	kdcInstance     string
	protocolVersion uint16
	cookieTTL       time.Duration
	clock           func() time.Time
	random          io.Reader
	maxSessions     int

	mu       sync.Mutex
	sessions map[[cookieHandleSize]byte]sessionState
}

// NewManager validates configuration and creates an empty session manager.
func NewManager(config Config) (*Manager, error) {
	if config.FrontendPartition == "" || len(config.FrontendPartition) > 2048 {
		return nil, fmt.Errorf("%w: frontend partition is required", ErrInvalidConfiguration)
	}
	if config.KDCInstance == "" || len(config.KDCInstance) > 2048 {
		return nil, fmt.Errorf("%w: KDC instance is required", ErrInvalidConfiguration)
	}
	if config.Keyring == nil {
		return nil, fmt.Errorf("%w: keyring is required", ErrInvalidConfiguration)
	}
	if config.Keyring.Instance() != config.KDCInstance {
		return nil, fmt.Errorf("%w: keyring instance does not match KDC instance", ErrInvalidConfiguration)
	}
	if config.FinishedVerifier == nil {
		return nil, fmt.Errorf("%w: finished verifier is required", ErrInvalidConfiguration)
	}
	if config.CookieTTL < 0 {
		return nil, fmt.Errorf("%w: cookie TTL must not be negative", ErrInvalidConfiguration)
	}
	if config.MaxSessions < 0 {
		return nil, fmt.Errorf("%w: maximum sessions must not be negative", ErrInvalidConfiguration)
	}

	protocolVersion := config.ProtocolVersion
	if protocolVersion == 0 {
		protocolVersion = defaultProtocolVersion
	}
	cookieTTL := config.CookieTTL
	if cookieTTL == 0 {
		cookieTTL = defaultCookieTTL
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	random := config.Rand
	if random == nil {
		random = rand.Reader
	}
	maxSessions := config.MaxSessions
	if maxSessions == 0 {
		maxSessions = defaultMaxSessions
	}

	if _, _, ok := config.Keyring.activeKey(); !ok {
		return nil, fmt.Errorf("%w: keyring has no active key", ErrInvalidConfiguration)
	}

	return &Manager{
		keyring:         config.Keyring,
		verifier:        config.FinishedVerifier,
		partition:       config.FrontendPartition,
		kdcInstance:     config.KDCInstance,
		protocolVersion: protocolVersion,
		cookieTTL:       cookieTTL,
		clock:           clock,
		random:          random,
		maxSessions:     maxSessions,
		sessions:        make(map[[cookieHandleSize]byte]sessionState),
	}, nil
}

// Begin validates a parsed first request and creates a fresh state with a
// pending pre-authentication phase. Unknown and encrypted input fields are not
// copied into the state or cookie.
func (m *Manager) Begin(ctx context.Context, req Request) (Cookie, error) {
	req = req.clone()
	if !req.validForVerification() {
		return nil, ErrSessionRejected
	}
	if err := m.verifyFinished(ctx, req); err != nil {
		return nil, err
	}

	nonce, err := m.newNonce()
	if err != nil {
		return nil, ErrSessionRejected
	}
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	generation, key, ok := m.keyring.activeKey()
	if !ok {
		return nil, ErrSessionRejected
	}
	if err := m.ensureCapacityLocked(now); err != nil {
		return nil, err
	}
	handle, err := m.newHandleLocked(nil)
	if err != nil {
		return nil, err
	}

	state := sessionState{
		protocolVersion: m.protocolVersion,
		handle:          handle,
		phase:           PhasePreAuthPending,
		expiresAt:       now.Add(m.cookieTTL).UnixNano(),
		binding: sessionBinding{
			requestBinding: bindingFromRequest(req),
			kdcInstance:    m.kdcInstance,
			keyGeneration:  generation,
		},
	}
	cookie, err := m.sealStateLocked(state, key, nonce)
	if err != nil {
		return nil, ErrSessionRejected
	}
	m.sessions[handle] = state
	return cookie, nil
}

// Issue is an alias for Begin.
func (m *Manager) Issue(ctx context.Context, req Request) (Cookie, error) {
	return m.Begin(ctx, req)
}

// BeginParsed accepts the ASReq or TGSReq strict-parser view directly.
func (m *Manager) BeginParsed(ctx context.Context, parsed ParsedRequest) (Cookie, error) {
	switch req := parsed.(type) {
	case nil:
		return nil, ErrSessionRejected
	case ASReq:
		return m.BeginAS(ctx, req)
	case *ASReq:
		if req == nil {
			return nil, ErrSessionRejected
		}
		return m.BeginAS(ctx, *req)
	case TGSReq:
		return m.BeginTGS(ctx, req)
	case *TGSReq:
		if req == nil {
			return nil, ErrSessionRejected
		}
		return m.BeginTGS(ctx, *req)
	default:
		return m.Begin(ctx, parsed.ToRequest())
	}
}

// BeginAS accepts a parsed AS-REQ view.
func (m *Manager) BeginAS(ctx context.Context, req ASReq) (Cookie, error) {
	return m.Begin(ctx, req.ToRequest())
}

// BeginTGS accepts a parsed TGS-REQ view.
func (m *Manager) BeginTGS(ctx context.Context, req TGSReq) (Cookie, error) {
	return m.Begin(ctx, req.ToRequest())
}

// Advance verifies a request against an existing state and atomically replaces
// that state with a fresh-handle cookie in the requested phase. Phases are
// monotonic; the same phase may be used for another pre-authentication round.
// The original absolute expiry is preserved.
func (m *Manager) Advance(ctx context.Context, cookie Cookie, req Request, next Phase) (Cookie, error) {
	req = req.clone()
	if !next.valid() || !req.validForVerification() {
		return nil, ErrSessionRejected
	}
	if err := m.verifyFinished(ctx, req); err != nil {
		return nil, err
	}

	nonce, err := m.newNonce()
	if err != nil {
		return nil, ErrSessionRejected
	}
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	state, key, err := m.openAndMatchLocked(cookie, req, now)
	if err != nil {
		return nil, err
	}
	if next.rank() < state.phase.rank() {
		return nil, ErrSessionRejected
	}

	handle, err := m.newHandleLocked(&state.handle)
	if err != nil {
		return nil, err
	}
	newState := state
	newState.handle = handle
	newState.phase = next

	newCookie, err := m.sealStateLocked(newState, key, nonce)
	if err != nil {
		return nil, ErrSessionRejected
	}
	delete(m.sessions, state.handle)
	m.sessions[handle] = newState
	return newCookie, nil
}

// Validate verifies all request bindings without consuming the state and
// returns its current phase. A concurrent Consume may still invalidate the
// state immediately after Validate returns.
func (m *Manager) Validate(ctx context.Context, cookie Cookie, req Request) (Phase, error) {
	req = req.clone()
	if !req.validForVerification() {
		return 0, ErrSessionRejected
	}
	if err := m.verifyFinished(ctx, req); err != nil {
		return 0, err
	}

	now := m.clock()
	m.mu.Lock()
	defer m.mu.Unlock()
	state, _, err := m.openAndMatchLocked(cookie, req, now)
	if err != nil {
		return 0, err
	}
	return state.phase, nil
}

// Check is an alias for Validate.
func (m *Manager) Check(ctx context.Context, cookie Cookie, req Request) (Phase, error) {
	return m.Validate(ctx, cookie, req)
}

// Consume verifies a request and atomically removes the state. Exactly one
// concurrent Consume of the same cookie can succeed.
func (m *Manager) Consume(ctx context.Context, cookie Cookie, req Request) error {
	req = req.clone()
	if !req.validForVerification() {
		return ErrSessionRejected
	}
	if err := m.verifyFinished(ctx, req); err != nil {
		return err
	}

	now := m.clock()
	m.mu.Lock()
	defer m.mu.Unlock()
	state, _, err := m.openAndMatchLocked(cookie, req, now)
	if err != nil {
		return err
	}
	delete(m.sessions, state.handle)
	return nil
}

// Refer atomically consumes the source state and creates a new, fresh-handle
// state for a cross-realm referral. The next request binding is supplied
// explicitly: no handle, phase, expiry, nonce, armor binding, or cookie bytes
// are inherited from the source cookie. Referrals are TGS-only and must keep
// the same client while changing the target realm.
func (m *Manager) Refer(ctx context.Context, cookie Cookie, current Request, next Request) (Cookie, error) {
	current = current.clone()
	next = next.clone()
	if !current.validForVerification() || !next.validForBinding() {
		return nil, ErrSessionRejected
	}
	if err := m.verifyFinished(ctx, current); err != nil {
		return nil, err
	}

	nonce, err := m.newNonce()
	if err != nil {
		return nil, ErrSessionRejected
	}
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	source, key, err := m.openAndMatchLocked(cookie, current, now)
	if err != nil {
		return nil, err
	}
	nextBinding := bindingFromRequest(next)
	if source.binding.kind != RequestKindTGS ||
		nextBinding.kind != RequestKindTGS ||
		!principalsEqual(source.binding.client, nextBinding.client) ||
		nextBinding.targetRealm == source.binding.targetRealm {
		return nil, ErrSessionRejected
	}

	handle, err := m.newHandleLocked(&source.handle)
	if err != nil {
		return nil, err
	}
	newState := sessionState{
		protocolVersion: m.protocolVersion,
		handle:          handle,
		phase:           PhasePreAuthPending,
		expiresAt:       now.Add(m.cookieTTL).UnixNano(),
		binding: sessionBinding{
			requestBinding: nextBinding,
			kdcInstance:    m.kdcInstance,
			keyGeneration:  source.binding.keyGeneration,
		},
	}
	newCookie, err := m.sealStateLocked(newState, key, nonce)
	if err != nil {
		return nil, ErrSessionRejected
	}
	delete(m.sessions, source.handle)
	m.sessions[handle] = newState
	return newCookie, nil
}

// KeyGeneration reports the active cookie generation.
func (m *Manager) KeyGeneration() uint64 {
	return m.keyring.ActiveGeneration()
}

// RotateKey installs and activates a new cookie generation while serializing
// against in-flight Manager operations. Callers that share a Keyring with other
// managers must coordinate rotation at the deployment layer.
func (m *Manager) RotateKey(generation uint64, key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keyring.Rotate(generation, key)
}

// InstallKey stages a future cookie generation without activating it.
func (m *Manager) InstallKey(generation uint64, key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keyring.InstallKey(generation, key)
}

// ActivateKey activates a previously staged cookie generation.
func (m *Manager) ActivateKey(generation uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keyring.Activate(generation)
}

func (m *Manager) verifyFinished(ctx context.Context, req Request) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ErrSessionRejected
	}
	if err := m.verifier.VerifyFinished(ctx, req.clone()); err != nil {
		return ErrSessionRejected
	}
	return nil
}

func (m *Manager) openAndMatchLocked(
	cookie Cookie,
	req Request,
	now time.Time,
) (sessionState, [keySize]byte, error) {
	var zeroKey [keySize]byte

	generation, key, ok := m.keyring.activeKey()
	if !ok {
		return sessionState{}, zeroKey, ErrSessionRejected
	}
	state, err := openCookie(
		cookie,
		key,
		generation,
		m.protocolVersion,
		m.partition,
	)
	if err != nil {
		return sessionState{}, zeroKey, ErrSessionRejected
	}
	stored, ok := m.sessions[state.handle]
	if !ok {
		return sessionState{}, zeroKey, ErrSessionRejected
	}
	if stored.protocolVersion != state.protocolVersion ||
		stored.phase != state.phase ||
		stored.expiresAt != state.expiresAt ||
		!stored.binding.equal(state.binding) ||
		stored.binding.kdcInstance != m.kdcInstance ||
		stored.binding.keyGeneration != generation {
		return sessionState{}, zeroKey, ErrSessionRejected
	}

	request := bindingFromRequest(req)
	if state.binding.outerRealm != request.outerRealm {
		return sessionState{}, zeroKey, ErrSessionRejected
	}
	if !armorTicketsEqual(state.binding.armor, request.armor) {
		return sessionState{}, zeroKey, ErrSessionRejected
	}
	if subtle.ConstantTimeCompare(
		state.binding.bodyDigest[:],
		request.bodyDigest[:],
	) != 1 {
		return sessionState{}, zeroKey, ErrSessionRejected
	}
	if !state.binding.requestBinding.equal(request) {
		return sessionState{}, zeroKey, ErrSessionRejected
	}
	if !state.binding.equal(stored.binding) {
		return sessionState{}, zeroKey, ErrSessionRejected
	}
	if now.UnixNano() >= state.expiresAt {
		return sessionState{}, zeroKey, ErrSessionRejected
	}
	return stored, key, nil
}

func (m *Manager) sealStateLocked(
	state sessionState,
	key [keySize]byte,
	nonce [cookieNonceSize]byte,
) (Cookie, error) {
	payload, err := marshalSessionState(state)
	if err != nil {
		return nil, err
	}
	return sealCookieWithNonce(state, key, m.partition, nonce, payload)
}

func (m *Manager) newHandleLocked(replacing *[cookieHandleSize]byte) ([cookieHandleSize]byte, error) {
	var handle [cookieHandleSize]byte
	for attempt := 0; attempt < 8; attempt++ {
		if _, err := io.ReadFull(m.random, handle[:]); err != nil {
			return handle, ErrSessionRejected
		}
		if replacing != nil && handle == *replacing {
			continue
		}
		if _, exists := m.sessions[handle]; exists {
			continue
		}
		return handle, nil
	}
	return handle, ErrSessionRejected
}

func (m *Manager) newNonce() ([cookieNonceSize]byte, error) {
	var nonce [cookieNonceSize]byte
	_, err := io.ReadFull(m.random, nonce[:])
	return nonce, err
}

func (m *Manager) ensureCapacityLocked(now time.Time) error {
	if len(m.sessions) >= m.maxSessions {
		for handle, state := range m.sessions {
			if now.UnixNano() >= state.expiresAt {
				delete(m.sessions, handle)
			}
		}
	}
	if len(m.sessions) >= m.maxSessions {
		return ErrSessionRejected
	}
	return nil
}
