package netsec

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Operator defines a trust anchor for account JWTs. PublicKey is the operator
// identity key and SigningKeys are the operator keys eligible to issue account
// JWTs. SigningKeys may be omitted when only the identity key is used.
type Operator struct {
	ID          string
	PublicKey   ed25519.PublicKey
	SigningKeys []Key
}

// NewOperator builds an operator trust anchor. IDs are derived from the
// URL-safe base64 form of each public key.
func NewOperator(publicKey ed25519.PublicKey, signingKeys ...ed25519.PublicKey) Operator {
	operator := Operator{ID: PublicKeyID(publicKey), PublicKey: publicKey}
	for _, signingKey := range signingKeys {
		operator.SigningKeys = append(operator.SigningKeys, NewKey(signingKey))
	}
	return operator
}

type trustedSigner struct {
	operator  string
	publicKey ed25519.PublicKey
}

// Authenticator verifies NATS-style leafnode account and user JWTs. Configure
// it with at least one trusted Operator, then verify the account JWT before
// verifying any user JWT issued by that account.
type Authenticator struct {
	mu    sync.RWMutex
	keys  map[string]ed25519.PublicKey
	roots map[string]trustedSigner

	accountRevocations map[string]map[string]int64

	configurationErr error

	// Now may be replaced before concurrent use, primarily for deterministic
	// tests. A nil value uses time.Now.
	Now func() time.Time
	// Leeway is applied to account and user iat/exp comparisons. It is never
	// applied to revocation cutoffs.
	Leeway time.Duration
	// Nonces stores one-time nonce signatures. It is created by
	// NewAuthenticator.
	Nonces *NonceStore
}

// NewAuthenticator returns an authenticator trusting the supplied operators.
// Use AddOperator when configuration errors must be surfaced immediately.
func NewAuthenticator(operators ...Operator) *Authenticator {
	authenticator := &Authenticator{
		keys:               make(map[string]ed25519.PublicKey),
		roots:              make(map[string]trustedSigner),
		accountRevocations: make(map[string]map[string]int64),
		Nonces:             NewNonceStore(),
	}
	for _, operator := range operators {
		if err := authenticator.AddOperator(operator); err != nil &&
			authenticator.configurationErr == nil {
			authenticator.configurationErr = err
		}
	}
	return authenticator
}

// NewVerifier is an alias for NewAuthenticator.
func NewVerifier(operators ...Operator) *Authenticator {
	return NewAuthenticator(operators...)
}

// AddOperator registers an operator identity key and all of its registered
// signing keys as trusted account JWT issuers.
func (authenticator *Authenticator) AddOperator(operator Operator) error {
	if authenticator == nil {
		return fmt.Errorf("netsec: nil authenticator")
	}
	if !validPublicKey(operator.PublicKey) {
		return fmt.Errorf("%w: operator public key", ErrInvalidKey)
	}

	id := strings.TrimSpace(operator.ID)
	if id == "" {
		id = PublicKeyID(operator.PublicKey)
	}

	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	authenticator.initialise()

	if err := authenticator.addKeyLocked(id, operator.PublicKey); err != nil {
		return err
	}
	if err := authenticator.addRootLocked(id, operator.PublicKey, id); err != nil {
		return err
	}

	for _, signingKey := range operator.SigningKeys {
		if !validPublicKey(signingKey.PublicKey) {
			return fmt.Errorf("%w: operator signing key", ErrInvalidKey)
		}
		keyID := strings.TrimSpace(signingKey.ID)
		if keyID == "" {
			keyID = PublicKeyID(signingKey.PublicKey)
		}
		if err := authenticator.addKeyLocked(keyID, signingKey.PublicKey); err != nil {
			return err
		}
		if err := authenticator.addRootLocked(keyID, signingKey.PublicKey, id); err != nil {
			return err
		}
	}
	return nil
}

// TrustOperator is an alias for AddOperator.
func (authenticator *Authenticator) TrustOperator(operator Operator) error {
	return authenticator.AddOperator(operator)
}

// RegisterKey associates an arbitrary key identifier with a public key. This is
// needed when account subjects, account signing_keys, or user subjects are
// application names rather than self-describing public key encodings.
func (authenticator *Authenticator) RegisterKey(id string, publicKey ed25519.PublicKey) error {
	if authenticator == nil {
		return fmt.Errorf("netsec: nil authenticator")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: empty key identifier", ErrInvalidKey)
	}
	if !validPublicKey(publicKey) {
		return fmt.Errorf("%w: registered key", ErrInvalidKey)
	}

	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	authenticator.initialise()
	return authenticator.addKeyLocked(id, publicKey)
}

// VerifyAccount verifies an account JWT's signature, issuer trust, type, and
// expiry. The returned claims identify the account public key in Subject and
// its registered signing keys in NATS.SigningKeys.
func (authenticator *Authenticator) VerifyAccount(token string) (*AccountClaims, error) {
	if err := authenticator.ready(); err != nil {
		return nil, err
	}

	parsed, err := ParseJWT(token)
	if err != nil {
		return nil, err
	}
	claims, err := decodeAccountClaims(parsed.Payload)
	if err != nil {
		return nil, err
	}
	if claims.Type != "" && !strings.EqualFold(claims.Type, TypeAccount) {
		return nil, fmt.Errorf("%w: got %q, want account", ErrWrongTokenType, claims.Type)
	}
	if claims.Issuer == "" || claims.Subject == "" {
		return nil, fmt.Errorf("%w: account issuer or subject is empty", ErrMalformedToken)
	}

	signer, ok := authenticator.rootSigner(claims.Issuer)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUntrustedIssuer, claims.Issuer)
	}
	if !parsed.VerifySignature(signer.publicKey) {
		return nil, ErrInvalidSignature
	}
	if err := authenticator.validateTimes(claims.Claims); err != nil {
		return nil, err
	}
	if _, err := authenticator.resolveKey(claims.Subject); err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidAccountKey, claims.Subject)
	}

	return claims, nil
}

// VerifyAccountJWT is an alias for VerifyAccount.
func (authenticator *Authenticator) VerifyAccountJWT(token string) (*AccountClaims, error) {
	return authenticator.VerifyAccount(token)
}

// VerifyAccountToken is an alias for VerifyAccount.
func (authenticator *Authenticator) VerifyAccountToken(token string) (*AccountClaims, error) {
	return authenticator.VerifyAccount(token)
}

// VerifyUser verifies a user JWT against an already verified account's claims.
// The signer must be the account identity key, or a key listed in the account's
// signing_keys. A signing_keys issuer must include issuer_account exactly equal
// to the account Subject.
func (authenticator *Authenticator) VerifyUser(token string, account *AccountClaims) (*UserClaims, error) {
	if err := authenticator.ready(); err != nil {
		return nil, err
	}
	if account == nil {
		return nil, fmt.Errorf("%w: nil account claims", ErrInvalidAccountKey)
	}

	parsed, err := ParseJWT(token)
	if err != nil {
		return nil, err
	}
	claims, err := decodeUserClaims(parsed.Payload)
	if err != nil {
		return nil, err
	}
	if claims.Type != "" && !strings.EqualFold(claims.Type, TypeUser) {
		return nil, fmt.Errorf("%w: got %q, want user", ErrWrongTokenType, claims.Type)
	}
	if claims.Issuer == "" || claims.Subject == "" {
		return nil, fmt.Errorf("%w: user issuer or subject is empty", ErrMalformedToken)
	}
	if account.Subject == "" {
		return nil, fmt.Errorf("%w: empty account subject", ErrInvalidAccountKey)
	}
	if err := authenticator.validateTimes(account.Claims); err != nil {
		return nil, err
	}

	accountKey, err := authenticator.resolveKey(account.Subject)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidAccountKey, account.Subject)
	}

	var signingKey ed25519.PublicKey
	if claims.Issuer == account.Subject {
		signingKey = accountKey
		if claims.IssuerAccount() != "" && claims.IssuerAccount() != account.Subject {
			return nil, fmt.Errorf("%w: %q != %q", ErrIssuerAccountMismatch,
				claims.IssuerAccount(), account.Subject)
		}
	} else {
		if !contains(account.NATS.SigningKeys, claims.Issuer) {
			return nil, fmt.Errorf("%w: %q", ErrUntrustedSigningKey, claims.Issuer)
		}
		if claims.IssuerAccount() == "" {
			return nil, ErrMissingIssuerAccount
		}
		if claims.IssuerAccount() != account.Subject {
			return nil, fmt.Errorf("%w: %q != %q", ErrIssuerAccountMismatch,
				claims.IssuerAccount(), account.Subject)
		}
		signingKey, err = authenticator.resolveKey(claims.Issuer)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrUntrustedSigningKey, claims.Issuer)
		}
	}

	if !parsed.VerifySignature(signingKey) {
		return nil, ErrInvalidSignature
	}
	if err := authenticator.validateTimes(claims.Claims); err != nil {
		return nil, err
	}
	if _, err := authenticator.resolveKey(claims.Subject); err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidUserKey, claims.Subject)
	}
	if err := authenticator.userRevoked(account, claims); err != nil {
		return nil, err
	}

	return claims, nil
}

// VerifyUserJWT is an alias for VerifyUser.
func (authenticator *Authenticator) VerifyUserJWT(token string, account *AccountClaims) (*UserClaims, error) {
	return authenticator.VerifyUser(token, account)
}

// VerifyUserToken is an alias for VerifyUser.
func (authenticator *Authenticator) VerifyUserToken(token string, account *AccountClaims) (*UserClaims, error) {
	return authenticator.VerifyUser(token, account)
}

// VerifyUserForAccount verifies both the account token and the user token.
func (authenticator *Authenticator) VerifyUserForAccount(userToken, accountToken string) (*UserClaims, error) {
	account, err := authenticator.VerifyAccount(accountToken)
	if err != nil {
		return nil, err
	}
	return authenticator.VerifyUser(userToken, account)
}

// Authenticate is an alias for VerifyUserForAccount.
func (authenticator *Authenticator) Authenticate(userToken, accountToken string) (*UserClaims, error) {
	return authenticator.VerifyUserForAccount(userToken, accountToken)
}

// VerifyUserNonce verifies a raw Ed25519 signature over nonce using the user
// JWT subject public key, then consumes the nonce.
func (authenticator *Authenticator) VerifyUserNonce(user *UserClaims, nonce, signature []byte) error {
	if authenticator == nil || authenticator.Nonces == nil {
		return fmt.Errorf("netsec: nonce store is not configured")
	}
	if user == nil {
		return fmt.Errorf("%w: nil user claims", ErrInvalidUserKey)
	}
	if err := authenticator.validateTimes(user.Claims); err != nil {
		return err
	}
	publicKey, err := authenticator.resolveKey(user.Subject)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrInvalidUserKey, user.Subject)
	}
	return authenticator.Nonces.Verify(publicKey, nonce, signature)
}

// VerifyNonce verifies and consumes a nonce signature with an explicit user
// public key.
func (authenticator *Authenticator) VerifyNonce(publicKey ed25519.PublicKey, nonce, signature []byte) error {
	if authenticator == nil || authenticator.Nonces == nil {
		return fmt.Errorf("netsec: nonce store is not configured")
	}
	return authenticator.Nonces.Verify(publicKey, nonce, signature)
}

// AuthenticateWithNonce verifies the complete account/user/nonce chain.
func (authenticator *Authenticator) AuthenticateWithNonce(
	userToken, accountToken string,
	nonce, signature []byte,
) (*UserClaims, error) {
	user, err := authenticator.VerifyUserForAccount(userToken, accountToken)
	if err != nil {
		return nil, err
	}
	if err := authenticator.VerifyUserNonce(user, nonce, signature); err != nil {
		return nil, err
	}
	return user, nil
}

// IssueNonce creates a one-time challenge. The default size is 32 bytes.
func (authenticator *Authenticator) IssueNonce(size ...int) ([]byte, error) {
	if authenticator == nil || authenticator.Nonces == nil {
		return nil, fmt.Errorf("netsec: nonce store is not configured")
	}
	return authenticator.Nonces.Issue(size...)
}

// Challenge is an alias for IssueNonce.
func (authenticator *Authenticator) Challenge(size ...int) ([]byte, error) {
	return authenticator.IssueNonce(size...)
}

// NonceStore returns the store used for nonce verification.
func (authenticator *Authenticator) NonceStore() *NonceStore {
	if authenticator == nil {
		return nil
	}
	return authenticator.Nonces
}

// SetRevocation records a user revocation cutoff in an account revocation
// table. A user JWT with iat <= revokedAt is rejected.
func (authenticator *Authenticator) SetRevocation(accountID, userID string, revokedAt int64) error {
	if authenticator == nil {
		return fmt.Errorf("netsec: nil authenticator")
	}
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(userID) == "" {
		return fmt.Errorf("%w: empty account or user identifier", ErrRevoked)
	}

	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	authenticator.initialise()
	users := authenticator.accountRevocations[accountID]
	if users == nil {
		users = make(map[string]int64)
		authenticator.accountRevocations[accountID] = users
	}
	if existing, ok := users[userID]; !ok || revokedAt > existing {
		users[userID] = revokedAt
	}
	return nil
}

// RevokeUser records a revocation cutoff using a time value.
func (authenticator *Authenticator) RevokeUser(accountID, userID string, revokedAt time.Time) error {
	return authenticator.SetRevocation(accountID, userID, revokedAt.Unix())
}

// SetRevocations replaces the external revocation table for an account.
func (authenticator *Authenticator) SetRevocations(accountID string, revocations map[string]int64) error {
	if authenticator == nil {
		return fmt.Errorf("netsec: nil authenticator")
	}
	if strings.TrimSpace(accountID) == "" {
		return fmt.Errorf("%w: empty account identifier", ErrRevoked)
	}
	copied := make(map[string]int64, len(revocations))
	for userID, revokedAt := range revocations {
		if userID != "" {
			copied[userID] = revokedAt
		}
	}

	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	authenticator.initialise()
	authenticator.accountRevocations[accountID] = copied
	return nil
}

func (authenticator *Authenticator) ready() error {
	if authenticator == nil {
		return fmt.Errorf("netsec: nil authenticator")
	}
	authenticator.mu.Lock()
	authenticator.initialise()
	configurationErr := authenticator.configurationErr
	rootCount := len(authenticator.roots)
	authenticator.mu.Unlock()

	if configurationErr != nil {
		return configurationErr
	}
	if rootCount == 0 {
		return fmt.Errorf("%w: no trusted operator configured", ErrUntrustedIssuer)
	}
	return nil
}

func (authenticator *Authenticator) initialise() {
	if authenticator.keys == nil {
		authenticator.keys = make(map[string]ed25519.PublicKey)
	}
	if authenticator.roots == nil {
		authenticator.roots = make(map[string]trustedSigner)
	}
	if authenticator.accountRevocations == nil {
		authenticator.accountRevocations = make(map[string]map[string]int64)
	}
	if authenticator.Nonces == nil {
		authenticator.Nonces = NewNonceStore()
	}
}

func (authenticator *Authenticator) addKeyLocked(id string, publicKey ed25519.PublicKey) error {
	if existing, ok := authenticator.keys[id]; ok && !existing.Equal(publicKey) {
		return fmt.Errorf("%w: identifier %q is already bound to another key", ErrInvalidKey, id)
	}
	authenticator.keys[id] = publicKey
	return nil
}

func (authenticator *Authenticator) addRootLocked(id string, publicKey ed25519.PublicKey, operator string) error {
	if existing, ok := authenticator.roots[id]; ok &&
		(!existing.publicKey.Equal(publicKey) || existing.operator != operator) {
		return fmt.Errorf("%w: issuer %q is already trusted differently", ErrInvalidKey, id)
	}
	authenticator.roots[id] = trustedSigner{operator: operator, publicKey: publicKey}
	return nil
}

func (authenticator *Authenticator) rootSigner(id string) (trustedSigner, bool) {
	authenticator.mu.RLock()
	defer authenticator.mu.RUnlock()
	signer, ok := authenticator.roots[id]
	return signer, ok
}

func (authenticator *Authenticator) resolveKey(id string) (ed25519.PublicKey, error) {
	authenticator.mu.RLock()
	if publicKey, ok := authenticator.keys[id]; ok {
		authenticator.mu.RUnlock()
		return publicKey, nil
	}
	authenticator.mu.RUnlock()

	publicKey, err := ParsePublicKeyID(id)
	if err != nil {
		return nil, err
	}
	return publicKey, nil
}

func (authenticator *Authenticator) validateTimes(claims Claims) error {
	if claims.IssuedAt <= 0 || claims.Expires <= 0 {
		return ErrMissingTimeClaim
	}

	now := authenticator.currentTime()
	leeway := authenticator.Leeway
	if leeway < 0 {
		leeway = 0
	}
	if time.Unix(claims.IssuedAt, 0).After(now.Add(leeway)) {
		return fmt.Errorf("%w: iat %d is after %s", ErrNotYetValid,
			claims.IssuedAt, now.UTC().Format(time.RFC3339))
	}
	if !now.Before(time.Unix(claims.Expires, 0).Add(leeway)) {
		return fmt.Errorf("%w: exp %d", ErrExpired, claims.Expires)
	}
	return nil
}

func (authenticator *Authenticator) currentTime() time.Time {
	if authenticator.Now != nil {
		return authenticator.Now()
	}
	return time.Now()
}

func (authenticator *Authenticator) userRevoked(account *AccountClaims, user *UserClaims) error {
	var cutoff int64
	var revoked bool
	if revokedAt, ok := account.NATS.Revocations[user.Subject]; ok {
		cutoff, revoked = revokedAt, true
	}

	authenticator.mu.RLock()
	if users := authenticator.accountRevocations[account.Subject]; users != nil {
		if revokedAt, ok := users[user.Subject]; ok && (!revoked || revokedAt > cutoff) {
			cutoff, revoked = revokedAt, true
		}
	}
	authenticator.mu.RUnlock()

	if revoked && user.IssuedAt <= cutoff {
		return fmt.Errorf("%w: iat %d <= revocation %d", ErrRevoked, user.IssuedAt, cutoff)
	}
	return nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
