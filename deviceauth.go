// Package netsec implements the server side of an OAuth 2.0 device
// authorization grant (RFC 8628).
package netsec

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultDeviceCodeTTL is how long a device authorization remains valid.
	DefaultDeviceCodeTTL = 10 * time.Minute
	// DefaultPollInterval is the minimum interval between device polls.
	DefaultPollInterval = 5 * time.Second
	// DefaultMaxPolls is the maximum number of polls allowed for one device code.
	DefaultMaxPolls = 30
	// DefaultUserCodeFailureLimit is the number of failed user-code attempts
	// allowed in one failure window.
	DefaultUserCodeFailureLimit = 10
	// DefaultUserCodeFailureWindow is the lockout window for failed attempts.
	DefaultUserCodeFailureWindow = time.Minute
	// DefaultTokenTTL is the lifetime recorded on issued tokens.
	DefaultTokenTTL = time.Hour

	defaultVerificationURI = "https://example.com/device"
	maxClientIDLength      = 1024
)

// Config controls a Manager. Zero values select the documented defaults.
type Config struct {
	// DeviceCodeTTL is the lifetime of the device and user code pair.
	DeviceCodeTTL time.Duration
	// PollInterval is the minimum time between device token requests.
	PollInterval time.Duration
	// MaxPolls is the hard budget for polls made while approval is pending.
	// Exceeding it permanently invalidates the code, including a later user
	// approval.
	MaxPolls int
	// ApprovalTTL bounds how long an approval can wait for redemption. It is
	// also capped by DeviceCodeTTL.
	ApprovalTTL time.Duration
	// TokenTTL is recorded as the lifetime of the returned token.
	TokenTTL time.Duration
	// UserCodeFailureLimit is the maximum number of invalid user codes accepted
	// in UserCodeFailureWindow before verification is temporarily locked.
	UserCodeFailureLimit int
	// UserCodeFailureWindow is the fixed window used by the user-code limiter.
	UserCodeFailureWindow time.Duration
	// VerificationURI is returned to the device for user interaction.
	VerificationURI string
	// Now supplies the current time and is useful for deterministic tests.
	Now func() time.Time
	// Rand supplies cryptographic randomness. It defaults to crypto/rand.Reader.
	Rand io.Reader
	// ValidateApprovalSession, when set, lets the authorization server verify
	// that the approval session is authentic and still active.
	ValidateApprovalSession func(ApprovalSession) error
	// IssueToken, when set, replaces the default opaque random token issuer.
	IssueToken TokenIssuer
}

// ApprovalSession describes the authenticated user session that approved a
// device. ID and Subject are required. ClientID, when set, must match the
// client that requested the device code.
type ApprovalSession struct {
	ID              string
	Subject         string
	ClientID        string
	AuthenticatedAt time.Time
	ExpiresAt       time.Time
	Scope           []string
	Claims          map[string]string
}

// Authorization is the response returned by Authorize.
type Authorization struct {
	DeviceCode              string    `json:"device_code"`
	UserCode                string    `json:"user_code"`
	VerificationURI         string    `json:"verification_uri"`
	VerificationURIComplete string    `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int       `json:"expires_in"`
	Interval                int       `json:"interval"`
	IssuedAt                time.Time `json:"-"`
	ExpiresAt               time.Time `json:"-"`
}

// Approval is the server-side record created by Approve.
type Approval struct {
	ClientID   string
	SessionID  string
	Subject    string
	Scope      []string
	Claims     map[string]string
	ApprovedAt time.Time
	ExpiresAt  time.Time
}

// Grant is supplied to a custom TokenIssuer.
type Grant struct {
	ClientID  string
	Subject   string
	SessionID string
	Scope     []string
	Claims    map[string]string
	IssuedAt  time.Time
}

// TokenIssuer creates the access token value for an approved grant. The
// manager still enforces one-time redemption and client binding.
type TokenIssuer func(context.Context, Grant) (string, error)

// Token is an issued access token and its bound grant metadata.
type Token struct {
	AccessToken string            `json:"access_token"`
	TokenType   string            `json:"token_type"`
	ExpiresIn   int               `json:"expires_in"`
	Scope       []string          `json:"scope,omitempty"`
	ClientID    string            `json:"-"`
	Subject     string            `json:"-"`
	SessionID   string            `json:"-"`
	Claims      map[string]string `json:"-"`
	IssuedAt    time.Time         `json:"-"`
	ExpiresAt   time.Time         `json:"-"`
}

type recordState uint8

const (
	statePending recordState = iota
	stateApproved
	stateDenied
	stateConsumed
	statePollLimited
	stateRedeeming
	stateExpired
)

type deviceRecord struct {
	deviceHash [32]byte
	userHash   [32]byte
	clientID   string

	issuedAt  time.Time
	expiresAt time.Time
	lastPoll  time.Time
	polls     int
	state     recordState

	session           ApprovalSession
	approvalExpiresAt time.Time
}

// Manager is a concurrency-safe device authorization component. It stores
// only hashes of device and user codes.
type Manager struct {
	mu       sync.Mutex
	randMu   sync.Mutex
	cfg      Config
	byDevice map[[32]byte]*deviceRecord
	byUser   map[[32]byte]*deviceRecord

	nextSweep  time.Time
	sweepEvery time.Duration

	// User-code failures use a fixed window. Once the limit is reached, all
	// verification attempts are rejected until the window ends.
	failureWindowStart time.Time
	failureCount       int
	lockedUntil        time.Time
}

// New constructs a Manager.
func New(cfg Config) (*Manager, error) {
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}

	sweepEvery := time.Minute
	if normalized.DeviceCodeTTL < sweepEvery {
		sweepEvery = normalized.DeviceCodeTTL
	}
	if sweepEvery <= 0 {
		sweepEvery = time.Minute
	}

	now := normalized.Now()
	return &Manager{
		cfg:        normalized,
		byDevice:   make(map[[32]byte]*deviceRecord),
		byUser:     make(map[[32]byte]*deviceRecord),
		nextSweep:  now.Add(sweepEvery),
		sweepEvery: sweepEvery,
	}, nil
}

// NewManager is an alias for New.
func NewManager(cfg Config) (*Manager, error) {
	return New(cfg)
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.DeviceCodeTTL < 0 {
		return Config{}, fmt.Errorf("%w: DeviceCodeTTL must not be negative", ErrInvalidRequest)
	}
	if cfg.PollInterval < 0 {
		return Config{}, fmt.Errorf("%w: PollInterval must not be negative", ErrInvalidRequest)
	}
	if cfg.MaxPolls < 0 {
		return Config{}, fmt.Errorf("%w: MaxPolls must not be negative", ErrInvalidRequest)
	}
	if cfg.ApprovalTTL < 0 {
		return Config{}, fmt.Errorf("%w: ApprovalTTL must not be negative", ErrInvalidRequest)
	}
	if cfg.TokenTTL < 0 {
		return Config{}, fmt.Errorf("%w: TokenTTL must not be negative", ErrInvalidRequest)
	}
	if cfg.UserCodeFailureLimit < 0 {
		return Config{}, fmt.Errorf("%w: UserCodeFailureLimit must not be negative", ErrInvalidRequest)
	}
	if cfg.UserCodeFailureWindow < 0 {
		return Config{}, fmt.Errorf("%w: UserCodeFailureWindow must not be negative", ErrInvalidRequest)
	}

	if cfg.DeviceCodeTTL == 0 {
		cfg.DeviceCodeTTL = DefaultDeviceCodeTTL
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.MaxPolls == 0 {
		cfg.MaxPolls = DefaultMaxPolls
	}
	if cfg.ApprovalTTL == 0 {
		cfg.ApprovalTTL = cfg.DeviceCodeTTL
	}
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = DefaultTokenTTL
	}
	if cfg.UserCodeFailureLimit == 0 {
		cfg.UserCodeFailureLimit = DefaultUserCodeFailureLimit
	}
	if cfg.UserCodeFailureWindow == 0 {
		cfg.UserCodeFailureWindow = DefaultUserCodeFailureWindow
	}
	if cfg.VerificationURI == "" {
		cfg.VerificationURI = defaultVerificationURI
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	return cfg, nil
}

// Authorize creates a new device/user code pair for clientID.
func (m *Manager) Authorize(ctx context.Context, clientID string) (Authorization, error) {
	if err := ctx.Err(); err != nil {
		return Authorization{}, err
	}
	if !validClientID(clientID) {
		return Authorization{}, ErrInvalidClient
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.cfg.Now()
	m.sweepLocked(now, nil)

	for attempt := 0; attempt < 8; attempt++ {
		deviceCode, err := m.newDeviceCode()
		if err != nil {
			return Authorization{}, fmt.Errorf("%w: %v", ErrTokenGeneration, err)
		}
		userCode, err := m.newUserCode()
		if err != nil {
			return Authorization{}, fmt.Errorf("%w: %v", ErrTokenGeneration, err)
		}

		deviceHash := hashDeviceCode(deviceCode)
		userHash := hashUserCode(userCode)
		if m.byDevice[deviceHash] != nil || m.byUser[userHash] != nil {
			continue
		}

		record := &deviceRecord{
			deviceHash: deviceHash,
			userHash:   userHash,
			clientID:   clientID,
			issuedAt:   now,
			expiresAt:  now.Add(m.cfg.DeviceCodeTTL),
			state:      statePending,
		}
		m.byDevice[deviceHash] = record
		m.byUser[userHash] = record

		displayUserCode := FormatUserCode(userCode)
		return Authorization{
			DeviceCode:              deviceCode,
			UserCode:                displayUserCode,
			VerificationURI:         m.cfg.VerificationURI,
			VerificationURIComplete: verificationURIComplete(m.cfg.VerificationURI, displayUserCode),
			ExpiresIn:               ceilSeconds(m.cfg.DeviceCodeTTL),
			Interval:                ceilSeconds(m.cfg.PollInterval),
			IssuedAt:                record.issuedAt,
			ExpiresAt:               record.expiresAt,
		}, nil
	}

	return Authorization{}, fmt.Errorf("%w: unable to allocate unique codes", ErrTokenGeneration)
}

// Request is an alias for Authorize.
func (m *Manager) Request(ctx context.Context, clientID string) (Authorization, error) {
	return m.Authorize(ctx, clientID)
}

// Approve verifies a user code and binds the resulting approval to the
// authenticated session and the client that requested the device code.
func (m *Manager) Approve(ctx context.Context, userCode string, session ApprovalSession) (Approval, error) {
	if err := ctx.Err(); err != nil {
		return Approval{}, err
	}

	now := m.cfg.Now()
	if err := validateApprovalSession(session, now); err != nil {
		return Approval{}, err
	}
	if m.cfg.ValidateApprovalSession != nil {
		if err := m.cfg.ValidateApprovalSession(session); err != nil {
			return Approval{}, fmt.Errorf("%w: %w", ErrInvalidSession, err)
		}
	}

	normalized, normalizeErr := NormalizeUserCode(userCode)
	userHash := hashUserCode(normalized)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkUserCodeAttemptLocked(now); err != nil {
		return Approval{}, err
	}
	if normalizeErr != nil {
		return Approval{}, m.recordUserCodeFailureLocked(now)
	}

	record := m.byUser[userHash]
	if record == nil {
		return Approval{}, m.recordUserCodeFailureLocked(now)
	}
	m.sweepLocked(now, record)

	if !now.Before(record.expiresAt) {
		m.removeLocked(record)
		return Approval{}, ErrExpiredToken
	}

	switch record.state {
	case statePending:
	case stateApproved:
		return Approval{}, ErrAlreadyApproved
	case stateDenied:
		return Approval{}, ErrAccessDenied
	case stateConsumed:
		return Approval{}, ErrAlreadyUsed
	case statePollLimited:
		return Approval{}, ErrPollLimitExceeded
	case stateRedeeming:
		return Approval{}, ErrAlreadyUsed
	case stateExpired:
		return Approval{}, ErrExpiredToken
	default:
		return Approval{}, ErrInvalidUserCode
	}

	if session.ClientID != "" && session.ClientID != record.clientID {
		return Approval{}, ErrClientMismatch
	}

	approvalExpiresAt := now.Add(m.cfg.ApprovalTTL)
	if !session.ExpiresAt.IsZero() && session.ExpiresAt.Before(approvalExpiresAt) {
		approvalExpiresAt = session.ExpiresAt
	}
	if record.expiresAt.Before(approvalExpiresAt) {
		approvalExpiresAt = record.expiresAt
	}
	if !now.Before(approvalExpiresAt) {
		return Approval{}, ErrInvalidSession
	}

	record.session = cloneSession(session)
	record.approvalExpiresAt = approvalExpiresAt
	record.state = stateApproved

	return Approval{
		ClientID:   record.clientID,
		SessionID:  session.ID,
		Subject:    session.Subject,
		Scope:      cloneStrings(session.Scope),
		Claims:     cloneMap(session.Claims),
		ApprovedAt: now,
		ExpiresAt:  approvalExpiresAt,
	}, nil
}

// Deny marks a pending device authorization as denied by the user.
func (m *Manager) Deny(ctx context.Context, userCode string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	now := m.cfg.Now()
	normalized, normalizeErr := NormalizeUserCode(userCode)
	userHash := hashUserCode(normalized)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkUserCodeAttemptLocked(now); err != nil {
		return err
	}
	if normalizeErr != nil {
		return m.recordUserCodeFailureLocked(now)
	}

	record := m.byUser[userHash]
	if record == nil {
		return m.recordUserCodeFailureLocked(now)
	}
	m.sweepLocked(now, record)

	if !now.Before(record.expiresAt) {
		m.removeLocked(record)
		return ErrExpiredToken
	}

	switch record.state {
	case statePending:
		record.state = stateDenied
		return nil
	case stateApproved:
		return ErrAlreadyApproved
	case stateDenied:
		return ErrAccessDenied
	case stateConsumed:
		return ErrAlreadyUsed
	case statePollLimited:
		return ErrPollLimitExceeded
	case stateRedeeming:
		return ErrAlreadyUsed
	case stateExpired:
		return ErrExpiredToken
	default:
		return ErrInvalidUserCode
	}
}

// Redeem exchanges a device code for a one-time token. The call is bound to
// the client that created the device code and to the user's approval session.
func (m *Manager) Redeem(ctx context.Context, clientID, deviceCode string) (Token, error) {
	if err := ctx.Err(); err != nil {
		return Token{}, err
	}
	if !validClientID(clientID) {
		return Token{}, ErrInvalidClient
	}
	if deviceCode == "" {
		return Token{}, ErrInvalidDeviceCode
	}

	deviceHash := hashDeviceCode(deviceCode)
	now := m.cfg.Now()

	m.mu.Lock()
	record := m.byDevice[deviceHash]
	if record == nil {
		m.sweepLocked(now, nil)
		m.mu.Unlock()
		return Token{}, ErrInvalidDeviceCode
	}
	if record.clientID != clientID {
		m.sweepLocked(now, record)
		m.mu.Unlock()
		return Token{}, ErrClientMismatch
	}
	m.sweepLocked(now, record)

	if !now.Before(record.expiresAt) {
		m.removeLocked(record)
		m.mu.Unlock()
		return Token{}, ErrExpiredToken
	}

	switch record.state {
	case stateDenied:
		m.mu.Unlock()
		return Token{}, ErrAccessDenied
	case stateConsumed:
		m.mu.Unlock()
		return Token{}, ErrAlreadyUsed
	case statePollLimited:
		m.mu.Unlock()
		return Token{}, ErrPollLimitExceeded
	case stateRedeeming:
		m.mu.Unlock()
		return Token{}, ErrAlreadyUsed
	case stateExpired:
		m.mu.Unlock()
		return Token{}, ErrExpiredToken
	case statePending, stateApproved:
	default:
		m.mu.Unlock()
		return Token{}, ErrInvalidDeviceCode
	}

	if record.state == statePending {
		record.polls++
		if record.polls > m.cfg.MaxPolls {
			record.state = statePollLimited
			m.mu.Unlock()
			return Token{}, ErrPollLimitExceeded
		}
		if !record.lastPoll.IsZero() && now.Sub(record.lastPoll) < m.cfg.PollInterval {
			record.lastPoll = now
			m.mu.Unlock()
			return Token{}, ErrSlowDown
		}
		record.lastPoll = now
		m.mu.Unlock()
		return Token{}, ErrAuthorizationPending
	}

	if record.session.ID == "" || !now.Before(record.approvalExpiresAt) {
		record.state = stateExpired
		m.mu.Unlock()
		return Token{}, ErrExpiredToken
	}

	record.state = stateRedeeming
	grant := Grant{
		ClientID:  record.clientID,
		Subject:   record.session.Subject,
		SessionID: record.session.ID,
		Scope:     cloneStrings(record.session.Scope),
		Claims:    cloneMap(record.session.Claims),
		IssuedAt:  now,
	}
	m.mu.Unlock()

	tokenValue, issueErr := m.issueToken(ctx, grant)
	if issueErr != nil {
		m.mu.Lock()
		if record.state == stateRedeeming {
			record.state = stateApproved
		}
		m.mu.Unlock()
		return Token{}, issueErr
	}

	completedAt := m.cfg.Now()
	expiresAt := completedAt.Add(m.cfg.TokenTTL)

	m.mu.Lock()
	if m.byDevice[deviceHash] != record ||
		record.state != stateRedeeming ||
		!completedAt.Before(record.expiresAt) ||
		!completedAt.Before(record.approvalExpiresAt) {
		if record.state == stateRedeeming {
			record.state = stateExpired
		}
		m.mu.Unlock()
		return Token{}, ErrExpiredToken
	}
	record.state = stateConsumed
	m.mu.Unlock()

	return Token{
		AccessToken: tokenValue,
		TokenType:   "Bearer",
		ExpiresIn:   ceilSeconds(m.cfg.TokenTTL),
		Scope:       cloneStrings(grant.Scope),
		ClientID:    grant.ClientID,
		Subject:     grant.Subject,
		SessionID:   grant.SessionID,
		Claims:      cloneMap(grant.Claims),
		IssuedAt:    completedAt,
		ExpiresAt:   expiresAt,
	}, nil
}

// Exchange is an alias for Redeem.
func (m *Manager) Exchange(ctx context.Context, clientID, deviceCode string) (Token, error) {
	return m.Redeem(ctx, clientID, deviceCode)
}

func (m *Manager) issueToken(ctx context.Context, grant Grant) (string, error) {
	if m.cfg.IssueToken != nil {
		value, err := m.cfg.IssueToken(ctx, grant)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrTokenGeneration, err)
		}
		if value == "" {
			return "", fmt.Errorf("%w: issuer returned an empty token", ErrTokenGeneration)
		}
		return value, nil
	}

	value, err := m.newToken()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTokenGeneration, err)
	}
	return value, nil
}

func (m *Manager) newDeviceCode() (string, error) {
	m.randMu.Lock()
	defer m.randMu.Unlock()
	return generateDeviceCode(m.cfg.Rand)
}

func (m *Manager) newUserCode() (string, error) {
	m.randMu.Lock()
	defer m.randMu.Unlock()
	return generateUserCode(m.cfg.Rand)
}

func (m *Manager) newToken() (string, error) {
	m.randMu.Lock()
	defer m.randMu.Unlock()
	return generateToken(m.cfg.Rand)
}

func (m *Manager) checkUserCodeAttemptLocked(now time.Time) error {
	if now.Before(m.lockedUntil) {
		return ErrTooManyAttempts
	}
	if m.failureWindowStart.IsZero() || !now.Before(m.failureWindowStart.Add(m.cfg.UserCodeFailureWindow)) {
		m.failureWindowStart = now
		m.failureCount = 0
		m.lockedUntil = time.Time{}
	}
	return nil
}

func (m *Manager) recordUserCodeFailureLocked(now time.Time) error {
	m.failureCount++
	if m.failureCount >= m.cfg.UserCodeFailureLimit {
		m.lockedUntil = m.failureWindowStart.Add(m.cfg.UserCodeFailureWindow)
		return ErrTooManyAttempts
	}
	return ErrInvalidUserCode
}

func (m *Manager) sweepLocked(now time.Time, keep *deviceRecord) {
	if now.Before(m.nextSweep) {
		return
	}
	for _, record := range m.byDevice {
		if record == keep {
			continue
		}
		if !now.Before(record.expiresAt) {
			m.removeLocked(record)
		}
	}
	m.nextSweep = now.Add(m.sweepEvery)
}

func (m *Manager) removeLocked(record *deviceRecord) {
	delete(m.byDevice, record.deviceHash)
	if current := m.byUser[record.userHash]; current == record {
		delete(m.byUser, record.userHash)
	}
}

func validateApprovalSession(session ApprovalSession, now time.Time) error {
	if strings.TrimSpace(session.ID) == "" || strings.TrimSpace(session.Subject) == "" {
		return ErrInvalidSession
	}
	if !session.ExpiresAt.IsZero() && !now.Before(session.ExpiresAt) {
		return ErrInvalidSession
	}
	return nil
}

func validClientID(clientID string) bool {
	return strings.TrimSpace(clientID) != "" && len(clientID) <= maxClientIDLength
}

func cloneSession(session ApprovalSession) ApprovalSession {
	session.Scope = cloneStrings(session.Scope)
	session.Claims = cloneMap(session.Claims)
	return session
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func ceilSeconds(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	seconds := int((duration + time.Second - 1) / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func verificationURIComplete(base, userCode string) string {
	if base == "" {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	query.Set("user_code", userCode)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func base64URLEncode(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}
