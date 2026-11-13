package netsec

import (
	"time"
)

// KeyFunc resolves a verification key from the protected JWS header. Returning
// (nil, nil) falls back to the key configured with WithVerificationKey and then
// to an embedded "jwk" header, if allowed.
type KeyFunc func(header map[string]any) (any, error)

// ScopeFunc derives the nonce scope from a proof. The scope normally identifies
// the credential request or access token that received the c_nonce.
type ScopeFunc func(proof *Proof) string

// Option configures a Verifier.
type Option func(*Verifier) error

// Verifier validates OID4VCI credential proofs. A proof is accepted only when
// its signature verifies, every "aud" value equals the credential endpoint, and
// its "nonce" is the current unused c_nonce for the selected scope.
type Verifier struct {
	audience string

	key              any
	keyFunc          KeyFunc
	allowEmbeddedJWK bool

	nonceStore NonceStore
	scopeFunc  ScopeFunc

	clock             func() time.Time
	leeway            time.Duration
	maxAge            time.Duration
	requireIssuedAt   bool
	requireExpiration bool

	requiredType      string
	allowedAlgorithms map[string]bool
}

// ProofValidator is an alias for Verifier.
type ProofValidator = Verifier

// ProofVerifier is an alias for Verifier.
type ProofVerifier = Verifier

// CredentialProofVerifier is an alias for Verifier.
type CredentialProofVerifier = Verifier

// NewVerifier creates a proof verifier for audience, which must be the exact
// credential endpoint URL advertised in the proof's "aud" claim.
func NewVerifier(audience string, options ...Option) (*Verifier, error) {
	if audience == "" {
		return nil, proofError(ErrAudienceMismatch, "credential endpoint audience is required")
	}

	verifier := &Verifier{
		audience:          audience,
		nonceStore:        NewMemoryNonceStore(),
		clock:             time.Now,
		allowEmbeddedJWK:  true,
		allowedAlgorithms: supportedAlgorithms(),
	}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(verifier); err != nil {
			return nil, err
		}
	}
	if verifier.nonceStore == nil {
		return nil, proofError(ErrNonceStore, "nonce store must not be nil")
	}
	if verifier.clock == nil {
		verifier.clock = time.Now
	}
	return verifier, nil
}

// NewProofVerifier is an alias for NewVerifier.
func NewProofVerifier(audience string, options ...Option) (*Verifier, error) {
	return NewVerifier(audience, options...)
}

// NewProofValidator is an alias for NewVerifier.
func NewProofValidator(audience string, options ...Option) (*Verifier, error) {
	return NewVerifier(audience, options...)
}

// NewCredentialProofVerifier is an alias for NewVerifier.
func NewCredentialProofVerifier(audience string, options ...Option) (*Verifier, error) {
	return NewVerifier(audience, options...)
}

// WithVerificationKey configures a trusted verification key. Supported values
// include HMAC secrets, RSA/ECDSA/Ed25519 public keys, private keys (their public
// half is used), PEM data, X.509 certificates, JWK maps and JSON.
func WithVerificationKey(key any) Option {
	return func(verifier *Verifier) error {
		verifier.key = key
		return nil
	}
}

// WithPublicKey is an alias for WithVerificationKey.
func WithPublicKey(key any) Option {
	return WithVerificationKey(key)
}

// WithHMACSecret configures an HMAC verification secret. Prefer asymmetric
// algorithms for real OID4VCI deployments.
func WithHMACSecret(secret []byte) Option {
	return WithVerificationKey(append([]byte(nil), secret...))
}

// WithKeyFunc configures dynamic verification-key selection, for example by
// "kid". The callback receives a copy of the protected header.
func WithKeyFunc(keyFunc KeyFunc) Option {
	return func(verifier *Verifier) error {
		if keyFunc == nil {
			return proofError(ErrInvalidKey, "key function must not be nil")
		}
		verifier.keyFunc = keyFunc
		return nil
	}
}

// WithNonceStore supplies the issuer's c_nonce store. The store's Consume method
// must check the current nonce and mark it used atomically.
func WithNonceStore(store NonceStore) Option {
	return func(verifier *Verifier) error {
		if store == nil {
			return proofError(ErrNonceStore, "nonce store must not be nil")
		}
		verifier.nonceStore = store
		return nil
	}
}

// WithScopeFunc derives the c_nonce scope from the authenticated proof. Without
// it, all proofs share the default scope.
func WithScopeFunc(scopeFunc ScopeFunc) Option {
	return func(verifier *Verifier) error {
		if scopeFunc == nil {
			return proofError(ErrNonceStore, "scope function must not be nil")
		}
		verifier.scopeFunc = scopeFunc
		return nil
	}
}

// WithClock overrides the clock used for iat/nbf/exp checks.
func WithClock(clock func() time.Time) Option {
	return func(verifier *Verifier) error {
		if clock == nil {
			return proofError(ErrInvalidClaim, "clock must not be nil")
		}
		verifier.clock = clock
		return nil
	}
}

// WithLeeway allows the given tolerance when checking iat, nbf and exp.
func WithLeeway(leeway time.Duration) Option {
	return func(verifier *Verifier) error {
		if leeway < 0 {
			return proofError(ErrInvalidClaim, "leeway must not be negative")
		}
		verifier.leeway = leeway
		return nil
	}
}

// WithMaxProofAge rejects proofs whose iat is older than maxAge. A zero value
// disables the age check.
func WithMaxProofAge(maxAge time.Duration) Option {
	return func(verifier *Verifier) error {
		if maxAge < 0 {
			return proofError(ErrInvalidClaim, "maximum proof age must not be negative")
		}
		verifier.maxAge = maxAge
		return nil
	}
}

// WithRequireIssuedAt requires the proof to contain iat.
func WithRequireIssuedAt() Option {
	return func(verifier *Verifier) error {
		verifier.requireIssuedAt = true
		return nil
	}
}

// WithRequireExpiration requires the proof to contain exp.
func WithRequireExpiration() Option {
	return func(verifier *Verifier) error {
		verifier.requireExpiration = true
		return nil
	}
}

// WithRequiredType requires the JWT "typ" header to equal typ, for example
// "openid4vci-proof+jwt".
func WithRequiredType(typ string) Option {
	return func(verifier *Verifier) error {
		if typ == "" {
			return proofError(ErrProofType, "required proof type must not be empty")
		}
		verifier.requiredType = typ
		return nil
	}
}

// WithAllowedAlgorithms restricts the accepted JWS algorithms.
func WithAllowedAlgorithms(algorithms ...string) Option {
	return func(verifier *Verifier) error {
		if len(algorithms) == 0 {
			return proofError(ErrUnsupportedAlgorithm, "at least one algorithm is required")
		}
		allowed := make(map[string]bool, len(algorithms))
		for _, algorithm := range algorithms {
			allowed[algorithm] = true
		}
		verifier.allowedAlgorithms = allowed
		return nil
	}
}

// WithoutEmbeddedJWK disables verification with the proof header's "jwk".
// A trusted key or key function is then required.
func WithoutEmbeddedJWK() Option {
	return func(verifier *Verifier) error {
		verifier.allowEmbeddedJWK = false
		return nil
	}
}

// Audience returns the credential endpoint bound to this verifier.
func (v *Verifier) Audience() string {
	return v.audience
}

// IssueNonce creates and installs a fresh c_nonce in the default scope.
func (v *Verifier) IssueNonce() (string, error) {
	return v.IssueNonceForScope("")
}

// IssueCNonce is an alias for IssueNonce.
func (v *Verifier) IssueCNonce() (string, error) {
	return v.IssueNonce()
}

// NewNonce is an alias for IssueNonce.
func (v *Verifier) NewNonce() (string, error) {
	return v.IssueNonce()
}

// IssueNonceForScope creates and installs a fresh c_nonce for scope.
func (v *Verifier) IssueNonceForScope(scope string) (string, error) {
	nonce, err := v.nonceStore.Issue(scope)
	if err != nil {
		return "", proofErrorWithCause(ErrNonceStore, err, "issue c_nonce")
	}
	return nonce, nil
}

// CurrentNonce returns the current default-scope c_nonce when the configured
// store supports inspection.
func (v *Verifier) CurrentNonce() (string, bool) {
	store, ok := v.nonceStore.(interface {
		Current(scope string) (string, bool)
	})
	if !ok {
		return "", false
	}
	return store.Current("")
}

// ValidateProof parses, authenticates and validates a credential proof. The
// nonce is consumed only after the signature, audience and time checks pass.
func (v *Verifier) ValidateProof(raw string) error {
	proof, err := ParseProof(raw)
	if err != nil {
		return err
	}
	return v.validateParsedProof(proof, nil)
}

// VerifyProof is an alias for ValidateProof.
func (v *Verifier) VerifyProof(raw string) error {
	return v.ValidateProof(raw)
}

// Validate is an alias for ValidateProof.
func (v *Verifier) Validate(raw string) error {
	return v.ValidateProof(raw)
}

// ValidateProofForScope validates a proof against an explicitly supplied nonce
// scope, ignoring any configured ScopeFunc.
func (v *Verifier) ValidateProofForScope(raw, scope string) error {
	proof, err := ParseProof(raw)
	if err != nil {
		return err
	}
	return v.validateParsedProof(proof, &scope)
}

// VerifyProofForScope is an alias for ValidateProofForScope.
func (v *Verifier) VerifyProofForScope(raw, scope string) error {
	return v.ValidateProofForScope(raw, scope)
}

// ParseAndValidateProof validates a proof and returns its parsed form so the
// caller can inspect application-specific claims after authentication.
func (v *Verifier) ParseAndValidateProof(raw string) (*Proof, error) {
	proof, err := ParseProof(raw)
	if err != nil {
		return nil, err
	}
	if err := v.validateParsedProof(proof, nil); err != nil {
		return nil, err
	}
	return proof, nil
}

func (v *Verifier) scopeFor(proof *Proof) string {
	if v.scopeFunc == nil {
		return ""
	}
	return v.scopeFunc(proof)
}

func (v *Verifier) validateParsedProof(proof *Proof, explicitScope *string) error {
	if err := validateHeader(proof, v.allowedAlgorithms, v.requiredType); err != nil {
		return err
	}
	if err := v.validateTimes(proof); err != nil {
		return err
	}
	if err := v.validateAudience(proof); err != nil {
		return err
	}

	key, err := v.resolveKey(proof)
	if err != nil {
		return err
	}
	if err := verifySignature(proof, key); err != nil {
		return err
	}

	scope := ""
	if explicitScope != nil {
		scope = *explicitScope
	} else {
		scope = v.scopeFor(proof)
	}
	status, err := v.nonceStore.Consume(scope, proof.Nonce)
	if err != nil {
		return proofErrorWithCause(ErrNonceStore, err, "consume c_nonce")
	}
	switch status {
	case NonceAccepted:
		return nil
	case NonceMismatch:
		return proofError(ErrNonceMismatch, "nonce is not the current c_nonce")
	case NonceReused:
		return proofError(ErrNonceReused, "c_nonce has already been used")
	default:
		return proofError(ErrNonceRejected, "nonce store returned status %d", status)
	}
}

func (v *Verifier) validateAudience(proof *Proof) error {
	if len(proof.Audiences) == 0 {
		return proofError(ErrAudienceMismatch, `"aud" is empty`)
	}
	for _, audience := range proof.Audiences {
		if !equalConstantTime(audience, v.audience) {
			return proofError(ErrAudienceMismatch, "got %q, want %q", audience, v.audience)
		}
	}
	return nil
}

func (v *Verifier) validateTimes(proof *Proof) error {
	now := v.clock()
	if v.requireIssuedAt && !proof.HasIssuedAt {
		return proofError(ErrInvalidClaim, `"iat" claim is required`)
	}
	if v.requireExpiration && !proof.HasExpiresAt {
		return proofError(ErrInvalidClaim, `"exp" claim is required`)
	}

	if proof.HasExpiresAt && !now.Before(proof.ExpiresAt.Add(v.leeway)) {
		return proofError(ErrExpiredProof, "expired at %s", proof.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if proof.HasNotBefore && now.Add(v.leeway).Before(proof.NotBefore) {
		return proofError(ErrProofNotYetValid, "not valid before %s", proof.NotBefore.UTC().Format(time.RFC3339))
	}
	if proof.HasIssuedAt {
		if proof.IssuedAt.After(now.Add(v.leeway)) {
			return proofError(ErrProofNotYetValid, "issued in the future at %s", proof.IssuedAt.UTC().Format(time.RFC3339))
		}
		if v.maxAge > 0 && now.Sub(proof.IssuedAt) > v.maxAge+v.leeway {
			return proofError(ErrExpiredProof, "issued too long ago at %s", proof.IssuedAt.UTC().Format(time.RFC3339))
		}
	}
	return nil
}

func (v *Verifier) resolveKey(proof *Proof) (any, error) {
	if v.keyFunc != nil {
		key, err := v.keyFunc(copyHeader(proof.Header))
		if err != nil {
			return nil, proofErrorWithCause(ErrInvalidKey, err, "resolve verification key")
		}
		if key != nil {
			return key, nil
		}
	}
	if v.key != nil {
		return v.key, nil
	}
	if v.allowEmbeddedJWK {
		rawJWK, ok := proof.Header["jwk"]
		if ok {
			object, ok := rawJWK.(map[string]any)
			if !ok {
				return nil, proofError(ErrInvalidKey, `"jwk" header must be an object`)
			}
			return parseJWK(object, proof.Algorithm)
		}
	}
	return nil, proofError(ErrMissingKey, "configure a key or include a public JWK in the proof header")
}

func copyHeader(header map[string]any) map[string]any {
	copied := make(map[string]any, len(header))
	for key, value := range header {
		copied[key] = value
	}
	return copied
}

func supportedAlgorithms() map[string]bool {
	algorithms := []string{
		"HS256", "HS384", "HS512",
		"RS256", "RS384", "RS512",
		"PS256", "PS384", "PS512",
		"ES256", "ES384", "ES512",
		"EdDSA",
	}
	allowed := make(map[string]bool, len(algorithms))
	for _, algorithm := range algorithms {
		allowed[algorithm] = true
	}
	return allowed
}
