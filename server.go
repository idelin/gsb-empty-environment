package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

const (
	DefaultChallengeTTL        = 2 * time.Minute
	DefaultCancelCooldown      = 30 * time.Second
	DefaultHourlyLimit         = 10
	DefaultDenialLockThreshold = 5
	DefaultDenialLockDuration  = 15 * time.Minute
	DefaultTransactionTTL      = 5 * time.Minute
	DefaultSessionTTL          = time.Hour
)

// PushSender delivers a challenge to the account's registered device. A
// non-nil error means delivery failed; the creation still consumes one hourly
// allowance but the challenge never enters Pending.
type PushSender func(Challenge) error

// Config controls the host-side policy. Zero-valued durations and counters
// select the documented defaults.
type Config struct {
	ServiceKey []byte

	ChallengeTTL   time.Duration
	CancelCooldown time.Duration
	TransactionTTL time.Duration
	SessionTTL     time.Duration

	HourlyCreationLimit int
	DenialLockThreshold int
	DenialLockDuration  time.Duration

	Clock Clock
	Rand  io.Reader
}

type challengeRecord struct {
	challenge             Challenge
	passwordVersion       uint64
	accountRiskAtCreation RiskLevel
}

type challengePair struct {
	id      ChallengeID
	browser BrowserSessionID
}

type accountState struct {
	id AccountID

	pending  *ChallengeID
	inFlight *ChallengeID
	active   map[ChallengeID]struct{}

	cooldownUntil MonoTime
	creationTimes []MonoTime

	consecutiveDenials int
	lockedUntil        MonoTime

	risk            RiskLevel
	passwordVersion uint64
}

type deviceRecord struct {
	device Device
}

// Server is an in-memory, concurrency-safe push challenge host. All policy
// counters and state transitions are serialized by mu.
type Server struct {
	mu  sync.Mutex
	cfg Config

	challenges   map[ChallengeID]*challengeRecord
	challengeIDs map[ChallengeID]struct{}
	pairs        map[challengePair]struct{}
	accounts     map[AccountID]*accountState
	devices      map[DeviceID]*deviceRecord
	transactions map[TransactionID]*LoginTransaction
	sessions     map[SessionID]*Session
}

// NewServer validates cfg and constructs a host.
func NewServer(cfg Config) (*Server, error) {
	if len(cfg.ServiceKey) == 0 {
		return nil, ErrServiceKeyRequired
	}
	if cfg.ChallengeTTL < 0 || cfg.CancelCooldown < 0 || cfg.TransactionTTL < 0 || cfg.SessionTTL < 0 {
		return nil, fmt.Errorf("%w: durations must not be negative", ErrInvalidConfig)
	}
	if cfg.HourlyCreationLimit < 0 || cfg.DenialLockThreshold < 0 || cfg.DenialLockDuration < 0 {
		return nil, fmt.Errorf("%w: counters and lock duration must not be negative", ErrInvalidConfig)
	}
	if cfg.ChallengeTTL == 0 {
		cfg.ChallengeTTL = DefaultChallengeTTL
	}
	if cfg.CancelCooldown == 0 {
		cfg.CancelCooldown = DefaultCancelCooldown
	}
	if cfg.TransactionTTL == 0 {
		cfg.TransactionTTL = DefaultTransactionTTL
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = DefaultSessionTTL
	}
	if cfg.HourlyCreationLimit == 0 {
		cfg.HourlyCreationLimit = DefaultHourlyLimit
	}
	if cfg.DenialLockThreshold == 0 {
		cfg.DenialLockThreshold = DefaultDenialLockThreshold
	}
	if cfg.DenialLockDuration == 0 {
		cfg.DenialLockDuration = DefaultDenialLockDuration
	}
	if cfg.Clock == nil {
		cfg.Clock = NewMonotonicClock()
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	cfg.ServiceKey = append([]byte(nil), cfg.ServiceKey...)

	return &Server{
		cfg:          cfg,
		challenges:   make(map[ChallengeID]*challengeRecord),
		challengeIDs: make(map[ChallengeID]struct{}),
		pairs:        make(map[challengePair]struct{}),
		accounts:     make(map[AccountID]*accountState),
		devices:      make(map[DeviceID]*deviceRecord),
		transactions: make(map[TransactionID]*LoginTransaction),
		sessions:     make(map[SessionID]*Session),
	}, nil
}

// MustNewServer is NewServer with panic-on-error semantics.
func MustNewServer(cfg Config) *Server {
	s, err := NewServer(cfg)
	if err != nil {
		panic(err)
	}
	return s
}

// NewServerWithKey is a convenience constructor using defaults.
func NewServerWithKey(serviceKey []byte) *Server {
	return MustNewServer(Config{ServiceKey: serviceKey})
}

func (s *Server) nowLocked() MonoTime { return s.cfg.Clock.Now() }

func (s *Server) accountLocked(id AccountID) *accountState {
	acc := s.accounts[id]
	if acc == nil {
		acc = &accountState{id: id, active: make(map[ChallengeID]struct{})}
		s.accounts[id] = acc
	}
	if acc.active == nil {
		acc.active = make(map[ChallengeID]struct{})
	}
	return acc
}

func (s *Server) randomBytesLocked(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(s.cfg.Rand, buf); err != nil {
		return nil, fmt.Errorf("netsec: random source failed: %w", err)
	}
	return buf, nil
}

func (s *Server) newChallengeIDLocked(browser BrowserSessionID) (ChallengeID, error) {
	for attempt := 0; attempt < 32; attempt++ {
		id, err := NewChallengeID(s.cfg.Rand)
		if err != nil {
			return ChallengeID{}, err
		}
		if id.IsZero() {
			continue
		}
		if _, exists := s.challengeIDs[id]; exists {
			continue
		}
		pair := challengePair{id: id, browser: browser}
		if _, exists := s.pairs[pair]; exists {
			continue
		}
		s.challengeIDs[id] = struct{}{}
		s.pairs[pair] = struct{}{}
		return id, nil
	}
	return ChallengeID{}, fmt.Errorf("%w: could not find a unique 128-bit id", ErrDuplicateChallengeID)
}

func (s *Server) newOpaqueIDLocked(prefix string, size int) (string, error) {
	raw, err := s.randomBytesLocked(size)
	if err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw), nil
}

func validateRequest(req ChallengeRequest) error {
	if normalize(string(req.AccountID)) == "" {
		return fmt.Errorf("%w: account id is required", ErrInvalidConfig)
	}
	if normalize(string(req.BrowserSessionID)) == "" {
		return fmt.Errorf("%w: browser session id is required", ErrInvalidConfig)
	}
	if normalize(string(req.DeviceID)) == "" {
		return fmt.Errorf("%w: device id is required", ErrInvalidConfig)
	}
	if !req.Risk.valid() {
		return ErrInvalidRisk
	}
	if req.TTL < 0 {
		return fmt.Errorf("%w: ttl must not be negative", ErrInvalidConfig)
	}
	return nil
}

func (s *Server) pruneCreationTimesLocked(acc *accountState, now MonoTime) {
	if len(acc.creationTimes) == 0 {
		return
	}
	cutoff := now.Add(-time.Hour)
	first := 0
	for first < len(acc.creationTimes) && !acc.creationTimes[first].After(cutoff) {
		first++
	}
	if first > 0 {
		acc.creationTimes = append([]MonoTime(nil), acc.creationTimes[first:]...)
	}
}

func (s *Server) clearActiveLocked(acc *accountState, id ChallengeID) {
	if acc.pending != nil && *acc.pending == id {
		acc.pending = nil
	}
	if acc.inFlight != nil && *acc.inFlight == id {
		acc.inFlight = nil
	}
	delete(acc.active, id)
}

func (s *Server) expireChallengeLocked(acc *accountState, rec *challengeRecord, now MonoTime) bool {
	if rec == nil || !rec.challenge.State.Active() {
		return false
	}
	if now.Before(rec.challenge.ExpiresAt) {
		return false
	}
	rec.challenge.State = ChallengeExpired
	rec.challenge.Decision = DecisionNone
	s.clearActiveLocked(acc, rec.challenge.ID)
	return true
}

func (s *Server) expireAccountLocked(acc *accountState, now MonoTime) int {
	changed := 0
	ids := make([]ChallengeID, 0, len(acc.active))
	for id := range acc.active {
		ids = append(ids, id)
	}
	for _, id := range ids {
		if s.expireChallengeLocked(acc, s.challenges[id], now) {
			changed++
		}
	}
	return changed
}

func (s *Server) invalidateAccountLocked(acc *accountState, decision Decision) int {
	changed := 0
	for id := range acc.active {
		rec := s.challenges[id]
		if rec == nil {
			continue
		}
		if rec.challenge.State.Active() {
			rec.challenge.State = ChallengeInvalidated
			rec.challenge.Decision = decision
			changed++
		}
	}
	acc.pending = nil
	acc.inFlight = nil
	acc.active = make(map[ChallengeID]struct{})
	return changed
}

// CreateChallenge reserves rate allowance, generates the server-owned binding
// and display code, and optionally invokes a push sender. With no sender the
// challenge is installed directly as Pending. A sender failure leaves a
// SendFailed audit record and no Pending challenge, but the attempt still
// counts toward the hourly limit.
func (s *Server) CreateChallenge(req ChallengeRequest, senders ...PushSender) (*Challenge, error) {
	if len(senders) > 1 {
		return nil, fmt.Errorf("%w: at most one push sender", ErrInvalidConfig)
	}
	if err := validateRequest(req); err != nil {
		return nil, err
	}

	s.mu.Lock()
	now := s.nowLocked()
	acc := s.accountLocked(req.AccountID)
	s.expireAccountLocked(acc, now)
	s.pruneCreationTimesLocked(acc, now)

	dev := s.devices[req.DeviceID]
	if dev == nil {
		s.mu.Unlock()
		return nil, ErrDeviceNotFound
	}
	if dev.device.Revoked {
		s.mu.Unlock()
		return nil, ErrDeviceRevoked
	}
	if dev.device.AccountID != req.AccountID {
		s.mu.Unlock()
		return nil, ErrDeviceAccountMismatch
	}
	if acc.lockedUntil.After(now) {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: until %s", ErrAccountLocked, acc.lockedUntil)
	}
	if acc.pending != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrPendingExists, acc.pending)
	}
	if acc.inFlight != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrChallengeInFlight, acc.inFlight)
	}
	if acc.cooldownUntil.After(now) {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: until %s", ErrCooldown, acc.cooldownUntil)
	}
	if len(acc.creationTimes) >= s.cfg.HourlyCreationLimit {
		s.mu.Unlock()
		return nil, ErrHourlyLimit
	}

	ttl := req.TTL
	if ttl == 0 {
		ttl = s.cfg.ChallengeTTL
	}
	id, err := s.newChallengeIDLocked(req.BrowserSessionID)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}

	// The attempt consumes an hourly allowance before delivery is attempted.
	acc.creationTimes = append(acc.creationTimes, now)

	risk := req.Risk
	if acc.risk > risk {
		risk = acc.risk
	}
	ch := Challenge{
		AccountID:        req.AccountID,
		BrowserSessionID: req.BrowserSessionID,
		DeviceID:         req.DeviceID,
		ID:               id,
		DisplayCode:      DisplayCode(s.cfg.ServiceKey, id, req.RequestDigest),
		RequestDigest:    req.RequestDigest,
		Risk:             risk,
		CreatedAt:        now,
		ExpiresAt:        now.Add(ttl),
		State:            ChallengeSending,
	}
	rec := &challengeRecord{
		challenge:             ch,
		passwordVersion:       acc.passwordVersion,
		accountRiskAtCreation: acc.risk,
	}
	s.challenges[id] = rec
	acc.active[id] = struct{}{}
	acc.inFlight = &id

	var sender PushSender
	if len(senders) == 1 {
		sender = senders[0]
	}
	if sender == nil {
		rec.challenge.State = ChallengePending
		acc.pending = &id
		acc.inFlight = nil
		out := rec.challenge
		s.mu.Unlock()
		return &out, nil
	}

	deliveryCopy := ch
	s.mu.Unlock()

	sendErr := sender(deliveryCopy)

	s.mu.Lock()
	rec = s.challenges[id]
	if rec == nil {
		s.mu.Unlock()
		return nil, ErrChallengeNotFound
	}
	s.expireChallengeLocked(acc, rec, s.nowLocked())

	switch rec.challenge.State {
	case ChallengeSending:
		if sendErr != nil {
			rec.challenge.State = ChallengeSendFailed
			rec.challenge.Decision = DecisionNone
			s.clearActiveLocked(acc, id)
			out := rec.challenge
			s.mu.Unlock()
			return &out, errors.Join(ErrPushSendFailed, sendErr)
		}
		rec.challenge.State = ChallengePending
		acc.pending = &id
		acc.inFlight = nil
		out := rec.challenge
		s.mu.Unlock()
		return &out, nil
	case ChallengeCancelled:
		out := rec.challenge
		s.mu.Unlock()
		if sendErr != nil {
			return &out, fmt.Errorf("%w (push error: %v)", ErrChallengeCancelled, sendErr)
		}
		return &out, ErrChallengeCancelled
	case ChallengeInvalidated:
		out := rec.challenge
		s.mu.Unlock()
		return &out, ErrChallengeInvalidated
	case ChallengeExpired:
		out := rec.challenge
		s.mu.Unlock()
		return &out, ErrChallengeExpired
	default:
		out := rec.challenge
		err := stateError(out.State)
		s.mu.Unlock()
		return &out, err
	}
}

// RequestChallenge is an alias for CreateChallenge.
func (s *Server) RequestChallenge(req ChallengeRequest, senders ...PushSender) (*Challenge, error) {
	return s.CreateChallenge(req, senders...)
}

// NewChallenge is an alias for CreateChallenge.
func (s *Server) NewChallenge(req ChallengeRequest, senders ...PushSender) (*Challenge, error) {
	return s.CreateChallenge(req, senders...)
}

// CancelChallenge explicitly cancels an active challenge. A successful
// cancellation starts the 30-second account cooldown; this is the only way to
// remove a Pending challenge and make room for a replacement.
func (s *Server) CancelChallenge(accountID AccountID, challengeID ChallengeID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowLocked()
	acc := s.accountLocked(accountID)
	s.expireAccountLocked(acc, now)

	rec := s.challenges[challengeID]
	if rec == nil {
		return ErrChallengeNotFound
	}
	if rec.challenge.AccountID != accountID {
		return ErrAccountMismatch
	}
	switch rec.challenge.State {
	case ChallengeCancelled:
		return nil
	case ChallengePending, ChallengeApproved, ChallengeSending:
		rec.challenge.State = ChallengeCancelled
		rec.challenge.Decision = DecisionNone
		s.clearActiveLocked(acc, challengeID)
		acc.cooldownUntil = now.Add(s.cfg.CancelCooldown)
		return nil
	default:
		return fmt.Errorf("%w: state=%s", ErrChallengeNotActive, rec.challenge.State)
	}
}

// Cancel is an alias for CancelChallenge.
func (s *Server) Cancel(accountID AccountID, challengeID ChallengeID) error {
	return s.CancelChallenge(accountID, challengeID)
}

func bindingMatches(c Challenge, r DeviceResponse) bool {
	return r.AccountID == c.AccountID &&
		r.BrowserSessionID == c.BrowserSessionID &&
		r.DeviceID == c.DeviceID &&
		r.ChallengeID == c.ID &&
		r.RequestDigest == c.RequestDigest &&
		r.Risk == c.Risk &&
		r.CreatedAt == c.CreatedAt &&
		r.ExpiresAt == c.ExpiresAt
}

// Respond verifies a device's Ed25519 response and atomically moves the
// challenge once from Pending to Approved or Denied. Approval additionally
// requires the server-derived display code and a device that still belongs to
// the account.
func (s *Server) Respond(resp DeviceResponse) (*Challenge, error) {
	if !resp.Decision.valid() {
		return nil, ErrInvalidDecision
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowLocked()
	rec := s.challenges[resp.ChallengeID]
	if rec == nil {
		return nil, ErrChallengeNotFound
	}
	acc := s.accountLocked(rec.challenge.AccountID)
	s.expireAccountLocked(acc, now)
	s.expireChallengeLocked(acc, rec, now)

	if !bindingMatches(rec.challenge, resp) {
		return nil, ErrBindingMismatch
	}
	if rec.challenge.State != ChallengePending {
		return nil, stateError(rec.challenge.State)
	}

	dev := s.devices[rec.challenge.DeviceID]
	if dev == nil {
		return nil, ErrDeviceNotFound
	}
	if dev.device.Revoked || resp.DeviceID != dev.device.ID {
		return nil, ErrDeviceRevoked
	}
	if dev.device.AccountID != rec.challenge.AccountID || resp.AccountID != dev.device.AccountID {
		return nil, ErrDeviceAccountMismatch
	}
	if acc.passwordVersion != rec.passwordVersion || acc.risk != rec.accountRiskAtCreation {
		rec.challenge.State = ChallengeInvalidated
		rec.challenge.Decision = DecisionNone
		s.clearActiveLocked(acc, rec.challenge.ID)
		return nil, ErrChallengeInvalidated
	}

	msg, err := resp.SigningMessage()
	if err != nil {
		return nil, err
	}
	if len(resp.Signature) != ed25519.SignatureSize ||
		!ed25519.Verify(dev.device.PublicKey, msg, resp.Signature) {
		return nil, ErrInvalidSignature
	}

	if resp.Decision == DecisionApprove {
		if subtle.ConstantTimeCompare([]byte(resp.DisplayCode), []byte(rec.challenge.DisplayCode)) != 1 {
			return nil, ErrDisplayCodeMismatch
		}
		rec.challenge.State = ChallengeApproved
		rec.challenge.Decision = DecisionApprove
		acc.pending = nil
		acc.consecutiveDenials = 0
		out := rec.challenge
		return &out, nil
	}

	rec.challenge.State = ChallengeDenied
	rec.challenge.Decision = DecisionDeny
	s.clearActiveLocked(acc, rec.challenge.ID)
	acc.consecutiveDenials++
	if acc.consecutiveDenials >= s.cfg.DenialLockThreshold {
		acc.lockedUntil = now.Add(s.cfg.DenialLockDuration)
	}
	out := rec.challenge
	return &out, nil
}

// HandleResponse is an alias for Respond.
func (s *Server) HandleResponse(resp DeviceResponse) (*Challenge, error) {
	return s.Respond(resp)
}

// RegisterDevice registers or idempotently re-enrolls an authenticator.
func (s *Server) RegisterDevice(deviceID DeviceID, accountID AccountID, publicKey ed25519.PublicKey) error {
	if normalize(string(deviceID)) == "" || normalize(string(accountID)) == "" {
		return fmt.Errorf("%w: device and account ids are required", ErrInvalidConfig)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return ErrInvalidDeviceKey
	}
	key := append(ed25519.PublicKey(nil), publicKey...)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	existing := s.devices[deviceID]
	if existing != nil {
		if existing.device.AccountID != accountID {
			if !existing.device.Revoked {
				return ErrDeviceAlreadyRegistered
			}
		} else if !existing.device.Revoked {
			if subtle.ConstantTimeCompare(existing.device.PublicKey, key) == 1 {
				return nil
			}
			return ErrDeviceAlreadyRegistered
		}
	}
	s.devices[deviceID] = &deviceRecord{device: Device{
		ID:           deviceID,
		AccountID:    accountID,
		PublicKey:    key,
		RegisteredAt: now,
	}}
	return nil
}

// RevokeDevice revokes a device and invalidates every Pending or Approved
// challenge bound to it.
func (s *Server) RevokeDevice(deviceID DeviceID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	dev := s.devices[deviceID]
	if dev == nil {
		return ErrDeviceNotFound
	}
	if dev.device.Revoked {
		return nil
	}
	dev.device.Revoked = true
	dev.device.RevokedAt = now

	for _, rec := range s.challenges {
		if rec.challenge.DeviceID != deviceID || !rec.challenge.State.Active() {
			continue
		}
		acc := s.accountLocked(rec.challenge.AccountID)
		s.expireChallengeLocked(acc, rec, now)
		if rec.challenge.State.Active() {
			rec.challenge.State = ChallengeInvalidated
			rec.challenge.Decision = DecisionNone
			s.clearActiveLocked(acc, rec.challenge.ID)
		}
	}
	return nil
}

// Revoke is an alias for RevokeDevice.
func (s *Server) Revoke(deviceID DeviceID) error { return s.RevokeDevice(deviceID) }

// ChangePassword increments the account's credential version and invalidates
// all Pending/Approved challenges and open login transactions.
func (s *Server) ChangePassword(accountID AccountID) error {
	if normalize(string(accountID)) == "" {
		return fmt.Errorf("%w: account id is required", ErrInvalidConfig)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	acc := s.accountLocked(accountID)
	s.expireAccountLocked(acc, now)
	acc.passwordVersion++
	s.invalidateAccountLocked(acc, DecisionNone)
	for _, tx := range s.transactions {
		if tx.AccountID == accountID && tx.State == TransactionOpen {
			tx.State = TransactionInvalidated
		}
	}
	return nil
}

// EscalateRisk raises the account's risk floor. Any increase invalidates all
// Pending/Approved challenges; a lower value is ignored.
func (s *Server) EscalateRisk(accountID AccountID, level RiskLevel) error {
	if !level.valid() {
		return ErrInvalidRisk
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	acc := s.accountLocked(accountID)
	s.expireAccountLocked(acc, now)
	if level <= acc.risk {
		return nil
	}
	acc.risk = level
	s.invalidateAccountLocked(acc, DecisionNone)
	return nil
}

// BeginLogin creates the browser-held login transaction that is allowed to
// consume an approval. A new transaction for the same account/browser
// invalidates the previous open one.
func (s *Server) BeginLogin(accountID AccountID, browserSessionID BrowserSessionID, ttls ...time.Duration) (*LoginTransaction, error) {
	if normalize(string(accountID)) == "" || normalize(string(browserSessionID)) == "" {
		return nil, fmt.Errorf("%w: account and browser session ids are required", ErrInvalidConfig)
	}
	if len(ttls) > 1 {
		return nil, fmt.Errorf("%w: at most one ttl", ErrInvalidConfig)
	}
	ttl := s.cfg.TransactionTTL
	if len(ttls) == 1 {
		if ttls[0] < 0 {
			return nil, fmt.Errorf("%w: ttl must not be negative", ErrInvalidConfig)
		}
		if ttls[0] > 0 {
			ttl = ttls[0]
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	id, err := s.newOpaqueIDLocked("tx_", 16)
	if err != nil {
		return nil, err
	}
	for {
		if _, exists := s.transactions[TransactionID(id)]; !exists {
			break
		}
		id, err = s.newOpaqueIDLocked("tx_", 16)
		if err != nil {
			return nil, err
		}
	}
	for _, tx := range s.transactions {
		if tx.AccountID == accountID && tx.BrowserSessionID == browserSessionID && tx.State == TransactionOpen {
			tx.State = TransactionInvalidated
		}
	}
	tx := &LoginTransaction{
		ID:               TransactionID(id),
		AccountID:        accountID,
		BrowserSessionID: browserSessionID,
		State:            TransactionOpen,
		CreatedAt:        now,
		ExpiresAt:        now.Add(ttl),
	}
	s.transactions[tx.ID] = tx
	out := *tx
	return &out, nil
}

// CreateLoginTransaction is an alias for BeginLogin.
func (s *Server) CreateLoginTransaction(accountID AccountID, browserSessionID BrowserSessionID, ttls ...time.Duration) (*LoginTransaction, error) {
	return s.BeginLogin(accountID, browserSessionID, ttls...)
}

// ConsumeApproval issues a session only when tx is open, belongs to the same
// account, and holds the exact browserSessionID bound into the approved
// challenge. The challenge moves to Completed atomically. If a code is
// supplied it is checked again at consumption time.
func (s *Server) ConsumeApproval(txID TransactionID, challengeID ChallengeID, codes ...string) (*Session, error) {
	if len(codes) > 1 {
		return nil, fmt.Errorf("%w: at most one display code", ErrInvalidConfig)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.nowLocked()
	tx := s.transactions[txID]
	if tx == nil {
		return nil, ErrTransactionNotFound
	}
	if tx.State == TransactionOpen && !now.Before(tx.ExpiresAt) {
		tx.State = TransactionExpired
	}
	if tx.State != TransactionOpen {
		if tx.State == TransactionExpired {
			return nil, ErrTransactionExpired
		}
		return nil, fmt.Errorf("%w: state=%s", ErrTransactionClosed, tx.State)
	}

	rec := s.challenges[challengeID]
	if rec == nil {
		return nil, ErrChallengeNotFound
	}
	acc := s.accountLocked(rec.challenge.AccountID)
	s.expireAccountLocked(acc, now)
	s.expireChallengeLocked(acc, rec, now)

	if rec.challenge.AccountID != tx.AccountID {
		return nil, ErrAccountMismatch
	}
	if rec.challenge.BrowserSessionID != tx.BrowserSessionID {
		return nil, ErrBrowserMismatch
	}
	if rec.challenge.State != ChallengeApproved {
		if rec.challenge.State.Active() {
			return nil, fmt.Errorf("%w: state=%s", ErrChallengeNotApproved, rec.challenge.State)
		}
		return nil, stateError(rec.challenge.State)
	}

	dev := s.devices[rec.challenge.DeviceID]
	if dev == nil {
		return nil, ErrDeviceNotFound
	}
	if dev.device.Revoked {
		rec.challenge.State = ChallengeInvalidated
		rec.challenge.Decision = DecisionNone
		s.clearActiveLocked(acc, challengeID)
		return nil, ErrDeviceRevoked
	}
	if dev.device.AccountID != rec.challenge.AccountID {
		rec.challenge.State = ChallengeInvalidated
		rec.challenge.Decision = DecisionNone
		s.clearActiveLocked(acc, challengeID)
		return nil, ErrDeviceAccountMismatch
	}
	if acc.passwordVersion != rec.passwordVersion || acc.risk != rec.accountRiskAtCreation {
		rec.challenge.State = ChallengeInvalidated
		rec.challenge.Decision = DecisionNone
		s.clearActiveLocked(acc, challengeID)
		return nil, ErrChallengeInvalidated
	}
	if len(codes) == 1 {
		if subtle.ConstantTimeCompare([]byte(codes[0]), []byte(rec.challenge.DisplayCode)) != 1 {
			return nil, ErrDisplayCodeMismatch
		}
	}

	sessionID, err := s.newOpaqueIDLocked("sess_", 16)
	if err != nil {
		return nil, err
	}
	for {
		if _, exists := s.sessions[SessionID(sessionID)]; !exists {
			break
		}
		sessionID, err = s.newOpaqueIDLocked("sess_", 16)
		if err != nil {
			return nil, err
		}
	}
	tokenBytes, err := s.randomBytesLocked(32)
	if err != nil {
		return nil, err
	}

	rec.challenge.State = ChallengeCompleted
	s.clearActiveLocked(acc, challengeID)
	tx.State = TransactionConsumed
	tx.ChallengeID = challengeID
	tx.SessionID = SessionID(sessionID)
	tx.CompletedAt = now

	session := &Session{
		ID:               SessionID(sessionID),
		Token:            hex.EncodeToString(tokenBytes),
		AccountID:        rec.challenge.AccountID,
		BrowserSessionID: rec.challenge.BrowserSessionID,
		DeviceID:         rec.challenge.DeviceID,
		ChallengeID:      rec.challenge.ID,
		RequestDigest:    rec.challenge.RequestDigest,
		Risk:             rec.challenge.Risk,
		IssuedAt:         now,
		ExpiresAt:        now.Add(s.cfg.SessionTTL),
	}
	s.sessions[session.ID] = session
	out := *session
	return &out, nil
}

// Consume is an alias for ConsumeApproval.
func (s *Server) Consume(txID TransactionID, challengeID ChallengeID, codes ...string) (*Session, error) {
	return s.ConsumeApproval(txID, challengeID, codes...)
}

// ExpireDue advances timeout policy for every challenge and transaction and
// returns the number of state transitions caused by the sweep.
func (s *Server) ExpireDue() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	changed := 0
	for _, acc := range s.accounts {
		changed += s.expireAccountLocked(acc, now)
	}
	for _, tx := range s.transactions {
		if tx.State == TransactionOpen && !now.Before(tx.ExpiresAt) {
			tx.State = TransactionExpired
			changed++
		}
	}
	return changed
}

// GetChallenge returns a snapshot.
func (s *Server) GetChallenge(id ChallengeID) (*Challenge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.challenges[id]
	if rec == nil {
		return nil, false
	}
	acc := s.accountLocked(rec.challenge.AccountID)
	s.expireChallengeLocked(acc, rec, s.nowLocked())
	out := rec.challenge
	return &out, true
}

// Challenge is an alias for GetChallenge.
func (s *Server) Challenge(id ChallengeID) (*Challenge, bool) { return s.GetChallenge(id) }

// PendingChallenge returns the account's current Pending challenge, if any.
func (s *Server) PendingChallenge(accountID AccountID) (*Challenge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	acc := s.accountLocked(accountID)
	s.expireAccountLocked(acc, now)
	if acc.pending == nil {
		return nil, false
	}
	rec := s.challenges[*acc.pending]
	if rec == nil {
		return nil, false
	}
	out := rec.challenge
	return &out, true
}

// Pending is an alias for PendingChallenge.
func (s *Server) Pending(accountID AccountID) (*Challenge, bool) {
	return s.PendingChallenge(accountID)
}

// AccountStatus returns counter and policy state without exposing internals.
func (s *Server) AccountStatus(accountID AccountID) AccountStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	acc := s.accountLocked(accountID)
	s.expireAccountLocked(acc, now)
	s.pruneCreationTimesLocked(acc, now)

	status := AccountStatus{
		AccountID:          accountID,
		CooldownUntil:      acc.cooldownUntil,
		HourlyCreations:    len(acc.creationTimes),
		ConsecutiveDenials: acc.consecutiveDenials,
		LockedUntil:        acc.lockedUntil,
		Risk:               acc.risk,
		PasswordVersion:    acc.passwordVersion,
	}
	if acc.pending != nil {
		id := *acc.pending
		status.PendingChallenge = &id
	}
	if acc.inFlight != nil {
		id := *acc.inFlight
		status.InFlightChallenge = &id
	}
	if len(acc.creationTimes) > 0 {
		status.HourlyWindowStart = acc.creationTimes[0]
	}
	return status
}

// GetDevice returns a device snapshot.
func (s *Server) GetDevice(deviceID DeviceID) (*Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev := s.devices[deviceID]
	if dev == nil {
		return nil, false
	}
	out := dev.device
	out.PublicKey = append(ed25519.PublicKey(nil), out.PublicKey...)
	return &out, true
}

// GetTransaction returns a transaction snapshot.
func (s *Server) GetTransaction(id TransactionID) (*LoginTransaction, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.transactions[id]
	if tx == nil {
		return nil, false
	}
	if tx.State == TransactionOpen && !s.nowLocked().Before(tx.ExpiresAt) {
		tx.State = TransactionExpired
	}
	out := *tx
	return &out, true
}

// GetSession returns a session snapshot.
func (s *Server) GetSession(id SessionID) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[id]
	if session == nil {
		return nil, false
	}
	out := *session
	return &out, true
}

// ChallengesForAccount returns a deterministic snapshot ordered by creation.
func (s *Server) ChallengesForAccount(accountID AccountID) []Challenge {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	acc := s.accountLocked(accountID)
	s.expireAccountLocked(acc, now)
	out := make([]Challenge, 0)
	for _, rec := range s.challenges {
		if rec.challenge.AccountID == accountID {
			out = append(out, rec.challenge)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out
}
