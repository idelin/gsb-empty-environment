package netsec

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// TokenPath is the only path that may issue an IMDSv2 token.
	TokenPath = "/latest/api/token"

	// TokenHeader carries an IMDSv2 session token on metadata reads.
	TokenHeader = "X-aws-ec2-metadata-token"

	// TokenTTLHeader requests a token lifetime, in seconds.
	TokenTTLHeader = "X-aws-ec2-metadata-token-ttl-seconds"

	MinTokenTTL = 1
	MaxTokenTTL = 21600

	defaultMaxTokens = 10000
	maxTokenLength   = 4096
	maxPathDecodes   = 8
)

// forwardingHeaders are never accepted by the metadata guard. Their presence,
// including an empty value, indicates a request that was relayed from another
// client and must not receive container-scoped credentials.
var forwardingHeaders = []string{"X-Forwarded-For", "Forwarded", "Via"}

var (
	ErrNilRequest             = errors.New("netsec: nil request")
	ErrForwardedHeader        = errors.New("netsec: forwarding header is not allowed")
	ErrUnsafePath             = errors.New("netsec: unsafe metadata path")
	ErrEncodedSlash           = errors.New("netsec: percent-encoded path separator is not allowed")
	ErrPathTraversal          = errors.New("netsec: path traversal segment is not allowed")
	ErrNotTokenRequest        = errors.New("netsec: token requests must use " + TokenPath)
	ErrNotMetadataRequest     = errors.New("netsec: token endpoint is not a metadata read")
	ErrMethodNotAllowed       = errors.New("netsec: method is not allowed for this request")
	ErrContainerIDRequired    = errors.New("netsec: source container identifier is required")
	ErrTokenTTLMissing        = errors.New("netsec: token TTL header is required")
	ErrTokenTTLMalformed      = errors.New("netsec: token TTL must be a single integer")
	ErrTokenTTLOutOfRange     = errors.New("netsec: token TTL must be between 1 and 21600 seconds")
	ErrTokenTTLOnRead         = errors.New("netsec: token TTL header is not allowed on metadata reads")
	ErrTokenMissing           = errors.New("netsec: metadata token header is required")
	ErrTokenMalformed         = errors.New("netsec: metadata token is malformed")
	ErrTokenInvalid           = errors.New("netsec: metadata token is invalid")
	ErrTokenExpired           = errors.New("netsec: metadata token has expired")
	ErrTokenContainerMismatch = errors.New("netsec: metadata token belongs to a different container")
	// ErrTokenContainerMatch is kept as an alias for older callers.
	ErrTokenContainerMatch = ErrTokenContainerMismatch
	ErrTokenAlreadyBound   = errors.New("netsec: metadata token is already bound")
	ErrTokenStoreFull      = errors.New("netsec: metadata token store is full")
)

// Action describes what the forwarding layer should do after authorization.
type Action uint8

const (
	ActionDeny Action = iota
	ActionIssueToken
	ActionForwardMetadata
)

// ForwardingHeaderNames returns the headers rejected by the guard. A copy is
// returned so callers cannot weaken the policy by mutating package state.
func ForwardingHeaderNames() []string {
	return append([]string(nil), forwardingHeaders...)
}

func (a Action) String() string {
	switch a {
	case ActionIssueToken:
		return "issue-token"
	case ActionForwardMetadata:
		return "forward-metadata"
	default:
		return "deny"
	}
}

// Decision is the result of a successful authorization.
//
// For ActionIssueToken, Token is the newly issued token and ExpiresAt is its
// expiry time. For ActionForwardMetadata, the request may be relayed to IMDS.
type Decision struct {
	Action      Action
	Token       string
	TTL         time.Duration
	ExpiresAt   time.Time
	ContainerID string
}

// TTLSeconds returns the requested or granted token lifetime in whole seconds.
func (d Decision) TTLSeconds() int {
	return int(d.TTL / time.Second)
}

type tokenRecord struct {
	containerID string
	expiresAt   time.Time
}

// Guard validates IMDSv2 token acquisition and metadata reads and keeps the
// container binding for tokens. It is safe for concurrent use.
//
// The caller supplies the container identifier from a trusted source, such as
// the node agent's sandbox metadata. It must never come from request headers.
type Guard struct {
	mu        sync.Mutex
	tokens    map[string]tokenRecord
	maxTokens int
	now       func() time.Time
}

// Option configures a Guard.
type Option func(*Guard)

// WithClock replaces the clock used for token expiry. It is primarily useful
// in tests.
func WithClock(now func() time.Time) Option {
	return func(g *Guard) {
		if now != nil {
			g.now = now
		}
	}
}

// WithMaxTokens limits the number of live tokens retained by the guard.
func WithMaxTokens(max int) Option {
	return func(g *Guard) {
		if max > 0 {
			g.maxTokens = max
		}
	}
}

// NewGuard returns a Guard with an in-memory token registry.
func NewGuard(opts ...Option) *Guard {
	g := &Guard{
		tokens:    make(map[string]tokenRecord),
		maxTokens: defaultMaxTokens,
		now:       time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(g)
		}
	}
	return g
}

// New is an alias for NewGuard.
func New(opts ...Option) *Guard {
	return NewGuard(opts...)
}

// NewGuardWithClock returns a Guard that uses now for all expiry decisions.
func NewGuardWithClock(now func() time.Time) *Guard {
	return NewGuard(WithClock(now))
}

// Authorize performs a complete policy check for a request that is about to be
// forwarded to the instance metadata service.
//
// Token acquisition (PUT /latest/api/token) produces a locally issued token
// bound to containerID. A forwarding proxy that relays the token request to
// IMDS should instead use ValidateTokenRequest followed by BindToken with the
// token returned by IMDS.
func (g *Guard) Authorize(r *http.Request, containerID string) (Decision, error) {
	if err := validateCommon(r); err != nil {
		return Decision{}, err
	}

	if r.URL.Path == TokenPath {
		ttlSeconds, err := g.ValidateTokenRequest(r)
		if err != nil {
			return Decision{}, err
		}
		ttl := time.Duration(ttlSeconds) * time.Second
		token, expiresAt, err := g.IssueToken(containerID, ttl)
		if err != nil {
			return Decision{}, err
		}
		return Decision{
			Action:      ActionIssueToken,
			Token:       token,
			TTL:         ttl,
			ExpiresAt:   expiresAt,
			ContainerID: containerID,
		}, nil
	}

	if err := g.CheckMetadataRequest(r, containerID); err != nil {
		return Decision{}, err
	}
	return Decision{
		Action:      ActionForwardMetadata,
		ContainerID: containerID,
	}, nil
}

// ValidateTokenRequest checks that r is a well-formed IMDSv2 token request and
// returns the requested token lifetime. It does not consume or issue a token.
func (g *Guard) ValidateTokenRequest(r *http.Request) (int, error) {
	if err := validateCommon(r); err != nil {
		return 0, err
	}
	if r.URL.Path != TokenPath {
		return 0, ErrNotTokenRequest
	}
	if r.Method != http.MethodPut {
		return 0, fmt.Errorf("%w: token acquisition requires PUT", ErrMethodNotAllowed)
	}

	ttl, err := parseTTLHeaderValues(headerValues(r.Header, TokenTTLHeader))
	if err != nil {
		return 0, err
	}
	return ttl, nil
}

// CheckMetadataRequest checks that r is a metadata read carrying a live token
// bound to containerID. It does not modify the request.
func (g *Guard) CheckMetadataRequest(r *http.Request, containerID string) error {
	if err := validateCommon(r); err != nil {
		return err
	}
	if r.URL.Path == TokenPath {
		return ErrNotMetadataRequest
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return fmt.Errorf("%w: metadata reads require GET or HEAD", ErrMethodNotAllowed)
	}
	if len(headerValues(r.Header, TokenTTLHeader)) != 0 {
		return ErrTokenTTLOnRead
	}

	values := headerValues(r.Header, TokenHeader)
	if len(values) == 0 {
		return ErrTokenMissing
	}
	if len(values) != 1 {
		return ErrTokenMalformed
	}

	return g.ValidateToken(values[0], containerID)
}

// ValidateMetadataRequest is an alias for CheckMetadataRequest.
func (g *Guard) ValidateMetadataRequest(r *http.Request, containerID string) error {
	return g.CheckMetadataRequest(r, containerID)
}

// ValidateToken checks a token's lifetime and container binding.
func (g *Guard) ValidateToken(token, containerID string) error {
	if containerID == "" {
		return ErrContainerIDRequired
	}
	if !validTokenFormat(token) {
		return ErrTokenMalformed
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	record, ok := g.tokens[token]
	if !ok {
		return ErrTokenInvalid
	}
	if !now.Before(record.expiresAt) {
		return ErrTokenExpired
	}
	if record.containerID != containerID {
		return ErrTokenContainerMismatch
	}
	return nil
}

// IssueToken creates a random token bound to containerID for the requested
// lifetime. The lifetime must be a whole number of seconds between 1 and 21600.
func (g *Guard) IssueToken(containerID string, ttl time.Duration) (string, time.Time, error) {
	ttlSeconds, ok := durationSeconds(ttl)
	if !ok || ttlSeconds < MinTokenTTL || ttlSeconds > MaxTokenTTL {
		return "", time.Time{}, ErrTokenTTLOutOfRange
	}
	if containerID == "" {
		return "", time.Time{}, ErrContainerIDRequired
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	g.pruneLocked(now)
	if len(g.tokens) >= g.maxTokens {
		return "", time.Time{}, ErrTokenStoreFull
	}

	for {
		token, err := randomToken()
		if err != nil {
			return "", time.Time{}, err
		}
		if _, exists := g.tokens[token]; exists {
			continue
		}
		expiresAt := now.Add(time.Duration(ttlSeconds) * time.Second)
		g.tokens[token] = tokenRecord{
			containerID: containerID,
			expiresAt:   expiresAt,
		}
		return token, expiresAt, nil
	}
}

// BindToken registers a token issued by IMDS and binds it to containerID.
// This is the correct path for a proxy that forwards token acquisition to IMDS.
func (g *Guard) BindToken(token, containerID string, ttl time.Duration) (time.Time, error) {
	ttlSeconds, ok := durationSeconds(ttl)
	if !ok || ttlSeconds < MinTokenTTL || ttlSeconds > MaxTokenTTL {
		return time.Time{}, ErrTokenTTLOutOfRange
	}
	if containerID == "" {
		return time.Time{}, ErrContainerIDRequired
	}
	if !validTokenFormat(token) {
		return time.Time{}, ErrTokenMalformed
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	g.pruneLocked(now)
	if _, exists := g.tokens[token]; exists {
		return time.Time{}, ErrTokenAlreadyBound
	}
	if len(g.tokens) >= g.maxTokens {
		return time.Time{}, ErrTokenStoreFull
	}

	expiresAt := now.Add(time.Duration(ttlSeconds) * time.Second)
	g.tokens[token] = tokenRecord{
		containerID: containerID,
		expiresAt:   expiresAt,
	}
	return expiresAt, nil
}

// ValidatePath rejects dot-dot segments and percent-encoded path separators,
// including separators hidden behind multiple layers of percent encoding.
// The argument may be either an escaped URL path or an already-decoded path.
func ValidatePath(path string) error {
	decoded := path
	for depth := 0; depth < maxPathDecodes; depth++ {
		lower := strings.ToLower(decoded)
		if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
			return ErrEncodedSlash
		}
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return ErrUnsafePath
		}
		if next == decoded {
			break
		}
		decoded = next
	}

	if strings.Contains(strings.ToLower(decoded), "%2f") || strings.Contains(strings.ToLower(decoded), "%5c") {
		return ErrEncodedSlash
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == ".." {
			return ErrPathTraversal
		}
		for _, ch := range segment {
			if ch < 0x20 || ch == 0x7f {
				return ErrUnsafePath
			}
		}
	}
	return nil
}

// ValidateNoForwardingHeaders rejects any request carrying a forwarding header.
func ValidateNoForwardingHeaders(header http.Header) error {
	for _, name := range forwardingHeaders {
		if headerPresent(header, name) {
			return fmt.Errorf("%w: %s", ErrForwardedHeader, name)
		}
	}
	return nil
}

// ValidateTokenTTL parses a single token TTL header value and checks its range.
func ValidateTokenTTL(value string) (int, error) {
	return parseTTLHeaderValues([]string{value})
}

func validateCommon(r *http.Request) error {
	if r == nil {
		return ErrNilRequest
	}
	if r.URL == nil {
		return ErrUnsafePath
	}
	if r.URL.Opaque != "" {
		return ErrUnsafePath
	}
	if err := ValidateNoForwardingHeaders(r.Header); err != nil {
		return err
	}
	return ValidatePath(r.URL.EscapedPath())
}

func parseTTLHeaderValues(values []string) (int, error) {
	if len(values) == 0 {
		return 0, ErrTokenTTLMissing
	}
	if len(values) != 1 {
		return 0, ErrTokenTTLMalformed
	}

	value := strings.TrimSpace(values[0])
	if value == "" || strings.Contains(value, ",") {
		return 0, ErrTokenTTLMalformed
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, ErrTokenTTLMalformed
		}
	}

	ttl, err := strconv.Atoi(value)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, ErrTokenTTLOutOfRange
		}
		return 0, ErrTokenTTLMalformed
	}
	if ttl < MinTokenTTL || ttl > MaxTokenTTL {
		return 0, ErrTokenTTLOutOfRange
	}
	return ttl, nil
}

func headerPresent(header http.Header, name string) bool {
	for key := range header {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func headerValues(header http.Header, name string) []string {
	var values []string
	for key, vv := range header {
		if strings.EqualFold(key, name) {
			values = append(values, vv...)
		}
	}
	return values
}

func durationSeconds(ttl time.Duration) (int, bool) {
	if ttl < 0 || ttl%time.Second != 0 {
		return 0, false
	}
	seconds := int(ttl / time.Second)
	if time.Duration(seconds)*time.Second != ttl {
		return 0, false
	}
	return seconds, true
}

func validTokenFormat(token string) bool {
	if token == "" || len(token) > maxTokenLength || strings.TrimSpace(token) != token {
		return false
	}
	for _, ch := range token {
		if ch < '!' || ch > '~' {
			return false
		}
	}
	return true
}

func randomToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("netsec: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

func (g *Guard) pruneLocked(now time.Time) {
	for token, record := range g.tokens {
		if !now.Before(record.expiresAt) {
			delete(g.tokens, token)
		}
	}
}
