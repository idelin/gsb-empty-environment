package netsec

import (
	"context"
	"net/http"
)

type contextKey int

const identityKey contextKey = iota

// Identity carries the session and user a request belongs to. The
// payment service attaches it after authenticating the caller.
type Identity struct {
	SessionID string
	UserID    string
}

// ContextWithIdentity returns a request carrying the authenticated
// session and user, as the payment service's auth layer would set.
func ContextWithIdentity(r *http.Request, sessionID, userID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), identityKey, Identity{
		SessionID: sessionID,
		UserID:    userID,
	}))
}

func identityFrom(r *http.Request) (Identity, bool) {
	id, ok := r.Context().Value(identityKey).(Identity)
	return id, ok
}

// Protector enforces CSRF policy on payment service routes. Safe
// methods never reach write handlers, and cross-site navigation is
// governed by a different, looser policy than form submission.
type Protector struct {
	Manager *TokenManager
	// Origin is the service's own origin, e.g. "https://pay.example.com".
	Origin string
	// TokenField is the form field carrying the token.
	TokenField string
	// TokenHeader is the header carrying the token for API clients.
	TokenHeader string
}

func NewProtector(m *TokenManager, origin string) *Protector {
	return &Protector{
		Manager:     m,
		Origin:      origin,
		TokenField:  "csrf_token",
		TokenHeader: "X-CSRF-Token",
	}
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// ProtectRead guards a read-only endpoint with the navigation policy:
// top-level cross-site navigation (a user following a link) is allowed
// because GET must not mutate state, but cross-site subresource or
// script-driven loads are rejected.
func (p *Protector) ProtectRead(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isSafeMethod(r.Method) {
			http.Error(w, "method not allowed on read endpoint", http.StatusMethodNotAllowed)
			return
		}
		site := r.Header.Get("Sec-Fetch-Site")
		mode := r.Header.Get("Sec-Fetch-Mode")
		if site == "cross-site" && mode != "" && mode != "navigate" {
			http.Error(w, "cross-site request forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ProtectWrite guards a state-changing endpoint with the form
// submission policy: only unsafe methods are accepted (GET can never
// trigger a write), Origin and Sec-Fetch-Site must match a same-origin
// form submission, and a valid single-use token bound to this session,
// user and action is required.
func (p *Protector) ProtectWrite(action string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			// Writes must use an unsafe method; a GET, cross-site or
			// not, can never trigger a state change here.
			http.Error(w, "safe methods cannot perform writes", http.StatusMethodNotAllowed)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != p.Origin {
			http.Error(w, "origin mismatch", http.StatusForbidden)
			return
		}
		switch site := r.Header.Get("Sec-Fetch-Site"); site {
		case "", "same-origin", "none":
			// "" covers non-browser clients; "none" covers direct
			// user-initiated submissions.
		default:
			http.Error(w, "cross-site form submission forbidden", http.StatusForbidden)
			return
		}
		id, ok := identityFrom(r)
		if !ok || id.SessionID == "" || id.UserID == "" {
			http.Error(w, "missing authenticated identity", http.StatusUnauthorized)
			return
		}
		token := r.Header.Get(p.TokenHeader)
		if token == "" {
			token = r.FormValue(p.TokenField)
		}
		if token == "" {
			http.Error(w, "missing csrf token", http.StatusForbidden)
			return
		}
		if err := p.Manager.Validate(token, id.SessionID, id.UserID, action); err != nil {
			http.Error(w, "csrf token rejected: "+err.Error(), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
