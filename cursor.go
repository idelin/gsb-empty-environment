package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	cursorVersion = 1
	cursorPrefix  = "pc1"

	// DefaultCursorTTL keeps pagination cursors short lived by default.
	DefaultCursorTTL = 5 * time.Minute

	maxCursorSize     = 32 << 10
	maxSortKeyValues  = 32
	maxSortKeyValue   = 512
	maxUniqueKeySize  = 1024
	maxTenantIDSize   = 512
	maxQueryDigestLen = 128
	cursorNonceSize   = 16
	clockSkew         = time.Minute
)

var (
	// ErrInvalidConfig reports an unusable CursorManager configuration.
	ErrInvalidConfig = errors.New("cursor: invalid configuration")
	// ErrInvalidScope reports an unusable request scope.
	ErrInvalidScope = errors.New("cursor: invalid scope")
	// ErrInvalidPosition reports an invalid pagination position.
	ErrInvalidPosition = errors.New("cursor: invalid position")
	// ErrInvalidCursor reports a malformed, tampered, or unauthenticated cursor.
	ErrInvalidCursor = errors.New("cursor: invalid cursor")
	// ErrExpiredCursor reports a cursor whose signed expiration has passed.
	ErrExpiredCursor = errors.New("cursor: expired cursor")
	// ErrReplayedCursor reports reuse of a single-use cursor.
	ErrReplayedCursor = errors.New("cursor: replayed cursor")
	// ErrScopeMismatch is the base error for a cursor issued for another request.
	ErrScopeMismatch = errors.New("cursor: scope mismatch")
	// ErrTenantMismatch reports a cursor issued for another tenant.
	ErrTenantMismatch = fmt.Errorf("%w: tenant", ErrScopeMismatch)
	// ErrQueryMismatch reports a cursor issued for different query conditions.
	ErrQueryMismatch = fmt.Errorf("%w: query", ErrScopeMismatch)
)

// CursorConfig configures a CursorManager.
type CursorConfig struct {
	// Secret is the server-side authentication key. Keep it out of clients and
	// use at least 32 random bytes in production.
	Secret []byte

	// TTL is the maximum cursor lifetime. Zero uses DefaultCursorTTL.
	TTL time.Duration

	// Now is used for expiration checks. Zero uses time.Now.
	Now func() time.Time

	// SingleUse makes each cursor consumable once by this manager. In a
	// multi-instance deployment, place an equivalent replay check in shared
	// storage or rely on the short TTL instead.
	SingleUse bool
}

// Scope describes the list request a cursor is allowed to resume. Query
// normally contains the normalized filters and ordering specification. It
// must not contain tenant-specific data beyond TenantID.
type Scope struct {
	TenantID string
	Query    any
}

// Position points immediately after the last row returned on a page.
//
// SortKey contains the values of the non-unique ORDER BY expressions. The
// service must append UniqueKey to ORDER BY as its final unique tie-breaker,
// for example: ORDER BY created_at DESC, id ASC. UniqueKey must be stable and
// unique within the tenant; encode a composite key as one canonical value if
// the table has a composite primary key.
type Position struct {
	SortKey   []string
	UniqueKey string
	Offset    int64
}

// CursorManager issues and authenticates tenant-bound pagination cursors.
type CursorManager struct {
	secret    []byte
	ttl       time.Duration
	now       func() time.Time
	singleUse bool

	mu       sync.Mutex
	consumed map[[sha256.Size]byte]time.Time
}

type cursorClaims struct {
	Version   int      `json:"v"`
	TenantID  string   `json:"t"`
	QueryHash string   `json:"q"`
	SortKey   []string `json:"k"`
	UniqueKey string   `json:"u"`
	Offset    int64    `json:"o"`
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp"`
	Nonce     string   `json:"n"`
}

type fingerprintInput struct {
	TenantID string `json:"tenant_id"`
	Query    any    `json:"query"`
}

// NewCursorManager validates cfg and creates an authenticated cursor codec.
func NewCursorManager(cfg CursorConfig) (*CursorManager, error) {
	if len(cfg.Secret) == 0 {
		return nil, fmt.Errorf("%w: secret must not be empty", ErrInvalidConfig)
	}
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = DefaultCursorTTL
	}
	if ttl < 0 {
		return nil, fmt.Errorf("%w: TTL must be positive", ErrInvalidConfig)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &CursorManager{
		secret:    append([]byte(nil), cfg.Secret...),
		ttl:       ttl,
		now:       now,
		singleUse: cfg.SingleUse,
		consumed:  make(map[[sha256.Size]byte]time.Time),
	}, nil
}

// QueryFingerprint returns a deterministic digest binding a normalized query
// to a tenant. Maps are canonicalized by encoding/json's sorted map keys;
// callers should pass the same value shape for equivalent requests.
func QueryFingerprint(tenantID string, query any) (string, error) {
	if err := validateTenantID(tenantID); err != nil {
		return "", err
	}

	encoded, err := json.Marshal(fingerprintInput{
		TenantID: tenantID,
		Query:    query,
	})
	if err != nil {
		return "", fmt.Errorf("%w: query cannot be encoded: %v", ErrInvalidScope, err)
	}

	digest := sha256.New()
	_, _ = digest.Write([]byte("netsec/query-v1\x00"))
	_, _ = digest.Write(encoded)
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil)), nil
}

// Encode authenticates a position and returns a tamper-evident cursor string.
func (m *CursorManager) Encode(scope Scope, position Position) (string, error) {
	if m == nil {
		return "", fmt.Errorf("%w: nil manager", ErrInvalidConfig)
	}
	if err := validateScope(scope); err != nil {
		return "", err
	}
	position, err := validatePosition(position)
	if err != nil {
		return "", err
	}

	fingerprint, err := QueryFingerprint(scope.TenantID, scope.Query)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, cursorNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("cursor: generate nonce: %w", err)
	}

	now := m.now()
	issuedAt := now.UnixNano()
	expiresAt := now.Add(m.ttl).UnixNano()
	claims := cursorClaims{
		Version:   cursorVersion,
		TenantID:  scope.TenantID,
		QueryHash: fingerprint,
		SortKey:   position.SortKey,
		UniqueKey: position.UniqueKey,
		Offset:    position.Offset,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
		Nonce:     base64.RawURLEncoding.EncodeToString(nonce),
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("cursor: encode claims: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	signed := cursorPrefix + "." + body
	cursor := signed + "." + base64.RawURLEncoding.EncodeToString(m.mac(signed))
	if len(cursor) > maxCursorSize {
		return "", fmt.Errorf("%w: cursor is too large", ErrInvalidPosition)
	}
	return cursor, nil
}

// Decode authenticates a cursor and verifies that it belongs to scope. It
// rejects malformed, tampered, expired, replayed, or differently scoped
// cursors. Callers that accept a safe reset should use ResumeOrStart.
func (m *CursorManager) Decode(scope Scope, cursor string) (Position, error) {
	if m == nil {
		return Position{}, fmt.Errorf("%w: nil manager", ErrInvalidConfig)
	}
	if err := validateScope(scope); err != nil {
		return Position{}, err
	}

	claims, err := m.open(cursor)
	if err != nil {
		return Position{}, err
	}

	now := m.now()
	if !now.Before(time.Unix(0, claims.ExpiresAt)) {
		return Position{}, fmt.Errorf("%w: expired at %s", ErrExpiredCursor, time.Unix(0, claims.ExpiresAt).UTC().Format(time.RFC3339Nano))
	}

	// These comparisons are constant time even though both values are signed;
	// this keeps the caller from turning scope mismatches into a timing oracle.
	if subtle.ConstantTimeCompare([]byte(claims.TenantID), []byte(scope.TenantID)) != 1 {
		return Position{}, ErrTenantMismatch
	}
	fingerprint, err := QueryFingerprint(scope.TenantID, scope.Query)
	if err != nil {
		return Position{}, err
	}
	if subtle.ConstantTimeCompare([]byte(claims.QueryHash), []byte(fingerprint)) != 1 {
		return Position{}, ErrQueryMismatch
	}

	position, err := validatePosition(Position{
		SortKey:   claims.SortKey,
		UniqueKey: claims.UniqueKey,
		Offset:    claims.Offset,
	})
	if err != nil {
		return Position{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}

	if m.singleUse && !m.consume(cursor, time.Unix(0, claims.ExpiresAt), now) {
		return Position{}, ErrReplayedCursor
	}
	return position, nil
}

// ResumeOrStart is the safe fallback variant of Decode. It returns ok=false
// for an empty, malformed, expired, replayed, or differently scoped cursor,
// which means the caller should start from offset zero. It never returns a
// position from another tenant or a different query.
func (m *CursorManager) ResumeOrStart(scope Scope, cursor string) (position Position, ok bool) {
	if cursor == "" || m == nil {
		return Position{}, false
	}
	position, err := m.Decode(scope, cursor)
	if err != nil {
		return Position{}, false
	}
	return position, true
}

func (m *CursorManager) open(cursor string) (cursorClaims, error) {
	var claims cursorClaims
	if cursor == "" || len(cursor) > maxCursorSize {
		return claims, fmt.Errorf("%w: malformed token", ErrInvalidCursor)
	}

	parts := strings.Split(cursor, ".")
	if len(parts) != 3 || parts[0] != cursorPrefix || parts[1] == "" || parts[2] == "" {
		return claims, fmt.Errorf("%w: malformed token envelope", ErrInvalidCursor)
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size {
		return claims, fmt.Errorf("%w: malformed signature", ErrInvalidCursor)
	}
	signed := parts[0] + "." + parts[1]
	if !hmac.Equal(signature, m.mac(signed)) {
		return claims, fmt.Errorf("%w: signature verification failed", ErrInvalidCursor)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, fmt.Errorf("%w: malformed payload", ErrInvalidCursor)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil {
		return claims, fmt.Errorf("%w: malformed claims", ErrInvalidCursor)
	}
	if err := ensureEOF(decoder); err != nil {
		return claims, fmt.Errorf("%w: trailing claims data", ErrInvalidCursor)
	}

	now := m.now()
	if claims.Version != cursorVersion {
		return claims, fmt.Errorf("%w: unsupported version", ErrInvalidCursor)
	}
	if err := validateTenantID(claims.TenantID); err != nil {
		return claims, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	if claims.QueryHash == "" || len(claims.QueryHash) > maxQueryDigestLen {
		return claims, fmt.Errorf("%w: malformed query digest", ErrInvalidCursor)
	}
	if claims.ExpiresAt <= claims.IssuedAt {
		return claims, fmt.Errorf("%w: invalid validity window", ErrInvalidCursor)
	}
	if time.Unix(0, claims.IssuedAt).After(now.Add(clockSkew)) {
		return claims, fmt.Errorf("%w: issue time is in the future", ErrInvalidCursor)
	}
	if claims.Nonce == "" {
		return claims, fmt.Errorf("%w: missing nonce", ErrInvalidCursor)
	}

	return claims, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (m *CursorManager) mac(message string) []byte {
	h := hmac.New(sha256.New, m.secret)
	_, _ = h.Write([]byte(message))
	return h.Sum(nil)
}

func (m *CursorManager) consume(cursor string, expiresAt, now time.Time) bool {
	key := sha256.Sum256([]byte(cursor))

	m.mu.Lock()
	defer m.mu.Unlock()

	for seenKey, seenExpiry := range m.consumed {
		if !now.Before(seenExpiry) {
			delete(m.consumed, seenKey)
		}
	}
	if _, ok := m.consumed[key]; ok {
		return false
	}
	m.consumed[key] = expiresAt
	return true
}

func validateScope(scope Scope) error {
	if err := validateTenantID(scope.TenantID); err != nil {
		return err
	}
	return nil
}

func validateTenantID(tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("%w: tenant ID must not be empty", ErrInvalidScope)
	}
	if len(tenantID) > maxTenantIDSize {
		return fmt.Errorf("%w: tenant ID is too long", ErrInvalidScope)
	}
	if strings.IndexByte(tenantID, 0) >= 0 {
		return fmt.Errorf("%w: tenant ID contains NUL", ErrInvalidScope)
	}
	if !utf8.ValidString(tenantID) {
		return fmt.Errorf("%w: tenant ID is not valid UTF-8", ErrInvalidScope)
	}
	return nil
}

func validatePosition(position Position) (Position, error) {
	if position.UniqueKey == "" {
		return Position{}, fmt.Errorf("%w: unique tie-breaker is required", ErrInvalidPosition)
	}
	if len(position.UniqueKey) > maxUniqueKeySize {
		return Position{}, fmt.Errorf("%w: unique tie-breaker is too long", ErrInvalidPosition)
	}
	if !utf8.ValidString(position.UniqueKey) {
		return Position{}, fmt.Errorf("%w: unique tie-breaker is not valid UTF-8", ErrInvalidPosition)
	}
	if position.Offset < 0 {
		return Position{}, fmt.Errorf("%w: offset must not be negative", ErrInvalidPosition)
	}
	if len(position.SortKey) > maxSortKeyValues {
		return Position{}, fmt.Errorf("%w: too many sort key values", ErrInvalidPosition)
	}
	for index, value := range position.SortKey {
		if len(value) > maxSortKeyValue {
			return Position{}, fmt.Errorf("%w: sort key value %d is too long", ErrInvalidPosition, index)
		}
		if !utf8.ValidString(value) {
			return Position{}, fmt.Errorf("%w: sort key value %d is not valid UTF-8", ErrInvalidPosition, index)
		}
	}

	position.SortKey = append([]string(nil), position.SortKey...)
	return position, nil
}
