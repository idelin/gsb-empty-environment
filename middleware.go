package netsec

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Config configures token authentication and proof-of-possession enforcement.
//
// A token without cnf follows normal bearer-token rules. A token with cnf is
// never accepted as a plain bearer: it must be accompanied by a matching DPoP
// proof (cnf.jkt) or a matching TLS client certificate (cnf["x5t#S256"] or
// cnf.x5t). Missing proof material is rejected rather than downgraded.
type Config struct {
	// KeyFunc resolves the access-token verification key from the parsed token.
	// It takes precedence over Keys, Key, HMACSecret, and PublicKey.
	KeyFunc func(*Token) (any, error)

	// Keys maps JWT kid values to verification keys.
	Keys map[string]any

	// Key is a single verification key. An HMAC secret may be provided as
	// []byte or string; asymmetric keys may be *rsa.PublicKey,
	// *ecdsa.PublicKey, ed25519.PublicKey, a PEM value, or a certificate.
	Key any

	// HMACSecret is a convenience field for HS256/HS384/HS512 tokens.
	HMACSecret []byte

	// PublicKey is a convenience field for asymmetric tokens.
	PublicKey any

	// Algorithms limits accepted JWT algorithms, for example []string{"RS256"}.
	// When empty, the algorithm must still be supported and match the key type.
	Algorithms []string

	// Issuer, when non-empty, requires an exact iss claim match.
	Issuer string

	// Audience, when non-empty, requires the aud claim to contain this value.
	Audience string

	// ClockSkew is the allowed clock difference for exp, nbf, and iat.
	ClockSkew time.Duration

	// Now overrides the current time, primarily for tests.
	Now func() time.Time

	// DPoPLeeway is the allowed clock difference for a proof's iat. The default
	// is one minute.
	DPoPLeeway time.Duration

	// DPoPMaxAge is the maximum accepted age of a DPoP proof. The default is
	// five minutes.
	DPoPMaxAge time.Duration

	// DPoPReplayCache records proof jti values. Nil installs a per-authenticator
	// in-memory cache; set NoReplayCache{} to disable replay checks.
	DPoPReplayCache ReplayCache

	// RequireATH requires the DPoP proof to contain an ath claim. When false,
	// an ath claim is still validated if present.
	RequireATH bool

	// ClientCertificate extracts the TLS client certificate used for certificate
	// bound tokens. It defaults to the request context value installed by
	// WithClientCertificate and then r.TLS.PeerCertificates[0].
	ClientCertificate func(*http.Request) *x509.Certificate

	// Realm is included in WWW-Authenticate challenges. The default is "resource".
	Realm string

	// ErrorHandler may replace the default 401 response. The WWW-Authenticate
	// header and status code have already been selected when it is called.
	ErrorHandler func(http.ResponseWriter, *http.Request, error)
}

// Identity is the authenticated principal attached to a request context.
type Identity struct {
	Subject      string
	Claims       map[string]any
	Token        *Token
	Confirmation *Confirmation

	// Proof is non-nil when the token was authorized by a DPoP proof.
	Proof *DPoPProof

	// Certificate is non-nil when the token was authorized by a client
	// certificate.
	Certificate *x509.Certificate
}

// Authenticator verifies access tokens and enforces their cnf constraints.
type Authenticator struct {
	cfg Config
}

// New creates an Authenticator. It fails closed when no verification key
// source is configured.
func New(cfg Config) (*Authenticator, error) {
	if cfg.KeyFunc == nil && len(cfg.Keys) == 0 && cfg.Key == nil && len(cfg.HMACSecret) == 0 && cfg.PublicKey == nil {
		return nil, errors.New("netsec: no access-token verification key configured")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.DPoPReplayCache == nil {
		cfg.DPoPReplayCache = NewMemoryReplayCache()
	}
	if cfg.Realm == "" {
		cfg.Realm = "resource"
	}
	return &Authenticator{cfg: cfg}, nil
}

// MustNew is like New but panics on invalid configuration.
func MustNew(cfg Config) *Authenticator {
	a, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return a
}

// Authenticate verifies the request's access token and its proof of possession.
// It returns an *AuthError for authentication failures.
func (a *Authenticator) Authenticate(r *http.Request) (*Identity, error) {
	if a == nil {
		return nil, &AuthError{Code: "invalid_token", Status: http.StatusUnauthorized, Description: "authenticator is not configured"}
	}
	if r == nil {
		return nil, &AuthError{Code: "invalid_request", Status: http.StatusUnauthorized, Description: "missing request"}
	}

	raw, err := accessTokenFromRequest(r)
	if err != nil {
		return nil, &AuthError{Code: "invalid_request", Status: http.StatusUnauthorized, Description: err.Error()}
	}

	token, err := ParseToken(raw)
	if err != nil {
		return nil, invalidToken(err)
	}
	if !algorithmAllowed(a.cfg, token.Algorithm()) {
		return nil, invalidToken(fmt.Errorf("access token algorithm %q is not allowed", token.Algorithm()))
	}

	key, err := a.resolveKey(token)
	if err != nil {
		return nil, invalidToken(err)
	}
	if err := verifyTokenSignature(token, key); err != nil {
		return nil, invalidToken(err)
	}

	now := a.cfg.Now().UTC()
	if err := validateTokenClaims(token, a.cfg, now); err != nil {
		return nil, invalidToken(err)
	}

	identity := &Identity{
		Subject: token.Subject(),
		Claims:  token.Claims,
		Token:   token,
	}
	confirmation, present, err := token.Confirmation()
	if err != nil {
		return nil, invalidToken(err)
	}
	if !present {
		return identity, nil
	}
	if !confirmation.RequiresProof() {
		return nil, invalidToken(errors.New("cnf claim does not contain a supported key or certificate binding"))
	}
	identity.Confirmation = &confirmation

	if err := a.authenticateBoundRequest(r, token, confirmation, identity, now); err != nil {
		return nil, err
	}
	return identity, nil
}

func (a *Authenticator) authenticateBoundRequest(r *http.Request, token *Token, confirmation Confirmation, identity *Identity, now time.Time) error {
	var proofErr error
	var certErr error
	hasJKT := confirmation.JWKThumbprint != ""
	hasCertificate := confirmation.CertificateThumbprintSHA256 != "" || confirmation.CertificateThumbprintSHA1 != ""

	if hasJKT {
		proof, err := verifyDPoPProof(r, token.Raw, confirmation.JWKThumbprint, a.cfg, now)
		if err == nil {
			identity.Proof = proof
			return nil
		}
		proofErr = err
	}

	if hasCertificate {
		cert := a.clientCertificate(r)
		if cert != nil {
			if certificateMatchesConfirmation(cert, confirmation) {
				identity.Certificate = cert
				return nil
			}
			certErr = errors.New("client certificate does not match the access token cnf thumbprint")
		} else {
			certErr = errors.New("a client certificate is required for this access token")
		}
	}

	switch {
	case hasJKT && hasCertificate:
		return invalidDPoP(fmt.Errorf("access token requires a matching proof or certificate: DPoP: %v; certificate: %v", proofErr, certErr))
	case hasJKT:
		return invalidDPoP(proofErr)
	case hasCertificate:
		return invalidToken(certErr)
	default:
		return invalidToken(errors.New("cnf claim does not contain a supported key or certificate binding"))
	}
}

func (a *Authenticator) resolveKey(token *Token) (any, error) {
	cfg := a.cfg
	if cfg.KeyFunc != nil {
		return cfg.KeyFunc(token)
	}
	if len(cfg.Keys) > 0 {
		kid := token.KeyID()
		if kid != "" {
			key, ok := cfg.Keys[kid]
			if !ok {
				return nil, fmt.Errorf("access token kid %q is not configured", kid)
			}
			return key, nil
		}
		if len(cfg.Keys) == 1 {
			for _, key := range cfg.Keys {
				return key, nil
			}
		}
		return nil, errors.New("access token has no kid")
	}
	if cfg.Key != nil {
		return cfg.Key, nil
	}
	if strings.HasPrefix(token.Algorithm(), "HS") {
		if len(cfg.HMACSecret) > 0 {
			return cfg.HMACSecret, nil
		}
		if cfg.PublicKey != nil {
			return cfg.PublicKey, nil
		}
		return nil, errors.New("no HMAC secret is configured")
	}
	if cfg.PublicKey != nil {
		return cfg.PublicKey, nil
	}
	if len(cfg.HMACSecret) > 0 {
		return cfg.HMACSecret, nil
	}
	return nil, errors.New("no verification key is configured for the access token algorithm")
}

func (a *Authenticator) clientCertificate(r *http.Request) *x509.Certificate {
	if a.cfg.ClientCertificate != nil {
		return a.cfg.ClientCertificate(r)
	}
	if cert, ok := ClientCertificateFromContext(r.Context()); ok {
		return cert
	}
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		return r.TLS.PeerCertificates[0]
	}
	return nil
}

// Middleware returns a handler that authenticates each request and stores the
// resulting Identity in the request context.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := a.Authenticate(r)
		if err != nil {
			a.unauthorized(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), identityContextKey{}, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Wrap is an alias for Middleware.
func (a *Authenticator) Wrap(next http.Handler) http.Handler {
	return a.Middleware(next)
}

// Middleware is a convenience wrapper around New and Authenticator.Middleware.
// Invalid configuration produces a fail-closed 500 handler.
func Middleware(next http.Handler, cfg ...Config) http.Handler {
	if len(cfg) == 0 {
		return configErrorHandler(errors.New("netsec: missing middleware configuration"))
	}
	authenticator, err := New(cfg[0])
	if err != nil {
		return configErrorHandler(err)
	}
	return authenticator.Middleware(next)
}

// NewMiddleware returns a middleware function suitable for router chains:
// handler = netsec.NewMiddleware(cfg)(resourceHandler).
func NewMiddleware(cfg Config) func(http.Handler) http.Handler {
	authenticator, err := New(cfg)
	if err != nil {
		return func(next http.Handler) http.Handler {
			return configErrorHandler(err)
		}
	}
	return authenticator.Middleware
}

func configErrorHandler(err error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	})
}

func (a *Authenticator) unauthorized(w http.ResponseWriter, r *http.Request, err error) {
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		authErr = invalidToken(err)
	}
	status := authErr.Status
	if status == 0 {
		status = http.StatusUnauthorized
	}
	challenge := authErr.Challenge
	if challenge == "" {
		challenge = "Bearer"
	}

	parts := []string{challenge}
	if a.cfg.Realm != "" {
		parts = append(parts, fmt.Sprintf("realm=%q", a.cfg.Realm))
	}
	if authErr.Code != "" {
		parts = append(parts, fmt.Sprintf("error=%q", authErr.Code))
	}
	w.Header().Set("WWW-Authenticate", strings.Join(parts, " "))

	if a.cfg.ErrorHandler != nil {
		a.cfg.ErrorHandler(w, r, err)
		return
	}
	http.Error(w, authErr.Description, status)
}

// AuthError describes an authentication failure and the challenge to send.
type AuthError struct {
	Code        string
	Status      int
	Challenge   string
	Description string
	Err         error
}

func (e *AuthError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Description != "" {
		return e.Description
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "authentication failed"
}

// Unwrap returns the underlying cause.
func (e *AuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func invalidToken(err error) *AuthError {
	return &AuthError{Code: "invalid_token", Status: http.StatusUnauthorized, Description: err.Error(), Err: err}
}

func invalidDPoP(err error) *AuthError {
	return &AuthError{
		Code:        "invalid_dpop_proof",
		Status:      http.StatusUnauthorized,
		Challenge:   "DPoP",
		Description: err.Error(),
		Err:         err,
	}
}

func accessTokenFromRequest(r *http.Request) (string, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", errors.New("missing Authorization header")
	}
	scheme, token, found := strings.Cut(header, " ")
	if !found {
		return "", errors.New("malformed Authorization header")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("empty access token")
	}
	switch strings.ToLower(scheme) {
	case "bearer", "dpop":
		return token, nil
	default:
		return "", fmt.Errorf("unsupported Authorization scheme %q", scheme)
	}
}

func certificateMatchesConfirmation(cert *x509.Certificate, confirmation Confirmation) bool {
	if cert == nil {
		return false
	}
	if confirmation.CertificateThumbprintSHA256 != "" {
		sum := sha256.Sum256(cert.Raw)
		if thumbprintMatches(confirmation.CertificateThumbprintSHA256, sum[:]) {
			return true
		}
	}
	if confirmation.CertificateThumbprintSHA1 != "" {
		sum := sha1.Sum(cert.Raw)
		if thumbprintMatches(confirmation.CertificateThumbprintSHA1, sum[:]) {
			return true
		}
	}
	return false
}

func thumbprintMatches(expected string, sum []byte) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	values := []string{
		base64.RawURLEncoding.EncodeToString(sum),
		base64.URLEncoding.EncodeToString(sum),
		base64.RawStdEncoding.EncodeToString(sum),
		base64.StdEncoding.EncodeToString(sum),
		hex.EncodeToString(sum),
		strings.ToUpper(hex.EncodeToString(sum)),
	}
	for _, value := range values {
		if subtle.ConstantTimeCompare([]byte(value), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}

type identityContextKey struct{}

// IdentityFromContext returns the Identity stored by the middleware.
func IdentityFromContext(ctx context.Context) (*Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(*Identity)
	return identity, ok
}

type clientCertificateContextKey struct{}

// WithClientCertificate attaches a client certificate to a request context for
// deployments where TLS termination happens outside the Go process and a
// trusted adapter has already extracted the certificate.
func WithClientCertificate(ctx context.Context, cert *x509.Certificate) context.Context {
	return context.WithValue(ctx, clientCertificateContextKey{}, cert)
}

// ClientCertificateFromContext returns the certificate installed by
// WithClientCertificate.
func ClientCertificateFromContext(ctx context.Context) (*x509.Certificate, bool) {
	cert, ok := ctx.Value(clientCertificateContextKey{}).(*x509.Certificate)
	return cert, ok
}
