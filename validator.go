package netsec

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// EventBackchannelLogout is the RFC 8417 event type required in a logout token.
const EventBackchannelLogout = "http://schemas.openid.net/event/backchannel-logout"

// Scope describes the sessions that a validated logout token authorizes the
// relying party to terminate.
type Scope uint8

const (
	// TerminateSession terminates only the session identified by sid.
	TerminateSession Scope = iota + 1
	// TerminateAllSessions terminates every session for the subject in sub.
	TerminateAllSessions
)

// String implements fmt.Stringer.
func (s Scope) String() string {
	switch s {
	case TerminateSession:
		return "session"
	case TerminateAllSessions:
		return "all-sessions"
	default:
		return "unknown"
	}
}

// Config configures a Validator. Issuer, at least one audience, and KeyFunc are
// required.
type Config struct {
	Issuer    string
	Audience  string
	Audiences []string
	KeyFunc   ContextKeyFunc

	// ReplayCache is shared across validations. If nil, each Validator gets a
	// private cache. Supply this when more than one Validator protects the same
	// issuer.
	ReplayCache *ReplayCache

	// Clock is used for exp/iat/nbf and replay timestamps. Defaults to time.Now.
	Clock func() time.Time
	// ClockSkew is leeway granted to exp, iat, and nbf. Defaults to zero.
	ClockSkew time.Duration
	// AllowedAlgorithms optionally restricts accepted JWS algorithms. An empty
	// list accepts every algorithm returned by SupportedAlgorithms.
	AllowedAlgorithms []string
	// MaxTokenSize bounds the compact token length. Defaults to 64 KiB.
	MaxTokenSize int
}

// LogoutToken is an authenticated and validated back-channel logout token.
type LogoutToken struct {
	Raw       string
	Header    Header
	Issuer    string
	Subject   string
	SessionID string
	TokenID   string
	Audience  []string
	IssuedAt  time.Time
	ExpiresAt time.Time
	Events    map[string]json.RawMessage
}

// Scope returns the termination scope dictated by the token. A token with sid
// terminates that session only, even when sub is also present. A token with
// only sub terminates all sessions for that subject.
func (t *LogoutToken) Scope() Scope {
	if t.SessionID != "" {
		return TerminateSession
	}
	return TerminateAllSessions
}

// AllSessions reports whether the token authorizes terminating all sessions
// for its subject.
func (t *LogoutToken) AllSessions() bool {
	return t.Scope() == TerminateAllSessions
}

// Validator verifies back-channel logout tokens for one issuer.
type Validator struct {
	issuer       string
	audiences    map[string]struct{}
	keyFunc      ContextKeyFunc
	replay       *ReplayCache
	now          func() time.Time
	clockSkew    time.Duration
	algorithms   map[string]struct{}
	maxTokenSize int
}

// NewValidator constructs a Validator from cfg.
func NewValidator(cfg Config) (*Validator, error) {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, fmt.Errorf("netsec: Issuer is required")
	}
	if cfg.KeyFunc == nil {
		return nil, fmt.Errorf("netsec: KeyFunc is required")
	}

	audiences := make(map[string]struct{})
	if cfg.Audience != "" {
		audiences[cfg.Audience] = struct{}{}
	}
	for _, audience := range cfg.Audiences {
		if audience != "" {
			audiences[audience] = struct{}{}
		}
	}
	if len(audiences) == 0 {
		return nil, fmt.Errorf("netsec: at least one Audience is required")
	}
	if cfg.ClockSkew < 0 {
		return nil, fmt.Errorf("netsec: ClockSkew must not be negative")
	}
	if cfg.MaxTokenSize < 0 {
		return nil, fmt.Errorf("netsec: MaxTokenSize must not be negative")
	}

	algorithms := make(map[string]struct{})
	if len(cfg.AllowedAlgorithms) == 0 {
		for _, algorithm := range SupportedAlgorithms() {
			algorithms[algorithm] = struct{}{}
		}
	} else {
		supported := make(map[string]struct{})
		for _, algorithm := range SupportedAlgorithms() {
			supported[algorithm] = struct{}{}
		}
		for _, algorithm := range cfg.AllowedAlgorithms {
			if _, ok := supported[algorithm]; !ok {
				return nil, fmt.Errorf("netsec: unsupported algorithm %q in AllowedAlgorithms", algorithm)
			}
			algorithms[algorithm] = struct{}{}
		}
	}

	now := cfg.Clock
	if now == nil {
		now = time.Now
	}
	replay := cfg.ReplayCache
	if replay == nil {
		replay = NewReplayCache()
	}
	maxTokenSize := cfg.MaxTokenSize
	if maxTokenSize == 0 {
		maxTokenSize = 64 << 10
	}

	return &Validator{
		issuer:       cfg.Issuer,
		audiences:    audiences,
		keyFunc:      cfg.KeyFunc,
		replay:       replay,
		now:          now,
		clockSkew:    cfg.ClockSkew,
		algorithms:   algorithms,
		maxTokenSize: maxTokenSize,
	}, nil
}

// Validate authenticates a compact logout token and enforces all logout-token
// constraints. Signature, issuer, and audience validation happen before the
// token's typ, event, subject/session, nonce, and replay semantics are
// considered.
func (v *Validator) Validate(ctx context.Context, rawToken string) (*LogoutToken, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(rawToken) == 0 || len(rawToken) > v.maxTokenSize {
		return nil, fmt.Errorf("%w: token length is invalid", ErrMalformedToken)
	}

	token, signature, err := parseToken(rawToken)
	if err != nil {
		return nil, err
	}
	if _, ok := v.algorithms[token.Header.Algorithm]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, token.Header.Algorithm)
	}

	key, err := v.keyFunc(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyUnavailable, err)
	}
	if err := verifySignature(token.Header.Algorithm, key, []byte(token.signing), signature); err != nil {
		return nil, err
	}

	issuer, err := requiredString(token.Claims, "iss")
	if err != nil {
		return nil, fmt.Errorf("%w: iss is missing or not a string", ErrIssuerMismatch)
	}
	if issuer != v.issuer {
		return nil, fmt.Errorf("%w: got %q, want %q", ErrIssuerMismatch, issuer, v.issuer)
	}

	audiences, err := parseAudience(token.Claims)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAudienceMismatch, err)
	}
	if !v.audienceAccepted(audiences) {
		return nil, fmt.Errorf("%w: got %v", ErrAudienceMismatch, audiences)
	}

	if !strings.EqualFold(token.Header.Type, "logout+jwt") {
		return nil, fmt.Errorf("%w: typ is %q", ErrInvalidTokenType, token.Header.Type)
	}

	events, err := requireBackchannelLogoutEvent(token.Claims)
	if err != nil {
		return nil, err
	}

	subject, hasSubject, err := optionalNonEmptyString(token.Claims, "sub")
	if err != nil {
		return nil, fmt.Errorf("%w: invalid sub: %v", ErrMissingSubjectOrSession, err)
	}
	sessionID, hasSession, err := optionalNonEmptyString(token.Claims, "sid")
	if err != nil {
		return nil, fmt.Errorf("%w: invalid sid: %v", ErrMissingSubjectOrSession, err)
	}
	if !hasSubject && !hasSession {
		return nil, ErrMissingSubjectOrSession
	}
	if _, present := token.Claims["nonce"]; present {
		return nil, ErrNoncePresent
	}

	tokenID, err := requiredString(token.Claims, "jti")
	if err != nil || tokenID == "" {
		return nil, ErrMissingTokenID
	}

	now := v.now()
	issuedAt, hasIssuedAt, err := numericDate(token.Claims, "iat")
	if err != nil {
		return nil, fmt.Errorf("%w: iat: %v", ErrMalformedToken, err)
	}
	if hasIssuedAt && now.Add(v.clockSkew).Before(issuedAt) {
		return nil, ErrTokenIssuedInFuture
	}
	expiresAt, hasExpiresAt, err := numericDate(token.Claims, "exp")
	if err != nil {
		return nil, fmt.Errorf("%w: exp: %v", ErrMalformedToken, err)
	}
	if hasExpiresAt && !now.Before(expiresAt.Add(v.clockSkew)) {
		return nil, ErrTokenExpired
	}
	notBefore, hasNotBefore, err := numericDate(token.Claims, "nbf")
	if err != nil {
		return nil, fmt.Errorf("%w: nbf: %v", ErrMalformedToken, err)
	}
	if hasNotBefore && now.Add(v.clockSkew).Before(notBefore) {
		return nil, ErrTokenNotYetValid
	}

	if v.replay.CheckAndRecord(issuer, tokenID, now) {
		return nil, fmt.Errorf("%w: issuer %q jti %q", ErrReplayDetected, issuer, tokenID)
	}

	return &LogoutToken{
		Raw:       rawToken,
		Header:    token.Header,
		Issuer:    issuer,
		Subject:   subject,
		SessionID: sessionID,
		TokenID:   tokenID,
		Audience:  audiences,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
		Events:    events,
	}, nil
}

func (v *Validator) audienceAccepted(audiences []string) bool {
	for _, audience := range audiences {
		if _, ok := v.audiences[audience]; ok {
			return true
		}
	}
	return false
}

func requiredString(claims map[string]any, name string) (string, error) {
	value, ok := claims[name]
	if !ok {
		return "", fmt.Errorf("claim %q is absent", name)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("claim %q is not a string", name)
	}
	return text, nil
}

func optionalNonEmptyString(claims map[string]any, name string) (string, bool, error) {
	value, present := claims[name]
	if !present {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("claim %q is not a string", name)
	}
	if text == "" {
		return "", false, fmt.Errorf("claim %q is empty", name)
	}
	return text, true, nil
}

func parseAudience(claims map[string]any) ([]string, error) {
	value, present := claims["aud"]
	if !present {
		return nil, fmt.Errorf("aud is absent")
	}

	switch typed := value.(type) {
	case string:
		if typed == "" {
			return nil, fmt.Errorf("aud is empty")
		}
		return []string{typed}, nil
	case []any:
		if len(typed) == 0 {
			return nil, fmt.Errorf("aud is empty")
		}
		audiences := make([]string, 0, len(typed))
		for _, item := range typed {
			audience, ok := item.(string)
			if !ok || audience == "" {
				return nil, fmt.Errorf("aud contains a non-string or empty member")
			}
			audiences = append(audiences, audience)
		}
		return audiences, nil
	default:
		return nil, fmt.Errorf("aud is not a string or string array")
	}
}

func numericDate(claims map[string]any, name string) (time.Time, bool, error) {
	value, present := claims[name]
	if !present {
		return time.Time{}, false, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return time.Time{}, false, fmt.Errorf("claim %q is not a number", name)
	}
	seconds, err := number.Float64()
	if err != nil || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return time.Time{}, false, fmt.Errorf("claim %q is not a finite number", name)
	}
	const maxUnixSeconds = float64(math.MaxInt64) / float64(time.Second)
	if seconds > maxUnixSeconds || seconds < -maxUnixSeconds {
		return time.Time{}, false, fmt.Errorf("claim %q is outside the supported timestamp range", name)
	}
	nanoseconds := int64(math.Round(seconds * float64(time.Second)))
	return time.Unix(0, nanoseconds), true, nil
}

func requireBackchannelLogoutEvent(claims map[string]any) (map[string]json.RawMessage, error) {
	raw, present := claims["events"]
	if !present {
		return nil, fmt.Errorf("%w: events is absent", ErrMissingLogoutEvent)
	}

	eventMap, ok := raw.(map[string]any)
	if !ok || eventMap == nil {
		return nil, fmt.Errorf("%w: events is not a JSON object", ErrMissingLogoutEvent)
	}

	eventMapJSON, err := json.Marshal(eventMap)
	if err != nil {
		return nil, fmt.Errorf("%w: events is not encodable", ErrMissingLogoutEvent)
	}

	var events map[string]json.RawMessage
	if err := json.Unmarshal(eventMapJSON, &events); err != nil || events == nil {
		return nil, fmt.Errorf("%w: events is not a JSON object", ErrMissingLogoutEvent)
	}

	event, present := events[EventBackchannelLogout]
	if !present {
		return nil, fmt.Errorf("%w: event %q is absent", ErrMissingLogoutEvent, EventBackchannelLogout)
	}

	var eventObject map[string]any
	if err := json.Unmarshal(event, &eventObject); err != nil || eventObject == nil {
		return nil, fmt.Errorf("%w: event %q is not a JSON object", ErrMissingLogoutEvent, EventBackchannelLogout)
	}
	return events, nil
}
