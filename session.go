package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	// SessionIDBytes is the amount of entropy placed in every session
	// identifier. The identifier is encoded with unpadded base64url, so a
	// generated identifier is 43 characters long and contains 256 bits of
	// randomness.
	SessionIDBytes = 32

	// DefaultIdleTimeout is used when Config.IdleTimeout is zero.
	DefaultIdleTimeout = 30 * time.Minute

	// DefaultAbsoluteLifetime is used when Config.AbsoluteLifetime is zero.
	DefaultAbsoluteLifetime = 12 * time.Hour
)

var (
	// ErrUserIDRequired is returned when authentication is attempted without
	// a user identity.
	ErrUserIDRequired = errors.New("netsec: user ID is required")

	// ErrDeviceIDRequired is returned when an operation requires a device
	// binding but none was supplied.
	ErrDeviceIDRequired = errors.New("netsec: device ID is required")

	// ErrSessionInvalid is returned for an unknown, rotated, logged-out, or
	// otherwise unusable session identifier. It intentionally does not reveal
	// whether the identifier ever existed.
	ErrSessionInvalid = errors.New("netsec: session identifier is invalid or no longer active")

	// ErrSessionNotFound is an alias for ErrSessionInvalid.
	ErrSessionNotFound = ErrSessionInvalid

	// ErrSessionExpired is returned when a known session has reached either
	// its idle timeout or its absolute lifetime.
	ErrSessionExpired = errors.New("netsec: session has expired")

	// ErrDeviceMismatch is returned when a session is presented from a device
	// other than the one that was bound during authentication.
	ErrDeviceMismatch = errors.New("netsec: session is bound to a different device")

	// ErrConcurrentLoginRejected is returned by the RejectNew policy when the
	// user already has an active session.
	ErrConcurrentLoginRejected = errors.New("netsec: user already has an active session")

	// ErrTenantIDRequired is returned when a tenant switch has no target
	// tenant.
	ErrTenantIDRequired = errors.New("netsec: tenant ID is required")

	// ErrTenantUnchanged is returned when a tenant switch names the tenant
	// that the session is already using. No rotation is performed.
	ErrTenantUnchanged = errors.New("netsec: session already belongs to that tenant")

	// ErrNoPrivilegeChange is returned when an elevation operation would not
	// add a role, permission, or authentication level. No rotation is
	// performed for a no-op.
	ErrNoPrivilegeChange = errors.New("netsec: elevation makes no privilege change")

	// ErrPrivilegeDowngrade is returned when Elevate is asked to lower the
	// session's authentication level. Use a dedicated downgrade operation if
	// that behavior is required.
	ErrPrivilegeDowngrade = errors.New("netsec: elevation cannot lower authentication level")

	// ErrInvalidConcurrentPolicy is returned by NewManager for an unknown
	// concurrent-login policy.
	ErrInvalidConcurrentPolicy = errors.New("netsec: invalid concurrent login policy")

	// ErrInvalidConfig is returned for invalid duration or identity settings.
	ErrInvalidConfig = errors.New("netsec: invalid configuration")

	// ErrRandomSource is returned when the configured entropy source cannot
	// produce a complete session identifier.
	ErrRandomSource = errors.New("netsec: unable to read session entropy")
)

// ConcurrentLoginPolicy defines what happens when the same user authenticates
// while another session is still active.
//
// The zero value is ConcurrentLoginRejectNew. Policies are evaluated per user
// across all tenants after expired sessions have been removed.
type ConcurrentLoginPolicy uint8

const (
	// ConcurrentLoginRejectNew keeps the existing session and rejects the new
	// authentication attempt with ErrConcurrentLoginRejected.
	ConcurrentLoginRejectNew ConcurrentLoginPolicy = iota + 1

	// ConcurrentLoginRevokeExisting revokes every active session belonging to
	// the user, then issues the new session. This provides one active session
	// per user.
	ConcurrentLoginRevokeExisting

	// ConcurrentLoginAllow permits multiple independent sessions for the same
	// user. A logout or rotation of one session does not affect the others.
	ConcurrentLoginAllow
)

// Aliases make the intent of the policy values readable at call sites that use
// different terminology.
const (
	ConcurrentLoginReplaceExisting = ConcurrentLoginRevokeExisting
	ConcurrentLoginAllowMultiple   = ConcurrentLoginAllow
)

func (p ConcurrentLoginPolicy) String() string {
	switch p {
	case ConcurrentLoginRejectNew:
		return "reject-new"
	case ConcurrentLoginRevokeExisting:
		return "revoke-existing"
	case ConcurrentLoginAllow:
		return "allow"
	default:
		return "invalid"
	}
}

func (p ConcurrentLoginPolicy) valid() bool {
	return p == ConcurrentLoginRejectNew ||
		p == ConcurrentLoginRevokeExisting ||
		p == ConcurrentLoginAllow
}

// Config controls session lifetime and concurrency behavior.
type Config struct {
	// IdleTimeout is the maximum time between successful validations. A zero
	// value uses DefaultIdleTimeout. It must not be negative.
	IdleTimeout time.Duration

	// AbsoluteLifetime is the maximum lifetime of a session from the moment
	// of authentication. Rotation does not extend it. A zero value uses
	// DefaultAbsoluteLifetime. It must not be negative.
	AbsoluteLifetime time.Duration

	// ConcurrentLoginPolicy determines how a second login for the same user
	// is handled. The zero value means ConcurrentLoginRejectNew.
	ConcurrentLoginPolicy ConcurrentLoginPolicy

	// Clock supplies the current time. It is primarily useful for tests. A
	// nil value uses time.Now.
	Clock func() time.Time

	// Rand supplies entropy for session identifiers. Reads are serialized by
	// the manager. A nil value uses crypto/rand.Reader.
	Rand io.Reader
}

// DefaultConfig returns a secure default configuration. The concurrent login
// policy is RejectNew, so callers must deliberately choose Allow or
// RevokeExisting to support multiple sessions.
func DefaultConfig() Config {
	return Config{
		IdleTimeout:           DefaultIdleTimeout,
		AbsoluteLifetime:      DefaultAbsoluteLifetime,
		ConcurrentLoginPolicy: ConcurrentLoginRejectNew,
	}
}

// LoginRequest contains the result of a successfully completed authentication.
// It deliberately has no field for a client-supplied session identifier: a
// session identifier is only ever created by Manager.Authenticate.
type LoginRequest struct {
	UserID      string
	TenantID    string
	DeviceID    string
	AuthLevel   int
	Roles       []string
	Permissions []string
	AuthMethod  string
}

// Elevation describes privileges added by a successful privilege-elevation
// flow. Existing roles and permissions are retained. A zero AuthLevel means
// that only the supplied roles and permissions are added; a non-zero
// AuthLevel may not be lower than the session's current level.
type Elevation struct {
	AuthLevel   int
	Roles       []string
	Permissions []string
}

// TenantSwitch describes the state for a new tenant. Roles and permissions
// are replaced rather than inherited, preventing a privilege granted in one
// tenant from leaking into another.
type TenantSwitch struct {
	TenantID    string
	AuthLevel   int
	Roles       []string
	Permissions []string
}

// Session is an immutable snapshot of authenticated session state. The ID is
// the current identifier; an earlier ID is invalid immediately after any
// rotation.
type Session struct {
	ID            string
	UserID        string
	TenantID      string
	DeviceID      string
	AuthLevel     int
	Roles         []string
	Permissions   []string
	AuthMethod    string
	Authenticated bool
	CreatedAt     time.Time
	LastSeenAt    time.Time
	RotatedAt     time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
	Generation    uint64
}

// Expired reports whether the snapshot is expired at now. A snapshot is stale
// data and is not itself a source of authorization.
func (s Session) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt) || !now.Before(s.IdleExpiresAt)
}

type sessionRecord struct {
	idHash        [sha256.Size]byte
	userID        string
	tenantID      string
	deviceID      string
	authLevel     int
	roles         []string
	permissions   []string
	authMethod    string
	createdAt     time.Time
	lastSeenAt    time.Time
	rotatedAt     time.Time
	expiresAt     time.Time
	idleExpiresAt time.Time
	generation    uint64
}

// Manager owns the lifecycle of authenticated sessions. All exported methods
// are safe for concurrent use.
//
// There is intentionally no API that allocates a session identifier before
// authentication. Authenticate creates a fresh identifier from the configured
// entropy source; Rotate, Elevate, and SwitchTenant replace an existing
// identifier atomically.
type Manager struct {
	mu               sync.Mutex
	randMu           sync.Mutex
	idleTimeout      time.Duration
	absoluteLifetime time.Duration
	policy           ConcurrentLoginPolicy
	clock            func() time.Time
	rand             io.Reader

	sessions map[[sha256.Size]byte]*sessionRecord
	byUser   map[string]map[*sessionRecord]struct{}
}

// NewManager validates cfg and returns a session manager.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.IdleTimeout < 0 {
		return nil, fmt.Errorf("%w: IdleTimeout must not be negative", ErrInvalidConfig)
	}
	if cfg.AbsoluteLifetime < 0 {
		return nil, fmt.Errorf("%w: AbsoluteLifetime must not be negative", ErrInvalidConfig)
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.AbsoluteLifetime == 0 {
		cfg.AbsoluteLifetime = DefaultAbsoluteLifetime
	}
	if cfg.ConcurrentLoginPolicy == 0 {
		cfg.ConcurrentLoginPolicy = ConcurrentLoginRejectNew
	}
	if !cfg.ConcurrentLoginPolicy.valid() {
		return nil, fmt.Errorf("%w: %d", ErrInvalidConcurrentPolicy, cfg.ConcurrentLoginPolicy)
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}

	return &Manager{
		idleTimeout:      cfg.IdleTimeout,
		absoluteLifetime: cfg.AbsoluteLifetime,
		policy:           cfg.ConcurrentLoginPolicy,
		clock:            cfg.Clock,
		rand:             cfg.Rand,
		sessions:         make(map[[sha256.Size]byte]*sessionRecord),
		byUser:           make(map[string]map[*sessionRecord]struct{}),
	}, nil
}

// New is an alias for NewManager.
func New(cfg Config) (*Manager, error) {
	return NewManager(cfg)
}

// NewSessionManager is an alias for NewManager.
func NewSessionManager(cfg Config) (*Manager, error) {
	return NewManager(cfg)
}

// Authenticate completes a successful authentication and returns a newly
// created session. The identifier is generated exclusively by the manager;
// nothing supplied by the client can select or influence it. Any identifier
// the client presented before authentication is therefore unusable after
// login.
func (m *Manager) Authenticate(req LoginRequest) (*Session, error) {
	if strings.TrimSpace(req.UserID) == "" {
		return nil, ErrUserIDRequired
	}
	if strings.TrimSpace(req.DeviceID) == "" {
		return nil, ErrDeviceIDRequired
	}
	if req.AuthLevel < 0 {
		return nil, fmt.Errorf("%w: AuthLevel must not be negative", ErrInvalidConfig)
	}

	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.applyConcurrentPolicyLocked(req.UserID, now); err != nil {
		return nil, err
	}

	id, hash, err := m.newSessionIDLocked()
	if err != nil {
		return nil, err
	}

	rec := &sessionRecord{
		idHash:        hash,
		userID:        req.UserID,
		tenantID:      req.TenantID,
		deviceID:      req.DeviceID,
		authLevel:     req.AuthLevel,
		roles:         uniqueStrings(nil, req.Roles),
		permissions:   uniqueStrings(nil, req.Permissions),
		authMethod:    req.AuthMethod,
		createdAt:     now,
		lastSeenAt:    now,
		expiresAt:     now.Add(m.absoluteLifetime),
		idleExpiresAt: now.Add(m.idleTimeout),
		generation:    1,
	}
	if rec.idleExpiresAt.After(rec.expiresAt) {
		rec.idleExpiresAt = rec.expiresAt
	}

	m.sessions[hash] = rec
	userSessions := m.byUser[rec.userID]
	if userSessions == nil {
		userSessions = make(map[*sessionRecord]struct{})
		m.byUser[rec.userID] = userSessions
	}
	userSessions[rec] = struct{}{}

	return snapshot(rec, id), nil
}

// Login is an alias for Authenticate.
func (m *Manager) Login(req LoginRequest) (*Session, error) {
	return m.Authenticate(req)
}

// Validate checks an identifier, device binding, and both expiration limits.
// A successful validation refreshes the idle timeout, but never the absolute
// lifetime. Expired sessions are removed before the error is returned.
func (m *Manager) Validate(id, deviceID string) (*Session, error) {
	if strings.TrimSpace(deviceID) == "" {
		return nil, ErrDeviceIDRequired
	}

	now := m.clock()
	hash := hashID(id)

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, err := m.activeRecordLocked(hash, deviceID, now)
	if err != nil {
		return nil, err
	}

	rec.lastSeenAt = now
	rec.idleExpiresAt = now.Add(m.idleTimeout)
	if rec.idleExpiresAt.After(rec.expiresAt) {
		rec.idleExpiresAt = rec.expiresAt
	}

	return snapshot(rec, id), nil
}

// Rotate replaces id with a new high-entropy identifier and invalidates id
// atomically. It is suitable for generic state transitions. Privilege and
// tenant transitions should use Elevate and SwitchTenant so their state is
// updated in the same atomic operation.
func (m *Manager) Rotate(id, deviceID string) (*Session, error) {
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.rotateLocked(id, deviceID, now, func(*sessionRecord) error {
		return nil
	})
}

// RotateSession is an alias for Rotate.
func (m *Manager) RotateSession(id, deviceID string) (*Session, error) {
	return m.Rotate(id, deviceID)
}

// Elevate applies a successful privilege elevation and rotates the session
// identifier in the same critical section. The old identifier is invalid as
// soon as this method returns.
func (m *Manager) Elevate(id, deviceID string, elevation Elevation) (*Session, error) {
	if elevation.AuthLevel < 0 {
		return nil, fmt.Errorf("%w: AuthLevel must not be negative", ErrInvalidConfig)
	}

	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.rotateLocked(id, deviceID, now, func(rec *sessionRecord) error {
		if elevation.AuthLevel != 0 && elevation.AuthLevel < rec.authLevel {
			return ErrPrivilegeDowngrade
		}

		newRoles := uniqueStrings(rec.roles, elevation.Roles)
		newPermissions := uniqueStrings(rec.permissions, elevation.Permissions)
		newLevel := rec.authLevel
		if elevation.AuthLevel > newLevel {
			newLevel = elevation.AuthLevel
		}

		if newLevel == rec.authLevel &&
			len(newRoles) == len(rec.roles) &&
			len(newPermissions) == len(rec.permissions) {
			return ErrNoPrivilegeChange
		}

		rec.authLevel = newLevel
		rec.roles = newRoles
		rec.permissions = newPermissions
		return nil
	})
}

// ElevateRoles is a convenience wrapper for adding roles without changing the
// numeric authentication level.
func (m *Manager) ElevateRoles(id, deviceID string, roles ...string) (*Session, error) {
	return m.Elevate(id, deviceID, Elevation{Roles: roles})
}

// SwitchTenant moves a session to another tenant and rotates its identifier.
// Roles, permissions, and the authentication level come from req; inherited
// privileges are intentionally discarded.
func (m *Manager) SwitchTenant(id, deviceID string, req TenantSwitch) (*Session, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, ErrTenantIDRequired
	}
	if req.AuthLevel < 0 {
		return nil, fmt.Errorf("%w: AuthLevel must not be negative", ErrInvalidConfig)
	}

	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.rotateLocked(id, deviceID, now, func(rec *sessionRecord) error {
		if rec.tenantID == req.TenantID {
			return ErrTenantUnchanged
		}

		rec.tenantID = req.TenantID
		rec.authLevel = req.AuthLevel
		rec.roles = uniqueStrings(nil, req.Roles)
		rec.permissions = uniqueStrings(nil, req.Permissions)
		return nil
	})
}

// SwitchTenantTo is a convenience wrapper that switches tenant and clears
// tenant-scoped privileges.
func (m *Manager) SwitchTenantTo(id, deviceID, tenantID string) (*Session, error) {
	return m.SwitchTenant(id, deviceID, TenantSwitch{TenantID: tenantID})
}

// Logout invalidates the current identifier. It is idempotent and returns nil
// for an identifier that is already invalid.
func (m *Manager) Logout(id string) error {
	hash := hashID(id)

	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.sessions[hash]; ok {
		m.deleteLocked(rec)
	}
	return nil
}

// Revoke is an alias for Logout.
func (m *Manager) Revoke(id string) error {
	return m.Logout(id)
}

// RevokeUser invalidates all sessions for userID and returns the number of
// sessions that were removed.
func (m *Manager) RevokeUser(userID string) int {
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.purgeUserLocked(userID, now)
	removed := 0
	for rec := range m.byUser[userID] {
		m.deleteLocked(rec)
		removed++
	}
	return removed
}

// RevokeAll invalidates all sessions and returns the number that were removed.
func (m *Manager) RevokeAll() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := len(m.sessions)
	m.sessions = make(map[[sha256.Size]byte]*sessionRecord)
	m.byUser = make(map[string]map[*sessionRecord]struct{})
	return removed
}

// ActiveSessionCount returns the number of non-expired sessions for userID.
// Expired sessions are removed while counting.
func (m *Manager) ActiveSessionCount(userID string) int {
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.purgeUserLocked(userID, now)
	return len(m.byUser[userID])
}

// SessionCount returns the number of non-expired sessions across all users.
// Expired sessions are removed while counting.
func (m *Manager) SessionCount() int {
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.purgeAllLocked(now)
	return len(m.sessions)
}

// PruneExpired removes all expired sessions and returns the number removed.
// It is safe to call periodically; no background goroutine is started by the
// manager.
func (m *Manager) PruneExpired() int {
	now := m.clock()

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.purgeAllLocked(now)
}

func (m *Manager) applyConcurrentPolicyLocked(userID string, now time.Time) error {
	m.purgeUserLocked(userID, now)

	existing := m.byUser[userID]
	if len(existing) == 0 {
		return nil
	}

	switch m.policy {
	case ConcurrentLoginRejectNew:
		return ErrConcurrentLoginRejected
	case ConcurrentLoginRevokeExisting:
		for rec := range existing {
			m.deleteLocked(rec)
		}
		return nil
	case ConcurrentLoginAllow:
		return nil
	default:
		return ErrInvalidConcurrentPolicy
	}
}

func (m *Manager) rotateLocked(
	id string,
	deviceID string,
	now time.Time,
	update func(*sessionRecord) error,
) (*Session, error) {
	rec, err := m.activeRecordLocked(hashID(id), deviceID, now)
	if err != nil {
		return nil, err
	}

	newID, newHash, err := m.newSessionIDLocked()
	if err != nil {
		return nil, err
	}

	if update != nil {
		if err := update(rec); err != nil {
			return nil, err
		}
	}

	delete(m.sessions, rec.idHash)
	rec.idHash = newHash
	rec.generation++
	rec.rotatedAt = now
	rec.lastSeenAt = now
	rec.idleExpiresAt = now.Add(m.idleTimeout)
	if rec.idleExpiresAt.After(rec.expiresAt) {
		rec.idleExpiresAt = rec.expiresAt
	}
	m.sessions[newHash] = rec

	return snapshot(rec, newID), nil
}

func (m *Manager) activeRecordLocked(
	hash [sha256.Size]byte,
	deviceID string,
	now time.Time,
) (*sessionRecord, error) {
	if strings.TrimSpace(deviceID) == "" {
		return nil, ErrDeviceIDRequired
	}

	rec, ok := m.sessions[hash]
	if !ok {
		return nil, ErrSessionInvalid
	}
	if !secureEqual(rec.deviceID, deviceID) {
		return nil, ErrDeviceMismatch
	}
	if rec.expired(now) {
		m.deleteLocked(rec)
		return nil, ErrSessionExpired
	}
	return rec, nil
}

func (m *Manager) newSessionIDLocked() (string, [sha256.Size]byte, error) {
	for attempts := 0; attempts < 8; attempts++ {
		id, err := m.readSessionID()
		if err != nil {
			return "", [sha256.Size]byte{}, err
		}
		hash := hashID(id)
		if _, exists := m.sessions[hash]; !exists {
			return id, hash, nil
		}
	}
	return "", [sha256.Size]byte{}, fmt.Errorf("%w: repeated session identifier", ErrRandomSource)
}

func (m *Manager) readSessionID() (string, error) {
	buf := make([]byte, SessionIDBytes)

	m.randMu.Lock()
	_, err := io.ReadFull(m.rand, buf)
	m.randMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRandomSource, err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (m *Manager) purgeUserLocked(userID string, now time.Time) {
	for rec := range m.byUser[userID] {
		if rec.expired(now) {
			m.deleteLocked(rec)
		}
	}
}

func (m *Manager) purgeAllLocked(now time.Time) int {
	removed := 0
	for _, rec := range m.sessions {
		if rec.expired(now) {
			m.deleteLocked(rec)
			removed++
		}
	}
	return removed
}

func (m *Manager) deleteLocked(rec *sessionRecord) {
	delete(m.sessions, rec.idHash)

	userSessions := m.byUser[rec.userID]
	delete(userSessions, rec)
	if len(userSessions) == 0 {
		delete(m.byUser, rec.userID)
	}
}

func (r *sessionRecord) expired(now time.Time) bool {
	return !now.Before(r.expiresAt) || !now.Before(r.idleExpiresAt)
}

func snapshot(rec *sessionRecord, id string) *Session {
	return &Session{
		ID:            id,
		UserID:        rec.userID,
		TenantID:      rec.tenantID,
		DeviceID:      rec.deviceID,
		AuthLevel:     rec.authLevel,
		Roles:         append([]string(nil), rec.roles...),
		Permissions:   append([]string(nil), rec.permissions...),
		AuthMethod:    rec.authMethod,
		Authenticated: true,
		CreatedAt:     rec.createdAt,
		LastSeenAt:    rec.lastSeenAt,
		RotatedAt:     rec.rotatedAt,
		ExpiresAt:     rec.expiresAt,
		IdleExpiresAt: rec.idleExpiresAt,
		Generation:    rec.generation,
	}
}

func hashID(id string) [sha256.Size]byte {
	return sha256.Sum256([]byte(id))
}

func secureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func uniqueStrings(base, additions []string) []string {
	if len(base) == 0 && len(additions) == 0 {
		return nil
	}

	result := make([]string, 0, len(base)+len(additions))
	seen := make(map[string]struct{}, len(base)+len(additions))
	for _, values := range [][]string{base, additions} {
		for _, value := range values {
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
