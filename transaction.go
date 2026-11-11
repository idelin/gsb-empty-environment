package netsec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var (
	// ErrUnknownTransaction is returned when no transaction matches the
	// callback state (unknown, already consumed or expired).
	ErrUnknownTransaction = errors.New("netsec: unknown or consumed login transaction")

	// ErrTransactionBusy is returned while another completion of the same
	// transaction is in flight. It prevents a concurrent callback from
	// exchanging the same authorization code twice.
	ErrTransactionBusy = errors.New("netsec: login transaction completion already in progress")

	// ErrStateMismatch indicates the callback state does not belong to the
	// looked-up transaction.
	ErrStateMismatch = errors.New("netsec: state mismatch")

	// ErrSessionMismatch indicates the callback was delivered to a different
	// browser session than the one that started the login.
	ErrSessionMismatch = errors.New("netsec: browser session mismatch")

	// ErrIssuerMismatch indicates the callback/ID token issuer differs from
	// the issuer pinned when the transaction began. This is the primary
	// guard against IdP mix-up / cross-callback confusion.
	ErrIssuerMismatch = errors.New("netsec: issuer mismatch")

	// ErrPKCEMismatch indicates the presented code verifier does not hash to
	// the PKCE challenge pinned on the transaction.
	ErrPKCEMismatch = errors.New("netsec: PKCE code verifier mismatch")

	// ErrRedirectURIMismatch indicates the callback redirect URI differs
	// from the one pinned on the transaction.
	ErrRedirectURIMismatch = errors.New("netsec: redirect URI mismatch")

	// ErrMissingIDToken indicates the token endpoint returned no ID token.
	ErrMissingIDToken = errors.New("netsec: token endpoint returned no ID token")
)

// DefaultTTL is how long an unconsumed transaction remains valid.
const DefaultTTL = 10 * time.Minute

// Manager stores in-flight login transactions and coordinates their
// one-time, atomic completion. It is safe for concurrent use.
type Manager struct {
	mu      sync.Mutex
	pending map[string]*Transaction
	ttl     time.Duration
	now     func() time.Time
}

// NewManager creates a Manager whose unconsumed transactions expire after ttl.
// A ttl <= 0 selects DefaultTTL.
func NewManager(ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Manager{
		pending: make(map[string]*Transaction),
		ttl:     ttl,
		now:     time.Now,
	}
}

// BeginParams pins every security-relevant input of a login attempt.
type BeginParams struct {
	// BrowserSessionID is the identifier of the browser session (cookie) that
	// started the login.
	BrowserSessionID string

	// Issuer identifies the identity provider the user is authenticating to.
	Issuer string

	// State and Nonce may be supplied by the caller; if empty they are
	// generated cryptographically.
	State string
	Nonce string

	// CodeChallenge is the S256 PKCE challenge; CodeChallengeMethod must be
	// "S256" (the only supported method).
	CodeChallenge       string
	CodeChallengeMethod string

	// RedirectURI is sent to the provider and re-pinned for the exchange.
	RedirectURI string

	// Exchanger and Validator are the issuer-specific collaborators fixed for
	// the lifetime of the transaction. They are never re-selected from
	// callback input.
	Exchanger Exchanger
	Validator Validator
}

// Transaction is an in-flight, fully pinned OIDC authorization-code login.
type Transaction struct {
	state   string
	nonce   string
	session string
	issuer  string

	codeChallenge       string
	codeChallengeMethod string
	redirectURI         string

	exchanger Exchanger
	validator Validator

	createdAt time.Time
	expiresAt time.Time

	// completion serializes concurrent callbacks for the same transaction.
	// TryLock makes a second completion fail fast with ErrTransactionBusy.
	completion sync.Mutex

	// consumed is set once the transaction has been fully redeemed. It is
	// guarded by completion and closes the window where a second completer
	// acquires the lock after the first one released it.
	consumed bool
}

// Begin creates and stores a transaction bound to the given browser session
// and issuer, pinning the nonce, state, PKCE challenge, redirect URI and the
// issuer-specific exchanger/validator. It returns the stored transaction.
func (m *Manager) Begin(params BeginParams) (*Transaction, error) {
	if params.BrowserSessionID == "" {
		return nil, errors.New("netsec: browser session id is required")
	}
	if params.Issuer == "" {
		return nil, errors.New("netsec: issuer is required")
	}
	if params.CodeChallenge == "" {
		return nil, errors.New("netsec: PKCE code challenge is required")
	}
	if params.CodeChallengeMethod != "S256" {
		return nil, errors.New("netsec: only S256 PKCE is supported")
	}
	if params.RedirectURI == "" {
		return nil, errors.New("netsec: redirect URI is required")
	}
	if params.Exchanger == nil || params.Validator == nil {
		return nil, errors.New("netsec: exchanger and validator are required")
	}

	state := params.State
	if state == "" {
		var err error
		if state, err = randomURLString(24); err != nil {
			return nil, err
		}
	}
	nonce := params.Nonce
	if nonce == "" {
		var err error
		if nonce, err = randomURLString(24); err != nil {
			return nil, err
		}
	}

	now := m.now()
	tx := &Transaction{
		state:               state,
		nonce:               nonce,
		session:             params.BrowserSessionID,
		issuer:              params.Issuer,
		codeChallenge:       params.CodeChallenge,
		codeChallengeMethod: params.CodeChallengeMethod,
		redirectURI:         params.RedirectURI,
		exchanger:           params.Exchanger,
		validator:           params.Validator,
		createdAt:           now,
		expiresAt:           now.Add(m.ttl),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.pending[state]; exists {
		return nil, errors.New("netsec: state collision, retry login")
	}
	m.pending[state] = tx
	return tx, nil
}

// Callback carries everything observed on the redirect back to the relying
// party.
type Callback struct {
	// State is the opaque state echoed by the provider.
	State string

	// Code is the authorization code.
	Code string

	// Issuer is the issuer of the response, taken from a trusted channel
	// (e.g. the per-issuer callback route or an "iss" response parameter).
	Issuer string

	// BrowserSessionID is the session cookie presented with the callback.
	BrowserSessionID string

	// CodeVerifier is the S256 PKCE verifier held by the browser session.
	CodeVerifier string

	// RedirectURI, when non-empty, must equal the pinned value.
	RedirectURI string
}

// Result is returned on a successful, fully verified completion.
type Result struct {
	Claims      Claims
	Token       TokenResponse
	Transaction *Transaction
}

// Complete validates the callback against the transaction pinned at Begin,
// runs the pinned exchanger and validator, and consumes (deletes) the
// transaction exactly once — and only after every check has succeeded.
// Any failure leaves the transaction in place so a legitimate retry is not
// burned by an invalid or attacker-crafted response.
func (m *Manager) Complete(ctx context.Context, cb Callback) (*Result, error) {
	m.mu.Lock()
	tx := m.pending[cb.State]
	m.mu.Unlock()
	if tx == nil {
		return nil, ErrUnknownTransaction
	}

	// Reject concurrent completion attempts before any network activity.
	if !tx.completion.TryLock() {
		return nil, ErrTransactionBusy
	}
	defer tx.completion.Unlock()
	if tx.consumed {
		return nil, ErrUnknownTransaction
	}

	if err := tx.verifyPinned(cb); err != nil {
		return nil, err
	}

	// Use the collaborators fixed on this transaction — never anything
	// derived from the callback.
	token, err := tx.exchanger.Exchange(ctx, ExchangeRequest{
		Code:         cb.Code,
		RedirectURI:  tx.redirectURI,
		CodeVerifier: cb.CodeVerifier,
	})
	if err != nil {
		return nil, err
	}
	if token.IDToken == "" {
		return nil, ErrMissingIDToken
	}

	claims, err := tx.validator.VerifyIDToken(ctx, token.IDToken, tx.nonce)
	if err != nil {
		return nil, err
	}
	if claims.Issuer() != tx.issuer {
		return nil, ErrIssuerMismatch
	}

	// All checks passed: consume the transaction once, atomically.
	m.mu.Lock()
	current := m.pending[tx.state]
	if current != tx {
		// Defensive: the transaction was removed/replaced while the
		// exchange was in flight. Do not treat the exchange as a login.
		m.mu.Unlock()
		return nil, ErrUnknownTransaction
	}
	delete(m.pending, tx.state)
	m.mu.Unlock()
	tx.consumed = true

	return &Result{Claims: claims, Token: token, Transaction: tx}, nil
}

// verifyPinned enforces every binding established in Begin. It performs no
// network I/O so a forged callback is rejected without side effects.
func (tx *Transaction) verifyPinned(cb Callback) error {
	if tx.isExpired() {
		return ErrUnknownTransaction
	}
	if cb.State != tx.state {
		return ErrStateMismatch
	}
	if cb.BrowserSessionID != tx.session {
		return ErrSessionMismatch
	}
	if cb.Issuer != tx.issuer {
		return ErrIssuerMismatch
	}
	if cb.RedirectURI != "" && cb.RedirectURI != tx.redirectURI {
		return ErrRedirectURIMismatch
	}
	if S256Challenge(cb.CodeVerifier) != tx.codeChallenge {
		return ErrPKCEMismatch
	}
	if cb.Code == "" {
		return errors.New("netsec: authorization code is required")
	}
	return nil
}

func (tx *Transaction) isExpired() bool {
	return time.Now().After(tx.expiresAt)
}

// Len returns the number of live (unconsumed, unexpired) transactions.
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

// Lookup returns the transaction for a state without consuming it.
func (m *Manager) Lookup(state string) (*Transaction, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, ok := m.pending[state]
	return tx, ok
}

// PurgeExpired removes transactions past their TTL and returns the count.
func (m *Manager) PurgeExpired() int {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	for state, tx := range m.pending {
		if now.After(tx.expiresAt) {
			delete(m.pending, state)
			removed++
		}
	}
	return removed
}

// Accessors for the pinned values.

func (tx *Transaction) State() string          { return tx.state }
func (tx *Transaction) Nonce() string          { return tx.nonce }
func (tx *Transaction) BrowserSession() string { return tx.session }
func (tx *Transaction) Issuer() string         { return tx.issuer }
func (tx *Transaction) RedirectURI() string    { return tx.redirectURI }
func (tx *Transaction) CodeChallenge() string {
	return tx.codeChallenge
}
func (tx *Transaction) ExpiresAt() time.Time { return tx.expiresAt }

// S256Challenge computes the base64url-no-padding SHA-256 digest of a PKCE
// code verifier, matching the "S256" challenge method.
func S256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewCodeVerifier generates a high-entropy PKCE code verifier.
func NewCodeVerifier() (string, error) {
	return randomURLString(32)
}

func randomURLString(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
