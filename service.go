package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

const (
	// DefaultMaxBodyBytes is the default back-channel request body limit.
	DefaultMaxBodyBytes  int64 = 64 << 10
	backChannelMediaType       = "application/x-www-form-urlencoded"
)

var (
	// ErrUntrustedOrigin is returned for front-channel requests that do not
	// originate from an allow-listed iframe origin.
	ErrUntrustedOrigin = errors.New("netsec: untrusted iframe origin")

	// ErrUnsupportedMediaType is returned when the back-channel body is not
	// form encoded.
	ErrUnsupportedMediaType = errors.New("netsec: unsupported content type")

	// ErrRequestTooLarge is returned when the back-channel body exceeds its
	// configured limit.
	ErrRequestTooLarge = errors.New("netsec: logout request body too large")
)

// LogoutService validates logout tokens and terminates exactly the addressed
// session through the back channel and front channel.
type LogoutService struct {
	Verifier       *Verifier
	Sessions       SessionStore
	AllowedOrigins []string
	MaxBodyBytes   int64
}

// Service is an alias for LogoutService.
type Service = LogoutService

// ServiceOption configures a LogoutService.
type ServiceOption func(*LogoutService)

// WithAllowedOrigins sets the iframe origins trusted by the front channel.
func WithAllowedOrigins(origins ...string) ServiceOption {
	return func(service *LogoutService) {
		service.AllowedOrigins = append([]string(nil), origins...)
	}
}

// WithFrontChannelOrigins is an alias for WithAllowedOrigins.
func WithFrontChannelOrigins(origins ...string) ServiceOption {
	return WithAllowedOrigins(origins...)
}

// WithMaxBodyBytes sets the back-channel body limit.
func WithMaxBodyBytes(limit int64) ServiceOption {
	return func(service *LogoutService) {
		service.MaxBodyBytes = limit
	}
}

// WithSessionStore sets the session store.
func WithSessionStore(sessions SessionStore) ServiceOption {
	return func(service *LogoutService) {
		service.Sessions = sessions
	}
}

// WithVerifier sets the token verifier.
func WithVerifier(verifier *Verifier) ServiceOption {
	return func(service *LogoutService) {
		service.Verifier = verifier
	}
}

// NewLogoutService constructs a service. If sessions is nil, an empty
// in-memory store is used.
func NewLogoutService(verifier *Verifier, sessions SessionStore, options ...ServiceOption) *LogoutService {
	service := &LogoutService{
		Verifier:     verifier,
		Sessions:     sessions,
		MaxBodyBytes: DefaultMaxBodyBytes,
	}
	if service.Sessions == nil {
		service.Sessions = NewMemorySessionStore()
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

// NewService is an alias for NewLogoutService.
func NewService(verifier *Verifier, sessions SessionStore, options ...ServiceOption) *LogoutService {
	return NewLogoutService(verifier, sessions, options...)
}

// BackChannelHandler returns an http.Handler for the back-channel logout
// endpoint.
func (s *LogoutService) BackChannelHandler() http.Handler {
	return http.HandlerFunc(s.HandleBackChannel)
}

// FrontChannelHandler returns an http.Handler for the front-channel logout
// endpoint.
func (s *LogoutService) FrontChannelHandler() http.Handler {
	return http.HandlerFunc(s.HandleFrontChannel)
}

// BackChannelLogoutHandler is an alias for BackChannelHandler.
func (s *LogoutService) BackChannelLogoutHandler() http.Handler {
	return s.BackChannelHandler()
}

// FrontChannelLogoutHandler is an alias for FrontChannelHandler.
func (s *LogoutService) FrontChannelLogoutHandler() http.Handler {
	return s.FrontChannelHandler()
}

// BackChannelHandlerFunc is a convenience alias.
func (s *LogoutService) BackChannelHandlerFunc() http.HandlerFunc {
	return s.HandleBackChannel
}

// FrontChannelHandlerFunc is a convenience alias.
func (s *LogoutService) FrontChannelHandlerFunc() http.HandlerFunc {
	return s.HandleFrontChannel
}

// RegisterRoutes mounts the two endpoints on mux. Empty paths are ignored.
func (s *LogoutService) RegisterRoutes(mux *http.ServeMux, backChannelPath, frontChannelPath string) {
	if mux == nil {
		return
	}
	if backChannelPath != "" {
		mux.Handle(backChannelPath, s.BackChannelHandler())
	}
	if frontChannelPath != "" {
		mux.Handle(frontChannelPath, s.FrontChannelHandler())
	}
}

// LogoutBackChannel validates and consumes token and terminates the addressed
// session. The token is consumed even if the session is already gone.
func (s *LogoutService) LogoutBackChannel(ctx context.Context, token string) (bool, error) {
	if s == nil || s.Verifier == nil {
		return false, errors.New("netsec: logout service has no verifier")
	}
	claims, err := s.Verifier.Verify(token)
	if err != nil {
		return false, err
	}
	return s.terminate(ctx, claims)
}

// LogoutFrontChannel validates a trusted iframe origin and terminates exactly
// the session named by issuer and sessionID. If logoutToken is non-empty it
// must be valid and address the same session.
func (s *LogoutService) LogoutFrontChannel(ctx context.Context, origin, issuer, sessionID, logoutToken string) (bool, error) {
	if s == nil || s.Verifier == nil {
		return false, errors.New("netsec: logout service has no verifier")
	}
	if !s.originAllowed(origin) {
		return false, ErrUntrustedOrigin
	}
	if logoutToken != "" {
		claims, err := s.Verifier.Validate(logoutToken)
		if err != nil {
			return false, err
		}
		if issuer != "" && issuer != claims.Issuer {
			return false, fmt.Errorf("%w: iss does not match token", ErrInvalidToken)
		}
		if sessionID != "" && sessionID != claims.SessionID {
			return false, fmt.Errorf("%w: sid does not match token", ErrInvalidToken)
		}
		claims, err = s.Verifier.Verify(logoutToken)
		if err != nil {
			return false, err
		}
		return s.terminate(ctx, claims)
	}
	if issuer == "" || sessionID == "" {
		return false, fmt.Errorf("%w: iss and sid are required", ErrInvalidToken)
	}
	if !s.Verifier.TrustsIssuer(issuer) {
		return false, ErrUnknownIssuer
	}
	if s.Sessions == nil {
		return false, errors.New("netsec: logout service has no session store")
	}
	subject := ""
	if lookup, ok := s.Sessions.(SessionLookup); ok {
		session, found := lookup.GetSession(issuer, sessionID)
		if !found {
			return false, nil
		}
		subject = session.Subject
	}
	return s.Sessions.TerminateSession(ctx, issuer, sessionID, subject)
}

// HandleBackChannel implements the OIDC-style back-channel logout endpoint.
func (s *LogoutService) HandleBackChannel(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.Verifier == nil {
		writeHTTPError(w, http.StatusInternalServerError, "logout service unavailable")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeHTTPError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := s.maxBodyBytes()
	if r.ContentLength > limit {
		writeHTTPError(w, http.StatusRequestEntityTooLarge, "logout request body too large")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, backChannelMediaType) {
		writeHTTPError(w, http.StatusUnsupportedMediaType, "content type must be "+backChannelMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeHTTPError(w, http.StatusRequestEntityTooLarge, "logout request body too large")
			return
		}
		writeHTTPError(w, http.StatusBadRequest, "invalid form body")
		return
	}
	values := r.PostForm["logout_token"]
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		writeHTTPError(w, http.StatusBadRequest, "exactly one logout_token is required")
		return
	}
	claims, err := s.Verifier.Verify(values[0])
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, "invalid logout token")
		return
	}
	if _, err := s.terminate(r.Context(), claims); err != nil {
		if errors.Is(err, ErrSessionSubjectMismatch) || errors.Is(err, ErrInvalidSession) {
			writeHTTPError(w, http.StatusBadRequest, "invalid logout token")
			return
		}
		writeHTTPError(w, http.StatusInternalServerError, "session termination failed")
		return
	}
	writeHTTPOK(w)
}

// HandleFrontChannel implements the iframe-based front-channel logout endpoint.
// Origin or Referer must match an allow-listed origin. The request terminates
// only the session identified by iss+sid (or by a supplied logout_token).
func (s *LogoutService) HandleFrontChannel(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.Verifier == nil {
		writeHTTPError(w, http.StatusInternalServerError, "logout service unavailable")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeHTTPError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.frameOriginAllowed(r) {
		writeHTTPError(w, http.StatusForbidden, "untrusted iframe origin")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, "invalid query string")
		return
	}
	tokenValues := query["logout_token"]
	if len(tokenValues) > 1 || len(query["iss"]) > 1 || len(query["sid"]) > 1 {
		writeHTTPError(w, http.StatusBadRequest, "duplicate logout parameter")
		return
	}
	logoutToken := ""
	if len(tokenValues) == 1 {
		logoutToken = strings.TrimSpace(tokenValues[0])
	}
	issuer := strings.TrimSpace(query.Get("iss"))
	sessionID := strings.TrimSpace(query.Get("sid"))
	if logoutToken != "" {
		claims, err := s.Verifier.Validate(logoutToken)
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, "invalid logout token")
			return
		}
		if issuer != "" && issuer != claims.Issuer {
			writeHTTPError(w, http.StatusBadRequest, "issuer does not match token")
			return
		}
		if sessionID != "" && sessionID != claims.SessionID {
			writeHTTPError(w, http.StatusBadRequest, "session does not match token")
			return
		}
		claims, err = s.Verifier.Verify(logoutToken)
		if err != nil {
			writeHTTPError(w, http.StatusBadRequest, "invalid logout token")
			return
		}
		if issuer != "" && issuer != claims.Issuer {
			writeHTTPError(w, http.StatusBadRequest, "issuer does not match token")
			return
		}
		if sessionID != "" && sessionID != claims.SessionID {
			writeHTTPError(w, http.StatusBadRequest, "session does not match token")
			return
		}
		if _, err := s.terminate(r.Context(), claims); err != nil {
			if errors.Is(err, ErrSessionSubjectMismatch) {
				writeHTTPError(w, http.StatusBadRequest, "invalid logout token")
				return
			}
			writeHTTPError(w, http.StatusInternalServerError, "session termination failed")
			return
		}
		writeHTTPOK(w)
		return
	}
	if issuer == "" || sessionID == "" {
		writeHTTPError(w, http.StatusBadRequest, "iss and sid are required")
		return
	}
	if !s.Verifier.TrustsIssuer(issuer) {
		writeHTTPError(w, http.StatusBadRequest, "unknown token issuer")
		return
	}
	if s.Sessions == nil {
		writeHTTPError(w, http.StatusInternalServerError, "logout service unavailable")
		return
	}
	subject := ""
	if lookup, ok := s.Sessions.(SessionLookup); ok {
		session, found := lookup.GetSession(issuer, sessionID)
		if !found {
			writeHTTPOK(w)
			return
		}
		subject = session.Subject
	}
	if _, err := s.Sessions.TerminateSession(r.Context(), issuer, sessionID, subject); err != nil {
		if errors.Is(err, ErrSessionSubjectMismatch) {
			writeHTTPError(w, http.StatusBadRequest, "invalid session")
			return
		}
		writeHTTPError(w, http.StatusInternalServerError, "session termination failed")
		return
	}
	writeHTTPOK(w)
}

func (s *LogoutService) terminate(ctx context.Context, claims *LogoutToken) (bool, error) {
	if s.Sessions == nil {
		return false, errors.New("netsec: logout service has no session store")
	}
	return s.Sessions.TerminateSession(ctx, claims.Issuer, claims.SessionID, claims.Subject)
}

func (s *LogoutService) maxBodyBytes() int64 {
	if s.MaxBodyBytes <= 0 {
		return DefaultMaxBodyBytes
	}
	return s.MaxBodyBytes
}

func (s *LogoutService) frameOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	return s.originAllowed(origin)
}

func (s *LogoutService) originAllowed(origin string) bool {
	normalized, ok := normalizeOrigin(origin)
	if !ok {
		return false
	}
	for _, allowed := range s.AllowedOrigins {
		allowedOrigin, ok := normalizeOrigin(allowed)
		if ok && constantTimeEqual([]byte(normalized), []byte(allowedOrigin)) {
			return true
		}
	}
	return false
}

func normalizeOrigin(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "null") {
		return "", false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Host)
	switch {
	case scheme == "https" && strings.HasSuffix(host, ":443"):
		host = strings.TrimSuffix(host, ":443")
	case scheme == "http" && strings.HasSuffix(host, ":80"):
		host = strings.TrimSuffix(host, ":80")
	}
	return scheme + "://" + host, true
}

func writeHTTPError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message+"\n")
}

func writeHTTPOK(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "logout accepted\n")
}
