package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTokenTTL is deliberately short. A caller should issue a token as
	// close as possible to the moment the subject's report is needed.
	DefaultTokenTTL = 2 * time.Minute

	// DefaultAuthorizationTTL limits how long a grant can be used to issue
	// tokens.
	DefaultAuthorizationTTL = 10 * time.Minute
)

var (
	ErrInvalidRequest       = errors.New("netsec: invalid authorization request")
	ErrAuthorizationMissing = errors.New("netsec: authorization not found")
	ErrAuthorizationExpired = errors.New("netsec: authorization expired")
	ErrAuthorizationRevoked = errors.New("netsec: authorization revoked")
	ErrTokenMissing         = errors.New("netsec: token not found")
	ErrTokenExpired         = errors.New("netsec: token expired")
	ErrTokenUsed            = errors.New("netsec: token already used")
	ErrTokenRevoked         = errors.New("netsec: token revoked")
	ErrRequesterMismatch    = errors.New("netsec: requester does not match authorization")
	ErrSubjectMismatch      = errors.New("netsec: subject does not match authorization")
	ErrPurposeMismatch      = errors.New("netsec: purpose does not match authorization")
	ErrInstitutionMismatch  = errors.New("netsec: institution does not match authorization")
	ErrQueryLimitExceeded   = errors.New("netsec: authorization query limit exceeded")
	ErrAuditTampered        = errors.New("netsec: audit log integrity check failed")

	// These aliases make the security meaning explicit at call sites.
	ErrCrossSubject = ErrSubjectMismatch
	ErrTokenReuse   = ErrTokenUsed
)

// AuthorizationRequest describes the complete scope approved by the subject.
// All identity fields are required. A zero MaxQueries defaults to one.
type AuthorizationRequest struct {
	RequesterID   string    `json:"requester_id"`
	SubjectID     string    `json:"subject_id"`
	Purpose       string    `json:"purpose"`
	InstitutionID string    `json:"institution_id"`
	MaxQueries    int       `json:"max_queries"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
}

// Authorization is an immutable snapshot of a query grant. Callers may retain
// it, but changing the snapshot cannot alter the authorizer's state.
type Authorization struct {
	ID            string    `json:"id"`
	RequesterID   string    `json:"requester_id"`
	SubjectID     string    `json:"subject_id"`
	Purpose       string    `json:"purpose"`
	InstitutionID string    `json:"institution_id"`
	MaxQueries    int       `json:"max_queries"`
	UsedQueries   int       `json:"used_queries"`
	IssuedAt      time.Time `json:"issued_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Revoked       bool      `json:"revoked"`
	RevokedAt     time.Time `json:"revoked_at,omitempty"`
}

// QueryRequest contains both the one-time token and the identity claims that
// the credit service extracted from the current, authenticated request.
type QueryRequest struct {
	Token         string `json:"token"`
	RequesterID   string `json:"requester_id"`
	SubjectID     string `json:"subject_id"`
	Purpose       string `json:"purpose"`
	InstitutionID string `json:"institution_id"`
}

// Decision is returned only after a token has been consumed successfully. The
// service must use SubjectID (rather than an untrusted request parameter) as
// the object of the report lookup.
type Decision struct {
	AuthorizationID string    `json:"authorization_id"`
	RequesterID     string    `json:"requester_id"`
	SubjectID       string    `json:"subject_id"`
	Purpose         string    `json:"purpose"`
	InstitutionID   string    `json:"institution_id"`
	UsedQueries     int       `json:"used_queries"`
	Remaining       int       `json:"remaining"`
	UsedAt          time.Time `json:"used_at"`
}

type tokenState struct {
	authorizationID string
	expiresAt       time.Time
	consumed        bool
	revoked         bool
}

// Manager is safe for concurrent use. Its zero value is a valid manager with
// the default TTLs; use New to supply a clock or different TTLs.
type Manager struct {
	mu sync.Mutex

	now              func() time.Time
	tokenTTL         time.Duration
	authorizationTTL time.Duration

	authorizations map[string]*Authorization
	tokens         map[[sha256.Size]byte]*tokenState
	audit          []AuditEntry
	auditHash      string
}

// Option configures a Manager.
type Option func(*Manager)

// WithClock supplies a clock, primarily for deterministic expiry tests.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// WithTokenTTL changes the lifetime of newly issued tokens.
func WithTokenTTL(ttl time.Duration) Option {
	return func(m *Manager) {
		if ttl > 0 {
			m.tokenTTL = ttl
		}
	}
}

// WithAuthorizationTTL changes the default lifetime of new grants.
func WithAuthorizationTTL(ttl time.Duration) Option {
	return func(m *Manager) {
		if ttl > 0 {
			m.authorizationTTL = ttl
		}
	}
}

// New creates an in-memory query authorizer.
func New(opts ...Option) *Manager {
	m := &Manager{
		now:              time.Now,
		tokenTTL:         DefaultTokenTTL,
		authorizationTTL: DefaultAuthorizationTTL,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	m.initLocked()
	return m
}

// Aliases keep the component easy to discover in a credit service.
type Authorizer = Manager
type AuthorizationService = Manager
type AuthorizationManager = Manager
type QueryAuthorizer = Manager

type Grant = Authorization
type QueryAuthorization = Authorization
type GrantRequest = AuthorizationRequest
type AccessRequest = QueryRequest
type QueryResult = Decision

func NewAuthorizer(opts ...Option) *Manager           { return New(opts...) }
func NewAuthorizationService(opts ...Option) *Manager { return New(opts...) }
func NewAuthorizationManager(opts ...Option) *Manager { return New(opts...) }
func NewQueryAuthorizer(opts ...Option) *Manager      { return New(opts...) }

func (m *Manager) initLocked() {
	if m.now == nil {
		m.now = time.Now
	}
	if m.tokenTTL <= 0 {
		m.tokenTTL = DefaultTokenTTL
	}
	if m.authorizationTTL <= 0 {
		m.authorizationTTL = DefaultAuthorizationTTL
	}
	if m.authorizations == nil {
		m.authorizations = make(map[string]*Authorization)
	}
	if m.tokens == nil {
		m.tokens = make(map[[sha256.Size]byte]*tokenState)
	}
	if m.auditHash == "" {
		m.auditHash = zeroAuditHash
	}
}

// CreateAuthorization records a grant. It does not itself consume the query
// budget or create a token.
func (m *Manager) CreateAuthorization(req AuthorizationRequest) (Authorization, error) {
	return m.createAuthorization(req)
}

// Grant is an alias for CreateAuthorization.
func (m *Manager) Grant(req AuthorizationRequest) (Authorization, error) {
	return m.createAuthorization(req)
}

// CreateGrant is an alias for CreateAuthorization.
func (m *Manager) CreateGrant(req AuthorizationRequest) (Authorization, error) {
	return m.createAuthorization(req)
}

func (m *Manager) createAuthorization(req AuthorizationRequest) (Authorization, error) {
	req, err := normalizeAuthorizationRequest(req)
	if err != nil {
		return Authorization{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	now := m.now().UTC()
	if !req.ExpiresAt.IsZero() {
		req.ExpiresAt = req.ExpiresAt.UTC()
		if !now.Before(req.ExpiresAt) {
			return Authorization{}, fmt.Errorf("%w: expiry must be in the future", ErrInvalidRequest)
		}
	} else {
		req.ExpiresAt = now.Add(m.authorizationTTL)
	}

	id, err := randomID("auth_")
	if err != nil {
		return Authorization{}, fmt.Errorf("netsec: generate authorization ID: %w", err)
	}
	auth := &Authorization{
		ID:            id,
		RequesterID:   req.RequesterID,
		SubjectID:     req.SubjectID,
		Purpose:       req.Purpose,
		InstitutionID: req.InstitutionID,
		MaxQueries:    req.MaxQueries,
		IssuedAt:      now,
		ExpiresAt:     req.ExpiresAt,
	}
	m.authorizations[id] = auth
	m.appendAuditLocked(AuditEntry{
		Timestamp:       now,
		Action:          AuditAuthorizationCreated,
		Allowed:         true,
		RequesterID:     auth.RequesterID,
		SubjectID:       auth.SubjectID,
		Purpose:         auth.Purpose,
		InstitutionID:   auth.InstitutionID,
		AuthorizationID: auth.ID,
	})

	return *auth, nil
}

// Authorize creates a grant and immediately issues its first one-time token.
// Use AuthorizeWithGrant when the caller also needs the grant ID.
func (m *Manager) Authorize(req AuthorizationRequest) (string, error) {
	_, token, err := m.AuthorizeWithGrant(req)
	return token, err
}

// AuthorizeWithGrant is Authorize plus the newly created grant snapshot.
func (m *Manager) AuthorizeWithGrant(req AuthorizationRequest) (Authorization, string, error) {
	auth, err := m.createAuthorization(req)
	if err != nil {
		return Authorization{}, "", err
	}
	token, err := m.IssueToken(auth.ID)
	if err != nil {
		return auth, "", err
	}
	return auth, token, nil
}

// IssueToken creates an opaque, cryptographically random token bound to the
// grant. Issuance does not consume the grant's query budget; successful use of
// the one-time token does.
func (m *Manager) IssueToken(authorizationID string) (string, error) {
	authorizationID = strings.TrimSpace(authorizationID)
	if authorizationID == "" {
		return "", fmt.Errorf("%w: missing authorization ID", ErrInvalidRequest)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	now := m.now().UTC()
	auth, ok := m.authorizations[authorizationID]
	if !ok {
		m.appendAuditLocked(AuditEntry{
			Timestamp:       now,
			Action:          AuditTokenIssueDenied,
			AuthorizationID: authorizationID,
			Reason:          ErrAuthorizationMissing.Error(),
		})
		return "", ErrAuthorizationMissing
	}
	if err := checkAuthorizationLocked(auth, now); err != nil {
		m.appendAuditLocked(AuditEntry{
			Timestamp:       now,
			Action:          AuditTokenIssueDenied,
			AuthorizationID: auth.ID,
			RequesterID:     auth.RequesterID,
			SubjectID:       auth.SubjectID,
			Purpose:         auth.Purpose,
			InstitutionID:   auth.InstitutionID,
			Reason:          err.Error(),
		})
		return "", err
	}

	expiresAt := now.Add(m.tokenTTL)
	if auth.ExpiresAt.Before(expiresAt) {
		expiresAt = auth.ExpiresAt
	}
	token, digest, err := newToken()
	if err != nil {
		return "", err
	}
	m.tokens[digest] = &tokenState{
		authorizationID: auth.ID,
		expiresAt:       expiresAt,
	}
	m.appendAuditLocked(AuditEntry{
		Timestamp:       now,
		Action:          AuditTokenIssued,
		Allowed:         true,
		RequesterID:     auth.RequesterID,
		SubjectID:       auth.SubjectID,
		Purpose:         auth.Purpose,
		InstitutionID:   auth.InstitutionID,
		AuthorizationID: auth.ID,
		TokenID:         fingerprint(digest),
	})
	return token, nil
}

// AuthorizeQuery validates every claim, atomically consumes the token, and
// charges one query to the grant. A denied request does not consume the token
// or the query budget, and every outcome is written to the audit ledger.
func (m *Manager) AuthorizeQuery(req QueryRequest) (*Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	now := m.now().UTC()
	digest := sha256.Sum256([]byte(req.Token))
	tokenID := fingerprint(digest)
	token, ok := m.tokens[digest]
	if !ok {
		m.appendQueryAuditLocked(now, nil, tokenID, req, ErrTokenMissing)
		return nil, ErrTokenMissing
	}
	auth := m.authorizations[token.authorizationID]
	if auth == nil {
		m.appendQueryAuditLocked(now, nil, tokenID, req, ErrAuthorizationMissing)
		return nil, ErrAuthorizationMissing
	}

	if err := validateQueryLocked(auth, token, req, now); err != nil {
		m.appendQueryAuditLocked(now, auth, tokenID, req, err)
		return nil, err
	}

	token.consumed = true
	auth.UsedQueries++
	used := auth.UsedQueries
	m.appendQueryAuditLocked(now, auth, tokenID, req, nil)

	return &Decision{
		AuthorizationID: auth.ID,
		RequesterID:     auth.RequesterID,
		SubjectID:       auth.SubjectID,
		Purpose:         auth.Purpose,
		InstitutionID:   auth.InstitutionID,
		UsedQueries:     used,
		Remaining:       auth.MaxQueries - used,
		UsedAt:          now,
	}, nil
}

// ValidateQuery is the error-only form of AuthorizeQuery.
func (m *Manager) ValidateQuery(req QueryRequest) error {
	_, err := m.AuthorizeQuery(req)
	return err
}

// Check is an alias for AuthorizeQuery.
func (m *Manager) Check(req QueryRequest) (*Decision, error) {
	return m.AuthorizeQuery(req)
}

func validateQueryLocked(auth *Authorization, token *tokenState, req QueryRequest, now time.Time) error {
	if token.revoked {
		return ErrTokenRevoked
	}
	if token.consumed {
		return ErrTokenUsed
	}
	if !now.Before(token.expiresAt) {
		return ErrTokenExpired
	}
	if err := checkAuthorizationLocked(auth, now); err != nil {
		return err
	}
	if req.RequesterID != auth.RequesterID {
		return ErrRequesterMismatch
	}
	if req.SubjectID != auth.SubjectID {
		return ErrSubjectMismatch
	}
	if req.Purpose != auth.Purpose {
		return ErrPurposeMismatch
	}
	if req.InstitutionID != auth.InstitutionID {
		return ErrInstitutionMismatch
	}
	if auth.UsedQueries >= auth.MaxQueries {
		return ErrQueryLimitExceeded
	}
	return nil
}

func checkAuthorizationLocked(auth *Authorization, now time.Time) error {
	if auth.Revoked {
		return ErrAuthorizationRevoked
	}
	if !now.Before(auth.ExpiresAt) {
		return ErrAuthorizationExpired
	}
	return nil
}

// Revoke invalidates an authorization by ID or an individual token. A grant
// revocation applies to all of its outstanding tokens. Revocation is
// idempotent.
func (m *Manager) Revoke(idOrToken string) error {
	idOrToken = strings.TrimSpace(idOrToken)
	if idOrToken == "" {
		return fmt.Errorf("%w: missing authorization ID or token", ErrInvalidRequest)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	now := m.now().UTC()
	if auth, ok := m.authorizations[idOrToken]; ok {
		return m.revokeAuthorizationLocked(auth, now)
	}
	digest := sha256.Sum256([]byte(idOrToken))
	if token, ok := m.tokens[digest]; ok {
		return m.revokeTokenLocked(token, digest, now)
	}
	return ErrAuthorizationMissing
}

// RevokeAuthorization invalidates a grant and every token issued under it.
func (m *Manager) RevokeAuthorization(authorizationID string) error {
	authorizationID = strings.TrimSpace(authorizationID)
	if authorizationID == "" {
		return fmt.Errorf("%w: missing authorization ID", ErrInvalidRequest)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	auth, ok := m.authorizations[authorizationID]
	if !ok {
		return ErrAuthorizationMissing
	}
	return m.revokeAuthorizationLocked(auth, m.now().UTC())
}

// RevokeGrant is an alias for RevokeAuthorization.
func (m *Manager) RevokeGrant(authorizationID string) error {
	return m.RevokeAuthorization(authorizationID)
}

func (m *Manager) revokeAuthorizationLocked(auth *Authorization, now time.Time) error {
	if !auth.Revoked {
		auth.Revoked = true
		auth.RevokedAt = now
		m.appendAuditLocked(AuditEntry{
			Timestamp:       now,
			Action:          AuditAuthorizationRevoked,
			Allowed:         true,
			RequesterID:     auth.RequesterID,
			SubjectID:       auth.SubjectID,
			Purpose:         auth.Purpose,
			InstitutionID:   auth.InstitutionID,
			AuthorizationID: auth.ID,
		})
	}
	return nil
}

// RevokeToken invalidates exactly one token without revoking its grant.
func (m *Manager) RevokeToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("%w: missing token", ErrInvalidRequest)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.initLocked()

	digest := sha256.Sum256([]byte(token))
	state, ok := m.tokens[digest]
	if !ok {
		return ErrTokenMissing
	}
	return m.revokeTokenLocked(state, digest, m.now().UTC())
}

func (m *Manager) revokeTokenLocked(token *tokenState, digest [sha256.Size]byte, now time.Time) error {
	if token.revoked {
		return nil
	}
	token.revoked = true
	auth := m.authorizations[token.authorizationID]
	entry := AuditEntry{
		Timestamp:       now,
		Action:          AuditTokenRevoked,
		Allowed:         true,
		AuthorizationID: token.authorizationID,
		TokenID:         fingerprint(digest),
	}
	if auth != nil {
		entry.RequesterID = auth.RequesterID
		entry.SubjectID = auth.SubjectID
		entry.Purpose = auth.Purpose
		entry.InstitutionID = auth.InstitutionID
	}
	m.appendAuditLocked(entry)
	return nil
}

// Authorization returns a snapshot of a grant. The boolean is false when the
// ID is unknown.
func (m *Manager) Authorization(id string) (Authorization, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	auth, ok := m.authorizations[id]
	if !ok {
		return Authorization{}, false
	}
	return *auth, true
}

// AuditEntries returns a copy of the append-only, hash-chained query audit
// ledger. Mutating the returned slice or its records cannot change the ledger.
func (m *Manager) AuditEntries() []AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditEntry, len(m.audit))
	copy(out, m.audit)
	return out
}

// AuditLog is an alias for AuditEntries.
func (m *Manager) AuditLog() []AuditEntry {
	return m.AuditEntries()
}

func normalizeAuthorizationRequest(req AuthorizationRequest) (AuthorizationRequest, error) {
	req.RequesterID = strings.TrimSpace(req.RequesterID)
	req.SubjectID = strings.TrimSpace(req.SubjectID)
	req.Purpose = strings.TrimSpace(req.Purpose)
	req.InstitutionID = strings.TrimSpace(req.InstitutionID)
	if req.RequesterID == "" {
		return AuthorizationRequest{}, fmt.Errorf("%w: requester ID is required", ErrInvalidRequest)
	}
	if req.SubjectID == "" {
		return AuthorizationRequest{}, fmt.Errorf("%w: subject ID is required", ErrInvalidRequest)
	}
	if req.Purpose == "" {
		return AuthorizationRequest{}, fmt.Errorf("%w: purpose is required", ErrInvalidRequest)
	}
	if req.InstitutionID == "" {
		return AuthorizationRequest{}, fmt.Errorf("%w: institution ID is required", ErrInvalidRequest)
	}
	if req.MaxQueries < 0 {
		return AuthorizationRequest{}, fmt.Errorf("%w: max queries cannot be negative", ErrInvalidRequest)
	}
	if req.MaxQueries == 0 {
		req.MaxQueries = 1
	}
	return req, nil
}

func randomID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func newToken() (string, [sha256.Size]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("netsec: generate token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, sha256.Sum256([]byte(token)), nil
}

func fingerprint(digest [sha256.Size]byte) string {
	return hex.EncodeToString(digest[:8])
}
