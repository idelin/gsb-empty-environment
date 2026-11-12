// Package par implements Pushed Authorization Requests (RFC 9126) for an
// OAuth 2.0 authorization server.
//
// The component exposes two http.Handler building blocks:
//
//   - Manager.PushHandler authenticates the client, stores the pushed
//     parameters and returns a one-time request_uri.
//   - Manager.AuthorizeMiddleware wraps an existing authorization endpoint.
//     When the request carries a request_uri it replaces every query
//     parameter with the parameters bound to the reference, so parameters
//     appended by the attacker cannot override the pushed content.
//
// Storage is behind the Store interface; the default MemoryStore is safe for
// concurrent use and makes reference consumption atomic.
package par

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

const (
	// RequestURIPrefix is the required prefix of every issued reference.
	RequestURIPrefix = "urn:ietf:params:oauth:request_uri:"

	// DefaultTTL matches the RFC 9126 recommended maximum lifetime.
	DefaultTTL = 60 * time.Second

	// refEntropyBytes is the random portion length in bytes (32 bytes =
	// 256 bits), making references unguessable.
	refEntropyBytes = 32
)

// Authenticator performs client authentication on a PAR request. It must
// verify the client's credentials (for example client_secret_post,
// client_secret_basic or a private_key_jwt signature) and return the
// authenticated client identifier. A non-nil error means authentication
// failed and the push is rejected with invalid_client.
type Authenticator func(r *http.Request) (clientID string, err error)

// Options configures a Manager.
type Options struct {
	// TTL is the lifetime of an issued reference. Defaults to DefaultTTL.
	TTL time.Duration
	// Store persists pushed requests. Defaults to a MemoryStore.
	Store Store
	// Now supplies the current time, mainly for tests.
	Now func() time.Time
}

// Option customizes a Manager.
type Option func(*Options)

// WithTTL sets the reference lifetime. Non-positive values fall back to the
// default.
func WithTTL(ttl time.Duration) Option {
	return func(o *Options) { o.TTL = ttl }
}

// WithStore replaces the default storage.
func WithStore(store Store) Option {
	return func(o *Options) { o.Store = store }
}

// WithClock injects a time source, intended for tests.
func WithClock(now func() time.Time) Option {
	return func(o *Options) { o.Now = now }
}

// Manager owns PAR state and produces the HTTP building blocks.
type Manager struct {
	authn Authenticator
	store Store
	ttl   time.Duration
	now   func() time.Time
}

// NewManager creates a Manager. authenticate is invoked for every push and
// must establish the client's identity before anything is stored.
func NewManager(authenticate Authenticator, opts ...Option) *Manager {
	options := Options{
		TTL:   DefaultTTL,
		Now:   time.Now,
		Store: nil,
	}
	for _, opt := range opts {
		opt(&options)
	}
	if options.TTL <= 0 {
		options.TTL = DefaultTTL
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Store == nil {
		options.Store = newMemoryStore(options.Now)
	}
	return &Manager{
		authn: authenticate,
		store: options.Store,
		ttl:   options.TTL,
		now:   options.Now,
	}
}

// ttlSeconds reports the configured lifetime as the whole-second value
// returned in expires_in.
func (m *Manager) ttlSeconds() int {
	return int(m.ttl / time.Second)
}

// oauthError is the RFC 6749 error payload used for every rejection.
type oauthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(oauthError{Code: code, Description: description})
}

// clientAuthParams carry client credentials and must never be stored as
// authorization request parameters.
var clientAuthParams = map[string]struct{}{
	"client_id":             {},
	"client_secret":         {},
	"client_assertion":      {},
	"client_assertion_type": {},
}

// PushHandler serves POST requests at the PAR endpoint.
func (m *Manager) PushHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "invalid_request",
				"the PAR endpoint requires POST")
			return
		}
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"malformed request body")
			return
		}

		// A client cannot pre-choose a reference: request_uri is assigned by
		// the server and is forbidden in the pushed body.
		if _, ok := r.PostForm["request_uri"]; ok {
			writeError(w, http.StatusBadRequest, "invalid_request",
				"request_uri must not be included in the PAR request")
			return
		}

		clientID, err := m.authn(r)
		if err != nil || clientID == "" {
			writeError(w, http.StatusUnauthorized, "invalid_client",
				"client authentication failed")
			return
		}

		params := make(map[string]string, len(r.PostForm))
		for key, values := range r.PostForm {
			if _, isCredential := clientAuthParams[key]; isCredential {
				continue
			}
			params[key] = values[0]
		}
		// Bind the authenticated identity rather than any client_id the
		// client placed in the body.
		params["client_id"] = clientID

		ref, err := m.storeEntry(clientID, params)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "server_error",
				"could not issue request_uri")
			return
		}

		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_uri": ref,
			"expires_in":  m.ttlSeconds(),
		})
	})
}

// storeEntry generates an unguessable reference and persists the entry,
// retrying on the astronomically unlikely random collision.
func (m *Manager) storeEntry(clientID string, params map[string]string) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		random := make([]byte, refEntropyBytes)
		if _, err := rand.Read(random); err != nil {
			return "", err
		}
		ref := RequestURIPrefix + hex.EncodeToString(random)
		err := m.store.Put(ref, Entry{
			ClientID:  clientID,
			Params:    params,
			ExpiresAt: m.now().Add(m.ttl),
		})
		if err == nil {
			return ref, nil
		}
		if err != ErrConflict {
			return "", err
		}
	}
	return "", ErrConflict
}

// AuthorizeMiddleware guards an authorization endpoint. Requests carrying a
// request_uri are rewritten to contain only the parameters that were bound
// to the reference; every other incoming query parameter is discarded.
// Requests without a request_uri pass through untouched so ordinary
// authorization requests keep working.
func (m *Manager) AuthorizeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI := r.URL.Query().Get("request_uri")
		if requestURI == "" {
			next.ServeHTTP(w, r)
			return
		}

		entry, err := m.store.Consume(requestURI, m.now())
		switch err {
		case nil:
		case ErrExpired, ErrUsed, ErrNotFound:
			writeError(w, http.StatusBadRequest, "invalid_request_object",
				"request_uri is invalid, expired or already used")
			return
		default:
			writeError(w, http.StatusInternalServerError, "server_error",
				"could not resolve request_uri")
			return
		}

		// The client presenting the reference must be the one that pushed it.
		if presented := r.URL.Query().Get("client_id"); presented != "" &&
			subtle.ConstantTimeCompare([]byte(presented), []byte(entry.ClientID)) != 1 {
			writeError(w, http.StatusBadRequest, "invalid_client",
				"client_id does not match the client that registered the request_uri")
			return
		}

		// Replace the query wholesale with the pushed parameters. All
		// attacker-appended parameters are ignored.
		query := make(url.Values, len(entry.Params))
		for key, value := range entry.Params {
			query.Set(key, value)
		}
		rewritten := r.Clone(r.Context())
		rewrittenURL := *r.URL
		rewrittenURL.RawQuery = query.Encode()
		rewritten.URL = &rewrittenURL
		// Drop any parsed form so downstream handlers re-read the bound query.
		rewritten.Form = nil
		rewritten.PostForm = nil
		next.ServeHTTP(w, rewritten)
	})
}
