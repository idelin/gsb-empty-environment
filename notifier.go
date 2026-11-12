package netsec

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// CIBA ping-mode notification endpoint handling.
//
// A ping notification only means "the authentication request has a result,
// go and fetch it". The handler therefore never trusts any token material
// carried in the notification body: the only body field that is read is
// auth_req_id, and tokens are always obtained from the token endpoint via a
// TokenFetcher.

const (
	// MinNotificationTokenLength is the minimum accepted length, in
	// characters (Unicode code points), of a client_notification_token.
	MinNotificationTokenLength = 22
	// MaxNotificationTokenLength is the maximum accepted length, in
	// characters (Unicode code points), of a client_notification_token.
	MaxNotificationTokenLength = 1024

	maxPingBodyBytes = 1 << 20
	bearerPrefix     = "Bearer "
)

// Sentinel errors returned by the registration and parsing helpers.
var (
	// ErrInvalidNotificationToken is returned before any backchannel
	// authentication request is initiated when the
	// client_notification_token length is outside [22, 1024].
	ErrInvalidNotificationToken = errors.New("netsec: client_notification_token must be between 22 and 1024 characters")
	ErrInvalidAuthReqID         = errors.New("netsec: auth_req_id must not be empty")
	ErrInvalidExpiresIn         = errors.New("netsec: expires_in must be positive")
	ErrAuthRequestExists        = errors.New("netsec: auth_req_id is already registered")
	ErrMissingBearerToken       = errors.New("netsec: missing Authorization bearer token")
	ErrMalformedAuthorization   = errors.New("netsec: malformed Authorization header")
)

// TokenFetcher exchanges an auth_req_id for tokens at the authorization
// server's token endpoint. Implementations must send the CIBA grant
// (urn:openid:params:grant-type:ciba) and never source tokens from a ping
// notification body.
type TokenFetcher interface {
	FetchToken(ctx context.Context, authReqID string) (*TokenResponse, error)
}

// TokenResponse is the token material returned by the token endpoint.
type TokenResponse struct {
	AccessToken  string
	TokenType    string
	ExpiresIn    time.Duration
	RefreshToken string
	IDToken      string
	Scope        string
}

// authRequest is the server-side state of one outstanding CIBA request.
type authRequest struct {
	notificationToken string
	expiresAt         time.Time

	// notified is set while (or after) a valid ping has been accepted, so
	// repeated notifications for the same auth_req_id are ignored.
	notified bool
	// done means tokens were successfully fetched and the request is
	// finalized.
	done     bool
	token    *TokenResponse
	fetchErr error
}

// Client stores the state of the CIBA requests initiated by this backend and
// implements http.Handler for the client notification endpoint.
type Client struct {
	mu       sync.Mutex
	requests map[string]*authRequest
	fetcher  TokenFetcher
	now      func() time.Time
}

// NewClient creates a Client that fetches tokens through fetcher.
func NewClient(fetcher TokenFetcher) *Client {
	return &Client{
		requests: make(map[string]*authRequest),
		fetcher:  fetcher,
		now:      time.Now,
	}
}

// ValidateNotificationToken enforces the 22..1024 character length bound on a
// client_notification_token. Call it when building the backchannel
// authentication request, so an invalid token is rejected before the request
// is sent to the authorization server.
func ValidateNotificationToken(token string) error {
	length := utf8.RuneCountInString(token)
	if length < MinNotificationTokenLength || length > MaxNotificationTokenLength {
		return ErrInvalidNotificationToken
	}
	return nil
}

// PrepareAuthRequest records an authentication request initiated by this
// client. authReqID is the identifier returned by the backchannel
// authentication endpoint, clientNotificationToken is the secret supplied in
// that request, and expiresIn is the corresponding expires_in.
//
// The notification token is validated first: an invalid token makes this
// method fail before the caller may initiate (or complete) any backchannel
// authentication request.
func (c *Client) PrepareAuthRequest(authReqID, clientNotificationToken string, expiresIn time.Duration) error {
	if authReqID == "" {
		return ErrInvalidAuthReqID
	}
	if err := ValidateNotificationToken(clientNotificationToken); err != nil {
		return err
	}
	if expiresIn <= 0 {
		return ErrInvalidExpiresIn
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.requests[authReqID]; exists {
		return ErrAuthRequestExists
	}
	c.requests[authReqID] = &authRequest{
		notificationToken: clientNotificationToken,
		expiresAt:         c.now().Add(expiresIn),
	}
	return nil
}

// ServeHTTP implements the notification endpoint. Only POST is accepted.
//
// Processing order:
//  1. The JSON body must contain an auth_req_id for a request registered by
//     this client that has not expired.
//  2. The Authorization: Bearer value must equal, in constant time, the
//     client_notification_token bound to that auth_req_id.
//  3. A second accepted notification for the same auth_req_id is ignored.
//  4. For the first accepted notification the token endpoint is queried with
//     the auth_req_id; token fields in the notification body are never read.
func (c *Client) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authReqID, ok := decodePingBody(w, r)
	if !ok {
		http.Error(w, "malformed notification body", http.StatusBadRequest)
		return
	}

	c.mu.Lock()
	request, exists := c.requests[authReqID]
	if !exists {
		// Unknown identifier: either another client's auth_req_id or a
		// request that was never initiated by this backend.
		c.mu.Unlock()
		http.Error(w, "unknown auth_req_id", http.StatusNotFound)
		return
	}
	// A request is expired once the current instant reaches expiresAt.
	if !c.now().Before(request.expiresAt) {
		c.mu.Unlock()
		http.Error(w, "authentication request expired", http.StatusGone)
		return
	}

	bearer, err := parseBearer(r)
	if err != nil {
		c.mu.Unlock()
		w.Header().Set("WWW-Authenticate", "Bearer realm=\"ciba-ping\"")
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if subtle.ConstantTimeCompare([]byte(bearer), []byte(request.notificationToken)) != 1 {
		c.mu.Unlock()
		w.Header().Set("WWW-Authenticate", "Bearer realm=\"ciba-ping\", error=\"invalid_token\"")
		http.Error(w, "invalid client_notification_token", http.StatusUnauthorized)
		return
	}

	if request.notified {
		// Replay of an already accepted notification: acknowledge without
		// touching the token endpoint again.
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	request.notified = true
	fetcher := c.fetcher
	c.mu.Unlock()

	if fetcher == nil {
		c.recordFetchFailure(authReqID, errors.New("netsec: no token fetcher configured"))
		http.Error(w, "token fetcher not configured", http.StatusInternalServerError)
		return
	}

	// The ping carries no trusted data: fetch tokens from the token
	// endpoint using only the auth_req_id.
	token, err := fetcher.FetchToken(r.Context(), authReqID)
	if err != nil {
		c.recordFetchFailure(authReqID, err)
		http.Error(w, "token endpoint request failed", http.StatusBadGateway)
		return
	}

	c.mu.Lock()
	request.token = token
	request.done = true
	c.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (c *Client) recordFetchFailure(authReqID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if request, exists := c.requests[authReqID]; exists {
		// Allow a later (retransmitted) ping to retry the token fetch
		// instead of wedging the request in a half-notified state.
		request.notified = false
		request.fetchErr = err
	}
}

// IsFinalized reports whether tokens have successfully been fetched for the
// given auth_req_id.
func (c *Client) IsFinalized(authReqID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	request, exists := c.requests[authReqID]
	return exists && request.done
}

// Token returns the tokens fetched for authReqID after a valid ping.
func (c *Client) Token(authReqID string) (*TokenResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	request, exists := c.requests[authReqID]
	if !exists || request.token == nil {
		return nil, false
	}
	copy := *request.token
	return &copy, true
}

// FetchError returns the last token endpoint error observed for authReqID.
func (c *Client) FetchError(authReqID string) (error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	request, exists := c.requests[authReqID]
	if !exists || request.fetchErr == nil {
		return nil, false
	}
	return request.fetchErr, true
}

// decodePingBody extracts only auth_req_id from the notification body. Any
// other JSON fields, including token material such as access_token, are
// deliberately ignored.
func decodePingBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPingBodyBytes)
	var payload struct {
		AuthReqID string `json:"auth_req_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return "", false
	}
	if payload.AuthReqID == "" {
		return "", false
	}
	return payload.AuthReqID, true
}

// parseBearer implements the Bearer scheme of RFC 6750 section 2.1.
func parseBearer(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", ErrMissingBearerToken
	}
	if len(header) <= len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", ErrMalformedAuthorization
	}
	token := strings.TrimSpace(header[len(bearerPrefix):])
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", ErrMalformedAuthorization
	}
	return token, nil
}
