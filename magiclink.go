// Package netsec provides short-lived, user-bound, one-time magic links for a
// Go authentication service.
//
// A token is authenticated with HMAC-SHA256 and contains the user ID, session
// intent, optional session ID, issue time, expiry, and a random token ID.
// Probe validates a token without changing authentication state. Consume
// requires proof of an explicit same-origin POST and atomically spends the
// token before returning the authenticated claims.
//
// GET requests, link-preview user agents, and Referer headers never
// authenticate a user. In particular, a mail scanner may fetch a magic link
// repeatedly without spending it.
package netsec

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	tokenPrefix = "ml1"
	tokenDomain = "example.org/netsec/q0314/magiclink/v1\x00"

	defaultTTL     = 10 * time.Minute
	maxTokenLength = 4096
)

var (
	// ErrInvalidToken is the common classification for malformed, forged, or
	// otherwise untrustworthy tokens.
	ErrInvalidToken = errors.New("magiclink: invalid token")

	// ErrTokenExpired indicates that the token's signed expiry has passed.
	ErrTokenExpired = errors.New("magiclink: token expired")

	// ErrTokenNotYetValid indicates that the issue time is implausibly in the
	// future.
	ErrTokenNotYetValid = errors.New("magiclink: token not yet valid")

	// ErrTokenConsumed indicates that the token was already spent.
	ErrTokenConsumed = errors.New("magiclink: token already consumed")

	// ErrBindingMismatch indicates that the token belongs to another user,
	// intent, or session than the caller expected.
	ErrBindingMismatch = errors.New("magiclink: token binding mismatch")

	// ErrInvalidBinding indicates that a user ID, intent, or session ID cannot
	// be represented in a token.
	ErrInvalidBinding = errors.New("magiclink: invalid binding")

	// ErrUntrustedAction indicates that the request does not prove an explicit
	// same-origin user action.
	ErrUntrustedAction = errors.New("magiclink: untrusted user action")

	// ErrOriginNotConfigured indicates that no trusted origin was available
	// against which to check the action.
	ErrOriginNotConfigured = errors.New("magiclink: trusted origin not configured")

	// ErrInvalidConfig indicates an unusable Service configuration.
	ErrInvalidConfig = errors.New("magiclink: invalid configuration")
)

// Config configures a Service.
type Config struct {
	// Secret is the HMAC key. Use at least 32 random bytes, shared only with
	// the authentication service that verifies tokens.
	Secret []byte

	// TTL is the token lifetime. Zero uses a ten minute default. Magic links
	// should normally live for only a few minutes.
	TTL time.Duration

	// Now optionally supplies a clock, primarily for tests.
	Now func() time.Time

	// Store records one-time consumption. If nil, a process-local MemoryStore
	// is used. A multi-process deployment must supply a shared atomic store.
	Store Store

	// AllowedOrigins lists exact trusted origins, for example
	// "https://auth.example.com". The Origin header must match one of these
	// values for a token to be consumed. If empty, the HTTP handler falls back
	// to the request's own scheme and Host; direct Service.Consume calls
	// require at least one configured origin.
	AllowedOrigins []string

	// ClockSkew tolerates a small issue-time difference when validating a
	// token. It does not extend a token's expiry.
	ClockSkew time.Duration
}

// Binding identifies the user, session intent, and optional session to which a
// token is bound.
type Binding struct {
	UserID    string
	Intent    string
	SessionID string
}

// Claims are the authenticated contents of a magic-link token.
type Claims struct {
	TokenID   string
	UserID    string
	Intent    string
	SessionID string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Binding returns the claims' binding values.
func (c Claims) Binding() Binding {
	return Binding{
		UserID:    c.UserID,
		Intent:    c.Intent,
		SessionID: c.SessionID,
	}
}

// IssuedToken is the value placed in the email plus the claims it carries.
type IssuedToken struct {
	Value  string
	Claims Claims
}

// Action is the evidence that a request represents an explicit user action.
// Method must be POST and Origin must exactly match a trusted origin. A
// Sec-Fetch-Site header, when present, provides an additional rejection check;
// it is never the sole reason to trust a request.
//
// User-Agent and Referer are deliberately absent: both can be supplied by mail
// scanners or attackers and must not be used as proof of user action.
type Action struct {
	Method       string
	Origin       string
	SecFetchSite string
}

// Service issues, probes, and consumes magic-link tokens.
type Service struct {
	key            []byte
	ttl            time.Duration
	now            func() time.Time
	store          Store
	allowedOrigins []string
	clockSkew      time.Duration
}

// NewService validates cfg and returns a magic-link service.
func NewService(cfg Config) (*Service, error) {
	if len(cfg.Secret) == 0 {
		return nil, fmt.Errorf("%w: empty secret", ErrInvalidConfig)
	}
	if cfg.TTL < 0 {
		return nil, fmt.Errorf("%w: negative TTL", ErrInvalidConfig)
	}
	if cfg.ClockSkew < 0 {
		return nil, fmt.Errorf("%w: negative clock skew", ErrInvalidConfig)
	}

	ttl := cfg.TTL
	if ttl == 0 {
		ttl = defaultTTL
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	origins := make([]string, 0, len(cfg.AllowedOrigins))
	seen := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, raw := range cfg.AllowedOrigins {
		origin, err := canonicalOrigin(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: allowed origin %q: %v", ErrInvalidConfig, raw, err)
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}

	store := cfg.Store
	if store == nil {
		store = newMemoryStore(now)
	}

	return &Service{
		key:            append([]byte(nil), cfg.Secret...),
		ttl:            ttl,
		now:            now,
		store:          store,
		allowedOrigins: origins,
		clockSkew:      cfg.ClockSkew,
	}, nil
}

// Issue creates a token for userID and intent using the configured lifetime.
func (s *Service) Issue(userID, intent string) (string, error) {
	issued, err := s.IssueFor(Binding{UserID: userID, Intent: intent})
	if err != nil {
		return "", err
	}
	return issued.Value, nil
}

// IssueFor creates a token for an explicit binding. SessionID is optional;
// when set it is signed into the token and can be required again at
// consumption time.
func (s *Service) IssueFor(binding Binding) (IssuedToken, error) {
	if err := validateBinding(binding); err != nil {
		return IssuedToken{}, err
	}

	tokenID, err := randomID()
	if err != nil {
		return IssuedToken{}, fmt.Errorf("magiclink: generate token id: %w", err)
	}

	now := s.now()
	issuedAt := now.Truncate(time.Nanosecond)
	expiresAt := issuedAt.Add(s.ttl)

	payload := tokenPayload{
		Version:   1,
		TokenID:   tokenID,
		UserID:    binding.UserID,
		Intent:    binding.Intent,
		SessionID: binding.SessionID,
		IssuedAt:  issuedAt.UnixNano(),
		ExpiresAt: expiresAt.UnixNano(),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return IssuedToken{}, fmt.Errorf("magiclink: encode token: %w", err)
	}

	payloadPart := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signaturePart := base64.RawURLEncoding.EncodeToString(s.sign(payloadJSON))
	value := tokenPrefix + "." + payloadPart + "." + signaturePart

	return IssuedToken{
		Value: value,
		Claims: Claims{
			TokenID:   tokenID,
			UserID:    binding.UserID,
			Intent:    binding.Intent,
			SessionID: binding.SessionID,
			IssuedAt:  issuedAt,
			ExpiresAt: expiresAt,
		},
	}, nil
}

// Probe validates a token and reports its claims without spending it. A GET
// confirmation page should use Probe. Probe is safe to call any number of
// times and never marks the token as consumed.
func (s *Service) Probe(ctx context.Context, rawToken string) (Claims, error) {
	claims, err := s.parse(rawToken)
	if err != nil {
		return Claims{}, err
	}
	consumed, err := s.store.IsConsumed(ctx, claims.TokenID)
	if err != nil {
		return Claims{}, storeError(err)
	}
	if consumed {
		return Claims{}, ErrTokenConsumed
	}
	return claims, nil
}

// Consume validates the token and trusted action, checks the caller's expected
// binding, and atomically spends the token. Exactly one concurrent caller can
// succeed.
//
// A zero field in expected means "do not check this field". Claims embedded in
// the signed token are always validated; expected lets the authentication
// service additionally require the token to match an established pending
// session.
func (s *Service) Consume(ctx context.Context, rawToken string, expected Binding, action Action) (Claims, error) {
	return s.consume(ctx, rawToken, expected, action, s.allowedOrigins)
}

// ConsumeRequest is the HTTP integration path. It builds an Action from the
// request and validates the origin against configured origins, or against the
// request's own scheme and Host when no explicit origins are configured.
func (s *Service) ConsumeRequest(ctx context.Context, r *http.Request, rawToken string, expected Binding) (Claims, error) {
	if r == nil {
		return Claims{}, ErrUntrustedAction
	}
	origins := s.allowedOrigins
	if len(origins) == 0 {
		origin, err := requestOrigin(r)
		if err != nil {
			return Claims{}, ErrUntrustedAction
		}
		origins = []string{origin}
	}
	action, err := actionFromRequest(r)
	if err != nil {
		return Claims{}, err
	}
	return s.consume(ctx, rawToken, expected, action, origins)
}

func (s *Service) consume(ctx context.Context, rawToken string, expected Binding, action Action, allowedOrigins []string) (Claims, error) {
	if err := validateAction(action, allowedOrigins); err != nil {
		return Claims{}, err
	}

	claims, err := s.parse(rawToken)
	if err != nil {
		return Claims{}, err
	}
	if err := matchesBinding(claims, expected); err != nil {
		return Claims{}, err
	}

	fresh, err := s.store.MarkConsumed(ctx, claims.TokenID, claims.ExpiresAt)
	if err != nil {
		return Claims{}, storeError(err)
	}
	if !fresh {
		return Claims{}, ErrTokenConsumed
	}
	return claims, nil
}

type tokenPayload struct {
	Version   int    `json:"v"`
	TokenID   string `json:"jti"`
	UserID    string `json:"sub"`
	Intent    string `json:"int"`
	SessionID string `json:"sid,omitempty"`
	IssuedAt  int64  `json:"iat_ns"`
	ExpiresAt int64  `json:"exp_ns"`
}

func (s *Service) parse(rawToken string) (Claims, error) {
	if rawToken == "" || len(rawToken) > maxTokenLength {
		return Claims{}, ErrInvalidToken
	}

	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix {
		return Claims{}, ErrInvalidToken
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payloadJSON) == 0 {
		return Claims{}, ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size {
		return Claims{}, ErrInvalidToken
	}
	if !hmac.Equal(signature, s.sign(payloadJSON)) {
		return Claims{}, ErrInvalidToken
	}

	var payload tokenPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return Claims{}, ErrInvalidToken
	}
	if payload.Version != 1 ||
		payload.TokenID == "" ||
		payload.ExpiresAt <= payload.IssuedAt ||
		payload.IssuedAt <= 0 {
		return Claims{}, ErrInvalidToken
	}
	if err := validateBinding(Binding{
		UserID:    payload.UserID,
		Intent:    payload.Intent,
		SessionID: payload.SessionID,
	}); err != nil {
		return Claims{}, ErrInvalidToken
	}

	issuedAt := time.Unix(0, payload.IssuedAt)
	expiresAt := time.Unix(0, payload.ExpiresAt)
	now := s.now()

	if !now.Before(expiresAt) {
		return Claims{}, ErrTokenExpired
	}
	if issuedAt.After(now.Add(s.clockSkew)) {
		return Claims{}, ErrTokenNotYetValid
	}

	return Claims{
		TokenID:   payload.TokenID,
		UserID:    payload.UserID,
		Intent:    payload.Intent,
		SessionID: payload.SessionID,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(tokenDomain))
	mac.Write(payload)
	return mac.Sum(nil)
}

func randomID() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func validateBinding(binding Binding) error {
	if err := validateClaimString("user id", binding.UserID, 512); err != nil {
		return err
	}
	if err := validateClaimString("intent", binding.Intent, 128); err != nil {
		return err
	}
	if binding.SessionID != "" {
		if err := validateClaimString("session id", binding.SessionID, 512); err != nil {
			return err
		}
	}
	return nil
}

func validateClaimString(name, value string, max int) error {
	switch {
	case strings.TrimSpace(value) == "":
		return fmt.Errorf("%w: empty %s", ErrInvalidBinding, name)
	case len(value) > max:
		return fmt.Errorf("%w: %s too long", ErrInvalidBinding, name)
	case strings.ContainsAny(value, "\x00\r\n"):
		return fmt.Errorf("%w: %s contains control characters", ErrInvalidBinding, name)
	default:
		return nil
	}
}

func matchesBinding(claims Claims, expected Binding) error {
	if expected.UserID != "" && claims.UserID != expected.UserID {
		return ErrBindingMismatch
	}
	if expected.Intent != "" && claims.Intent != expected.Intent {
		return ErrBindingMismatch
	}
	if expected.SessionID != "" && claims.SessionID != expected.SessionID {
		return ErrBindingMismatch
	}
	return nil
}

func validateAction(action Action, allowedOrigins []string) error {
	if action.Method != http.MethodPost {
		return ErrUntrustedAction
	}
	if len(allowedOrigins) == 0 {
		return ErrOriginNotConfigured
	}

	origin, err := canonicalOrigin(action.Origin)
	if err != nil {
		return ErrUntrustedAction
	}
	matched := false
	for _, allowed := range allowedOrigins {
		if origin == allowed {
			matched = true
			break
		}
	}
	if !matched {
		return ErrUntrustedAction
	}

	switch strings.ToLower(strings.TrimSpace(action.SecFetchSite)) {
	case "", "same-origin", "same-site":
		return nil
	default:
		return ErrUntrustedAction
	}
}

func actionFromRequest(r *http.Request) (Action, error) {
	origins := r.Header.Values("Origin")
	sites := r.Header.Values("Sec-Fetch-Site")
	if len(origins) > 1 || len(sites) > 1 {
		return Action{}, ErrUntrustedAction
	}

	var origin, site string
	if len(origins) == 1 {
		origin = origins[0]
	}
	if len(sites) == 1 {
		site = sites[0]
	}
	return Action{
		Method:       r.Method,
		Origin:       origin,
		SecFetchSite: site,
	}, nil
}

func requestOrigin(r *http.Request) (string, error) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return canonicalOrigin(scheme + "://" + r.Host)
}

func canonicalOrigin(raw string) (string, error) {
	if raw == "" || strings.EqualFold(strings.TrimSpace(raw), "null") {
		return "", errors.New("empty origin")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Opaque != "" || u.User != nil || u.Host == "" {
		return "", errors.New("origin must contain only scheme and host")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("origin must not contain query or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("origin must not contain a path")
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("unsupported origin scheme")
	}

	hostname := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if hostname == "" {
		return "", errors.New("origin host is empty")
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}

	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return scheme + "://" + host, nil
}

func storeError(err error) error {
	return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
}
