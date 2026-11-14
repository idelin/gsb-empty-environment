package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrInvalidToken is the common error for malformed, unsigned, incorrectly
// signed, expired, or otherwise unacceptable logout tokens.
var ErrInvalidToken = errors.New("netsec: invalid logout token")

// ErrUnknownIssuer is returned when the token issuer is not trusted.
var ErrUnknownIssuer = errors.New("netsec: unknown token issuer")

// ErrAudienceMismatch is returned when a token is not intended for this
// logout service's audience.
var ErrAudienceMismatch = errors.New("netsec: token audience mismatch")

// ErrTokenExpired is returned when the token is outside its short validity
// window.
var ErrTokenExpired = errors.New("netsec: token expired")

// ErrReplay is returned when a logout token has already been consumed.
var ErrReplay = errors.New("netsec: logout token already consumed")

// ErrTokenLifetime is returned when a token claims a validity period longer
// than the verifier permits.
var ErrTokenLifetime = errors.New("netsec: token lifetime exceeds limit")

const (
	// DefaultTokenTTL is the lifetime used for newly issued logout tokens.
	DefaultTokenTTL = 2 * time.Minute
	// DefaultMaxTokenTTL is the longest lifetime accepted by a verifier.
	DefaultMaxTokenTTL = 5 * time.Minute
	// DefaultClockSkew is the tolerance applied to iat/exp comparisons.
	DefaultClockSkew = 30 * time.Second
	// DefaultMaxTokenBytes is the largest compact token accepted.
	DefaultMaxTokenBytes = 1 << 20
)

const backChannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

// LogoutToken is the validated claim set of a signed logout token.
type LogoutToken struct {
	Issuer    string
	Audience  string
	Audiences []string
	SessionID string
	Subject   string
	TokenID   string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// AudienceValues returns every audience present in the token.
func (t *LogoutToken) AudienceValues() []string {
	if len(t.Audiences) > 0 {
		return append([]string(nil), t.Audiences...)
	}
	if t.Audience == "" {
		return nil
	}
	return []string{t.Audience}
}

type audienceClaim []string

func (a *audienceClaim) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single == "" {
			return errors.New("empty audience")
		}
		*a = []string{single}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return errors.New("audience must be a string or array of strings")
	}
	if len(multiple) == 0 {
		return errors.New("empty audience")
	}
	for _, audience := range multiple {
		if audience == "" {
			return errors.New("empty audience")
		}
	}
	*a = multiple
	return nil
}

func (a audienceClaim) MarshalJSON() ([]byte, error) {
	if len(a) == 1 {
		return json.Marshal(a[0])
	}
	return json.Marshal([]string(a))
}

type wireClaims struct {
	Issuer    string                     `json:"iss"`
	Audience  audienceClaim              `json:"aud"`
	Subject   string                     `json:"sub"`
	SessionID string                     `json:"sid"`
	TokenID   string                     `json:"jti"`
	IssuedAt  json.Number                `json:"iat"`
	ExpiresAt json.Number                `json:"exp"`
	Events    map[string]json.RawMessage `json:"events,omitempty"`
	Nonce     string                     `json:"nonce,omitempty"`
}

// IssueRequest describes a logout token to sign.
type IssueRequest struct {
	Audience  string
	Audiences []string
	SessionID string
	Subject   string
	TokenID   string
	IssuedAt  time.Time
	ExpiresAt time.Time
	TTL       time.Duration
}

// TokenIssuer signs logout tokens. It is safe for concurrent use.
type TokenIssuer struct {
	Issuer      string
	Keys        *KeySet
	TTL         time.Duration
	Now         func() time.Time
	TokenID     func() (string, error)
	MaxTokenTTL time.Duration
}

// TokenSigner and LogoutTokenIssuer are aliases for TokenIssuer.
type TokenSigner = TokenIssuer
type LogoutTokenIssuer = TokenIssuer

// NewTokenIssuer creates a signer for the supplied issuer identifier. The
// optional TTL argument overrides DefaultTokenTTL.
func NewTokenIssuer(issuer string, keys *KeySet, ttl ...time.Duration) (*TokenIssuer, error) {
	if strings.TrimSpace(issuer) == "" {
		return nil, errors.New("netsec: issuer is required")
	}
	if keys == nil || len(keys.KeyIDs()) == 0 {
		return nil, errors.New("netsec: at least one signing key is required")
	}
	if keys.ActiveKeyID() == "" {
		return nil, errors.New("netsec: key set has no active signing key")
	}
	issuerTTL := DefaultTokenTTL
	if len(ttl) > 1 {
		return nil, errors.New("netsec: at most one TTL may be supplied")
	}
	if len(ttl) == 1 {
		if ttl[0] <= 0 {
			return nil, errors.New("netsec: token TTL must be positive")
		}
		issuerTTL = ttl[0]
	}
	return &TokenIssuer{
		Issuer:      issuer,
		Keys:        keys,
		TTL:         issuerTTL,
		Now:         time.Now,
		MaxTokenTTL: DefaultMaxTokenTTL,
	}, nil
}

// NewLogoutTokenIssuer is an alias for NewTokenIssuer.
func NewLogoutTokenIssuer(issuer string, keys *KeySet, ttl ...time.Duration) (*TokenIssuer, error) {
	return NewTokenIssuer(issuer, keys, ttl...)
}

// NewIssuer is an alias for NewTokenIssuer.
func NewIssuer(issuer string, keys *KeySet, ttl ...time.Duration) (*TokenIssuer, error) {
	return NewTokenIssuer(issuer, keys, ttl...)
}

// Issue signs a token described by req and returns its compact JWS form.
func (i *TokenIssuer) Issue(req IssueRequest) (string, error) {
	token, err := i.build(req)
	if err != nil {
		return "", err
	}
	return i.Sign(token)
}

// IssueToken is an alias for Issue.
func (i *TokenIssuer) IssueToken(req IssueRequest) (string, error) {
	return i.Issue(req)
}

// IssueLogoutToken is a convenience method for the common single-audience
// case.
func (i *TokenIssuer) IssueLogoutToken(audience, sessionID, subject string) (string, error) {
	return i.Issue(IssueRequest{
		Audience:  audience,
		SessionID: sessionID,
		Subject:   subject,
	})
}

// IssueForSession is an alias for IssueLogoutToken.
func (i *TokenIssuer) IssueForSession(audience, sessionID, subject string) (string, error) {
	return i.IssueLogoutToken(audience, sessionID, subject)
}

// Sign encodes and signs an already-built claim set. Empty times are filled in
// from the issuer clock and TTL; an empty TokenID is generated.
func (i *TokenIssuer) Sign(token LogoutToken) (string, error) {
	if i == nil || i.Keys == nil {
		return "", errors.New("netsec: nil token issuer")
	}
	if token.Issuer == "" {
		token.Issuer = i.Issuer
	}
	if token.Issuer != i.Issuer {
		return "", fmt.Errorf("%w: issuer does not match signer identity", ErrUnknownIssuer)
	}
	if token.SessionID == "" || token.Subject == "" {
		return "", fmt.Errorf("%w: sid and sub are required", ErrInvalidToken)
	}

	audiences, err := mergeAudiences(token.Audience, token.Audiences)
	if err != nil {
		return "", err
	}
	now := i.now()
	if token.IssuedAt.IsZero() {
		token.IssuedAt = now.Truncate(time.Second)
	}
	if token.ExpiresAt.IsZero() {
		ttl := i.TTL
		if ttl <= 0 {
			ttl = DefaultTokenTTL
		}
		token.ExpiresAt = token.IssuedAt.Add(ttl)
	}
	if !token.ExpiresAt.After(token.IssuedAt) {
		return "", fmt.Errorf("%w: exp must be after iat", ErrInvalidToken)
	}
	if i.MaxTokenTTL > 0 && token.ExpiresAt.Sub(token.IssuedAt) > i.MaxTokenTTL {
		return "", fmt.Errorf("%w: maximum is %s", ErrTokenLifetime, i.MaxTokenTTL)
	}

	if token.TokenID == "" {
		generator := i.TokenID
		if generator == nil {
			generator = randomTokenID
		}
		token.TokenID, err = generator()
		if err != nil {
			return "", fmt.Errorf("netsec: generate token id: %w", err)
		}
	}
	if token.TokenID == "" {
		return "", errors.New("netsec: token id generator returned an empty value")
	}

	claims := wireClaims{
		Issuer:    token.Issuer,
		Audience:  audienceClaim(audiences),
		Subject:   token.Subject,
		SessionID: token.SessionID,
		TokenID:   token.TokenID,
		IssuedAt:  json.Number(strconv.FormatInt(token.IssuedAt.Unix(), 10)),
		ExpiresAt: json.Number(strconv.FormatInt(token.ExpiresAt.Unix(), 10)),
		Events: map[string]json.RawMessage{
			backChannelLogoutEvent: json.RawMessage(`{}`),
		},
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return i.Keys.signJWS(payload)
}

func (i *TokenIssuer) build(req IssueRequest) (LogoutToken, error) {
	if i == nil {
		return LogoutToken{}, errors.New("netsec: nil token issuer")
	}
	audiences, err := mergeAudiences(req.Audience, req.Audiences)
	if err != nil {
		return LogoutToken{}, err
	}
	if req.SessionID == "" || req.Subject == "" {
		return LogoutToken{}, fmt.Errorf("%w: sid and sub are required", ErrInvalidToken)
	}
	now := i.now()
	issuedAt := req.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = now.Truncate(time.Second)
	}
	expiresAt := req.ExpiresAt
	if expiresAt.IsZero() {
		ttl := req.TTL
		if ttl <= 0 {
			ttl = i.TTL
		}
		expiresAt = issuedAt.Add(ttl)
	}
	if !expiresAt.After(issuedAt) {
		return LogoutToken{}, fmt.Errorf("%w: exp must be after iat", ErrInvalidToken)
	}
	return LogoutToken{
		Issuer:    i.Issuer,
		Audience:  audiences[0],
		Audiences: audiences,
		SessionID: req.SessionID,
		Subject:   req.Subject,
		TokenID:   req.TokenID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

func (i *TokenIssuer) now() time.Time {
	if i != nil && i.Now != nil {
		return i.Now()
	}
	return time.Now()
}

// SignLogoutToken signs a token using the active key in keys.
func SignLogoutToken(keys *KeySet, token LogoutToken) (string, error) {
	if keys == nil {
		return "", errors.New("netsec: nil key set")
	}
	if token.Issuer == "" {
		return "", errors.New("netsec: issuer is required")
	}
	issuer := &TokenIssuer{
		Issuer: token.Issuer,
		Keys:   keys,
		TTL:    DefaultTokenTTL,
		Now:    time.Now,
	}
	return issuer.Sign(token)
}

// VerifierConfig configures a logout-token verifier. Issuers maps each
// trusted issuer identifier to the key set that may sign tokens for it.
type VerifierConfig struct {
	Audience     string
	Audiences    []string
	Issuer       string
	Keys         *KeySet
	Issuers      map[string]*KeySet
	MaxTokenTTL  time.Duration
	ClockSkew    time.Duration
	MaxTokenSize int
	ReplayCache  ReplayCache
	Now          func() time.Time
	Clock        func() time.Time
}

// Verifier validates logout tokens and atomically consumes them exactly once.
type Verifier struct {
	mu        sync.RWMutex
	issuers   map[string]*KeySet
	audiences map[string]struct{}
	maxTTL    time.Duration
	clockSkew time.Duration
	maxSize   int
	replay    ReplayCache
	now       func() time.Time
}

// LogoutTokenVerifier and TokenVerifier are aliases for Verifier.
type LogoutTokenVerifier = Verifier
type TokenVerifier = Verifier

// NewVerifier validates the configuration and returns a token verifier.
func NewVerifier(config VerifierConfig) (*Verifier, error) {
	audienceValues, err := mergeAudiences(config.Audience, config.Audiences)
	if err != nil {
		return nil, fmt.Errorf("netsec: verifier audience: %w", err)
	}
	audiences := make(map[string]struct{}, len(audienceValues))
	for _, audience := range audienceValues {
		audiences[audience] = struct{}{}
	}
	issuers := make(map[string]*KeySet, len(config.Issuers)+1)
	for issuer, keys := range config.Issuers {
		if strings.TrimSpace(issuer) == "" {
			return nil, errors.New("netsec: trusted issuer identifier is empty")
		}
		if keys == nil || len(keys.KeyIDs()) == 0 {
			return nil, fmt.Errorf("netsec: issuer %q has no verification keys", issuer)
		}
		issuers[issuer] = keys
	}
	if config.Issuer != "" {
		if strings.TrimSpace(config.Issuer) == "" {
			return nil, errors.New("netsec: trusted issuer identifier is empty")
		}
		if config.Keys == nil || len(config.Keys.KeyIDs()) == 0 {
			return nil, fmt.Errorf("netsec: issuer %q has no verification keys", config.Issuer)
		}
		if _, exists := issuers[config.Issuer]; exists {
			return nil, fmt.Errorf("netsec: issuer %q is configured twice", config.Issuer)
		}
		issuers[config.Issuer] = config.Keys
	}
	if len(issuers) == 0 {
		return nil, errors.New("netsec: at least one trusted issuer is required")
	}

	maxTTL := config.MaxTokenTTL
	if maxTTL == 0 {
		maxTTL = DefaultMaxTokenTTL
	}
	if maxTTL < 0 {
		return nil, errors.New("netsec: maximum token lifetime must be positive")
	}
	skew := config.ClockSkew
	if skew < 0 {
		return nil, errors.New("netsec: clock skew cannot be negative")
	}
	maxSize := config.MaxTokenSize
	if maxSize == 0 {
		maxSize = DefaultMaxTokenBytes
	}
	if maxSize < 1 {
		return nil, errors.New("netsec: maximum token size must be positive")
	}
	now := config.Now
	if now == nil {
		now = config.Clock
	}
	if now == nil {
		now = time.Now
	}
	replay := config.ReplayCache
	if replay == nil {
		replay = NewMemoryReplayCache()
	}
	return &Verifier{
		issuers:   issuers,
		audiences: audiences,
		maxTTL:    maxTTL,
		clockSkew: skew,
		maxSize:   maxSize,
		replay:    replay,
		now:       now,
	}, nil
}

// NewLogoutTokenVerifier is an alias for NewVerifier.
func NewLogoutTokenVerifier(config VerifierConfig) (*Verifier, error) {
	return NewVerifier(config)
}

// Verify validates raw and atomically consumes its token identifier. A
// second call with the same token, including concurrent calls, returns
// ErrReplay.
func (v *Verifier) Verify(raw string) (*LogoutToken, error) {
	token, err := v.Validate(raw)
	if err != nil {
		return nil, err
	}
	key := replayKey(token, raw)
	if !v.replay.Consume(key, token.ExpiresAt.Add(v.clockSkew)) {
		return nil, ErrReplay
	}
	return token, nil
}

// VerifyAndConsume is an alias for Verify.
func (v *Verifier) VerifyAndConsume(raw string) (*LogoutToken, error) {
	return v.Verify(raw)
}

// VerifyLogoutToken is an alias for Verify.
func (v *Verifier) VerifyLogoutToken(raw string) (*LogoutToken, error) {
	return v.Verify(raw)
}

// Validate performs all signature and claim checks without consuming the
// token. It is intended for diagnostics; production logout paths should call
// Verify or VerifyAndConsume.
func (v *Verifier) Validate(raw string) (*LogoutToken, error) {
	if v == nil {
		return nil, errors.New("netsec: nil verifier")
	}
	if len(raw) == 0 || len(raw) > v.maxSize {
		return nil, fmt.Errorf("%w: invalid token size", ErrInvalidToken)
	}
	header, payload, signingInput, signature, err := parseJWS(raw)
	if err != nil {
		return nil, err
	}
	if len(header.Critical) != 0 {
		return nil, fmt.Errorf("%w: unsupported critical header", ErrInvalidToken)
	}
	algorithm, ok := canonicalAlgorithm(header.Algorithm)
	if !ok || header.Algorithm != algorithm {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, header.Algorithm)
	}
	if header.KeyID == "" {
		return nil, fmt.Errorf("%w: kid is required", ErrInvalidToken)
	}
	if err := rejectDuplicateJSONKeys(payload); err != nil {
		return nil, err
	}
	var claims wireClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("%w: malformed claims", ErrInvalidToken)
	}
	if claims.Issuer == "" {
		return nil, fmt.Errorf("%w: iss is required", ErrInvalidToken)
	}

	keys, err := v.keysForIssuer(claims.Issuer)
	if err != nil {
		return nil, err
	}
	if keys == nil {
		return nil, ErrUnknownIssuer
	}
	entry, err := keys.lookup(header.KeyID, algorithm)
	if err != nil {
		return nil, err
	}
	if entry.algorithm != algorithm {
		return nil, fmt.Errorf("%w: token algorithm does not match registered key", ErrUnsupportedAlgorithm)
	}
	if err := verifyBytes(entry, algorithm, signingInput, signature); err != nil {
		return nil, fmt.Errorf("%w: signature verification failed", ErrInvalidToken)
	}

	issuedAt, err := numericDate(claims.IssuedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid iat", ErrInvalidToken)
	}
	expiresAt, err := numericDate(claims.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid exp", ErrInvalidToken)
	}
	if !expiresAt.After(issuedAt) {
		return nil, fmt.Errorf("%w: exp must be after iat", ErrInvalidToken)
	}
	now := v.now()
	if issuedAt.After(now.Add(v.clockSkew)) {
		return nil, fmt.Errorf("%w: iat is in the future", ErrInvalidToken)
	}
	if !expiresAt.After(now.Add(-v.clockSkew)) {
		return nil, fmt.Errorf("%w: exp=%s now=%s", ErrTokenExpired, expiresAt.UTC(), now.UTC())
	}
	if v.maxTTL > 0 && expiresAt.Sub(issuedAt) > v.maxTTL {
		return nil, fmt.Errorf("%w: lifetime=%s maximum=%s", ErrTokenLifetime, expiresAt.Sub(issuedAt), v.maxTTL)
	}
	if claims.Subject == "" || claims.SessionID == "" {
		return nil, fmt.Errorf("%w: sub and sid are required", ErrInvalidToken)
	}
	if claims.Nonce != "" {
		return nil, fmt.Errorf("%w: logout tokens must not contain nonce", ErrInvalidToken)
	}
	if !v.audienceAllowed(claims.Audience) {
		return nil, ErrAudienceMismatch
	}

	return &LogoutToken{
		Issuer:    claims.Issuer,
		Audience:  string(claims.Audience[0]),
		Audiences: append([]string(nil), claims.Audience...),
		SessionID: claims.SessionID,
		Subject:   claims.Subject,
		TokenID:   claims.TokenID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

// TrustsIssuer reports whether issuer has registered verification keys.
func (v *Verifier) TrustsIssuer(issuer string) bool {
	if v == nil {
		return false
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	keys, ok := v.issuers[issuer]
	return ok && keys != nil && keys.HasUsableKey()
}

// AddIssuer registers or replaces verification keys for issuer.
func (v *Verifier) AddIssuer(issuer string, keys *KeySet) error {
	if v == nil {
		return errors.New("netsec: nil verifier")
	}
	if strings.TrimSpace(issuer) == "" {
		return errors.New("netsec: issuer is required")
	}
	if keys == nil || len(keys.KeyIDs()) == 0 {
		return errors.New("netsec: verification keys are required")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.issuers == nil {
		v.issuers = make(map[string]*KeySet)
	}
	v.issuers[issuer] = keys
	return nil
}

// RemoveIssuer stops trusting issuer.
func (v *Verifier) RemoveIssuer(issuer string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.issuers, issuer)
}

func (v *Verifier) keysForIssuer(issuer string) (*KeySet, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	keys, ok := v.issuers[issuer]
	if !ok {
		return nil, ErrUnknownIssuer
	}
	return keys, nil
}

func (v *Verifier) audienceAllowed(audience audienceClaim) bool {
	if len(audience) == 0 {
		return false
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	for _, value := range audience {
		if _, ok := v.audiences[value]; ok {
			return true
		}
	}
	return false
}

func numericDate(number json.Number) (time.Time, error) {
	text := string(number)
	if text == "" {
		return time.Time{}, errors.New("missing numeric date")
	}
	if seconds, err := strconv.ParseInt(text, 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC(), nil
	}
	seconds, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return time.Time{}, err
	}
	whole, fraction := math.Modf(seconds)
	return time.Unix(int64(whole), int64(fraction*float64(time.Second))).UTC(), nil
}

func mergeAudiences(single string, multiple []string) ([]string, error) {
	seen := make(map[string]struct{}, len(multiple)+1)
	var audiences []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		audiences = append(audiences, value)
	}
	add(single)
	for _, value := range multiple {
		add(value)
	}
	if len(audiences) == 0 {
		return nil, errors.New("netsec: at least one audience is required")
	}
	sort.Strings(audiences)
	if single != "" && len(audiences) > 1 {
		// Keep the explicit single audience first for deterministic display.
		primary := strings.TrimSpace(single)
		for index, value := range audiences {
			if value == primary && index != 0 {
				copy(audiences[1:index+1], audiences[0:index])
				audiences[0] = primary
				break
			}
		}
	}
	return audiences, nil
}

func replayKey(token *LogoutToken, raw string) string {
	if token.TokenID != "" {
		return token.Issuer + "\x00jti\x00" + token.TokenID
	}
	digest := sha256.Sum256([]byte(raw))
	return token.Issuer + "\x00hash\x00" + hex.EncodeToString(digest[:])
}

func randomTokenID() (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer[:]), nil
}

// ReplayCache tracks consumed token identifiers. Consume must return true only
// for the first call for a key and must be safe for concurrent use.
type ReplayCache interface {
	Consume(key string, expiresAt time.Time) bool
}

// MemoryReplayCache is an in-process replay cache. It opportunistically
// removes expired entries and is safe for concurrent use.
type MemoryReplayCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
	now     func() time.Time
}

// NewMemoryReplayCache returns an empty replay cache.
func NewMemoryReplayCache() *MemoryReplayCache {
	return &MemoryReplayCache{
		entries: make(map[string]time.Time),
		now:     time.Now,
	}
}

// MemReplayCache is an alias for MemoryReplayCache.
type MemReplayCache = MemoryReplayCache

// NewMemReplayCache is an alias for NewMemoryReplayCache.
func NewMemReplayCache() *MemoryReplayCache {
	return NewMemoryReplayCache()
}

// Consume records key and reports whether it had not been seen before.
func (c *MemoryReplayCache) Consume(key string, expiresAt time.Time) bool {
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]time.Time)
	}
	if expiry, ok := c.entries[key]; ok && now.Before(expiry) {
		return false
	}
	for value, expiry := range c.entries {
		if !now.Before(expiry) {
			delete(c.entries, value)
		}
	}
	if !expiresAt.After(now) {
		expiresAt = now.Add(DefaultTokenTTL)
	}
	c.entries[key] = expiresAt
	return true
}

// Seen reports whether key is currently in the cache.
func (c *MemoryReplayCache) Seen(key string) bool {
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expiry, ok := c.entries[key]
	return ok && now.Before(expiry)
}

// Len returns the number of unexpired entries.
func (c *MemoryReplayCache) Len() int {
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, expiry := range c.entries {
		if now.Before(expiry) {
			count++
		}
	}
	return count
}
