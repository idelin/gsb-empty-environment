package netsec

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"
)

// FakeProvider is an in-process stand-in for an OIDC identity provider.
// It issues authorization codes on /authorize and redeems them on /token,
// enforcing redirect_uri and PKCE S256 binding. Requests are served through
// an http.RoundTripper so no sockets are needed, which keeps the harness
// hermetic. Failure behavior is configurable so tests can exercise timeouts,
// malformed responses and issuer mix-up.
type FakeProvider struct {
	issuer  string
	secret  []byte
	handler http.Handler

	mu               sync.Mutex
	codes            map[string]*fakeCode
	exchangeAttempts int

	// Behavior knobs.
	TokenDelay      time.Duration // block the token endpoint this long
	TokenStatusCode int           // non-zero => respond with this status
	MalformedJSON   bool          // return a non-JSON 200 body
	NoIDToken       bool          // 200 response without id_token
	WrongIssuer     string        // non-empty => mint token with this issuer
	WrongSecret     []byte        // non-nil => sign token with this secret
}

type fakeCode struct {
	nonce       string
	redirectURI string
	challenge   string
	consumed    bool
}

// NewFakeProvider creates a fake provider whose tokens claim the given
// issuer. The issuer doubles as the provider's base URL.
func NewFakeProvider(issuer string, secret []byte) *FakeProvider {
	fp := &FakeProvider{
		issuer: issuer,
		secret: secret,
		codes:  make(map[string]*fakeCode),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", fp.handleAuthorize)
	mux.HandleFunc("/token", fp.handleToken)
	fp.handler = mux
	return fp
}

// Issuer returns the issuer identifier.
func (fp *FakeProvider) Issuer() string { return fp.issuer }

// TokenEndpoint is the pinned token endpoint URL.
func (fp *FakeProvider) TokenEndpoint() string { return fp.issuer + "/token" }

// RoundTrip implements http.RoundTripper by dispatching to the in-process
// handler. Request context cancellation (e.g. client timeouts) is honored.
func (fp *FakeProvider) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	fp.handler.ServeHTTP(recorder, req)
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return recorder.Result(), nil
}

// HTTPClient returns a client whose requests are served by this provider.
func (fp *FakeProvider) HTTPClient() *http.Client {
	return &http.Client{Transport: fp}
}

// NewExchanger builds an HTTPExchanger pinned to this provider.
func (fp *FakeProvider) NewExchanger() *HTTPExchanger {
	exchanger := NewHTTPExchanger(fp.TokenEndpoint(), "client-"+fp.issuer, "secret-"+fp.issuer)
	exchanger.HTTPClient = fp.HTTPClient()
	return exchanger
}

// NewValidator builds an HMACValidator pinned to this provider.
func (fp *FakeProvider) NewValidator() *HMACValidator {
	return NewHMACValidator(fp.issuer, fp.secret)
}

// ExchangeAttempts reports how many times /token was hit.
func (fp *FakeProvider) ExchangeAttempts() int {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.exchangeAttempts
}

// IsCodeConsumed reports whether an issued code has already been redeemed.
func (fp *FakeProvider) IsCodeConsumed(code string) bool {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	c := fp.codes[code]
	return c != nil && c.consumed
}

func (fp *FakeProvider) issueCode(nonce, redirectURI, challenge string) string {
	code, _ := randomURLString(18)
	fp.mu.Lock()
	fp.codes[code] = &fakeCode{nonce: nonce, redirectURI: redirectURI, challenge: challenge}
	fp.mu.Unlock()
	return code
}

// handleAuthorize mimics the provider-side authorization endpoint: it stores
// the login request parameters and redirects back with an authorization code.
func (fp *FakeProvider) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	nonce := q.Get("nonce")
	challenge := q.Get("code_challenge")

	code := fp.issueCode(nonce, redirectURI, challenge)

	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	values := target.Query()
	values.Set("code", code)
	values.Set("state", state)
	// A compliant provider also echoes the issuer on the callback.
	values.Set("iss", fp.issuer)
	target.RawQuery = values.Encode()

	w.Header().Set("Location", target.String())
	w.WriteHeader(http.StatusFound)
}

func (fp *FakeProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	if fp.TokenDelay > 0 {
		select {
		case <-time.After(fp.TokenDelay):
		case <-r.Context().Done():
			return
		}
	}

	fp.mu.Lock()
	fp.exchangeAttempts++
	fp.mu.Unlock()

	if fp.TokenStatusCode != 0 {
		http.Error(w, `{"error":"forced_failure"}`, fp.TokenStatusCode)
		return
	}

	code := r.PostForm.Get("code")
	redirectURI := r.PostForm.Get("redirect_uri")
	verifier := r.PostForm.Get("code_verifier")

	fp.mu.Lock()
	stored, ok := fp.codes[code]
	if !ok {
		fp.mu.Unlock()
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	if stored.consumed {
		fp.mu.Unlock()
		http.Error(w, `{"error":"code_reused"}`, http.StatusBadRequest)
		return
	}
	if stored.redirectURI != redirectURI {
		fp.mu.Unlock()
		http.Error(w, `{"error":"redirect_uri_mismatch"}`, http.StatusBadRequest)
		return
	}
	if stored.challenge != "" && S256Challenge(verifier) != stored.challenge {
		fp.mu.Unlock()
		http.Error(w, `{"error":"invalid_grant: pkce"}`, http.StatusBadRequest)
		return
	}
	stored.consumed = true
	fp.mu.Unlock()

	if fp.MalformedJSON {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{this is not json"))
		return
	}
	if fp.NoIDToken {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "acc-" + code,
			"token_type":   "Bearer",
		})
		return
	}

	issuer := fp.issuer
	if fp.WrongIssuer != "" {
		issuer = fp.WrongIssuer
	}
	secret := fp.secret
	if fp.WrongSecret != nil {
		secret = fp.WrongSecret
	}
	idToken, err := SignIDToken(issuer, secret, Claims{
		"sub":   "user-" + code,
		"nonce": stored.nonce,
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "acc-" + code,
		"token_type":   "Bearer",
		"id_token":     idToken,
		"expires_in":   300,
	})
}

// AuthorizeURL builds the URL the browser would be redirected to when a login
// begins at this provider.
func (fp *FakeProvider) AuthorizeURL(redirectURI string, tx *Transaction) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", "client-"+fp.issuer)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", tx.State())
	q.Set("nonce", tx.Nonce())
	q.Set("code_challenge", tx.CodeChallenge())
	q.Set("code_challenge_method", "S256")
	return fmt.Sprintf("%s/authorize?%s", fp.issuer, q.Encode())
}
