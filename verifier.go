// Package netsec verifies OpenID Connect ID tokens in a way that binds the
// token's issuer to the keys used to validate its signature.
//
// A verifier is constructed for one specific issuer. It only ever uses keys
// published by that issuer (via its OIDC discovery document and JWKS endpoint,
// or via keys configured explicitly for that issuer) and it rejects any token
// whose "iss" claim does not exactly match the configured issuer. Cached keys
// are therefore never shared between issuers, which prevents a token signed by
// issuer A from being accepted for a session that selected issuer B simply
// because the key happens to be cached locally.
package netsec

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Well-known OIDC discovery path appended to an issuer identifier.
const discoveryPath = "/.well-known/openid-configuration"

// Errors returned by Verify. Callers should use errors.Is rather than matching
// on message text.
var (
	// ErrMalformedToken means the input is not a compact JWS made of three
	// base64url-encoded segments.
	ErrMalformedToken = errors.New("netsec: malformed ID token")
	// ErrIssuerMismatch means the token's "iss" claim does not exactly equal
	// the issuer selected for the current session.
	ErrIssuerMismatch = errors.New("netsec: issuer mismatch")
	// ErrKeyNotFound means no key published by the token's issuer matches the
	// token's "kid" header.
	ErrKeyNotFound = errors.New("netsec: signing key not found for issuer")
	// ErrUnsupportedAlg means the JWT "alg" header is not supported.
	ErrUnsupportedAlg = errors.New("netsec: unsupported signing algorithm")
	// ErrSignature means the cryptographic signature did not verify.
	ErrSignature = errors.New("netsec: invalid signature")
	// ErrClaim means a standard claim (exp, nbf, iat, aud, nonce) failed.
	ErrClaim = errors.New("netsec: invalid claim")
)

// IDToken is the subset of OIDC ID token claims relevant to verification.
// All claims remain available in Claims.
type IDToken struct {
	Issuer    string
	Subject   string
	Audience  []string
	Expiry    time.Time
	NotBefore time.Time
	IssuedAt  time.Time
	Nonce     string
	Alg       string
	KeyID     string
	Claims    map[string]any
}

// Config configures a Verifier for a single OIDC issuer.
type Config struct {
	// Issuer is the exact expected value of every token's "iss" claim and the
	// base URL used for OIDC discovery. Required.
	Issuer string
	// Audience, when non-empty, requires the token's "aud" claim to contain
	// every listed value. When empty the audience claim is not checked.
	Audience []string
	// Nonce, when non-empty, must equal the token's "nonce" claim.
	Nonce string
	// Keys are static keys bound to Issuer. When set, discovery and the JWKS
	// endpoint are never contacted and only these keys are used. This is useful
	// for pinned keys and for tests.
	Keys *KeySet
	// HTTPClient performs discovery/JWKS requests. Defaults to
	// http.DefaultClient.
	HTTPClient *http.Client
	// Now overrides the current time, mainly for tests.
	Now func() time.Time
	// Leeway extends the tolerance around exp/nbf/iat. Defaults to zero.
	Leeway time.Duration
	// MaxTokenSize caps the accepted compact serialization length.
	MaxTokenSize int
}

// Verifier verifies ID tokens for exactly one issuer. It is safe for
// concurrent use.
type Verifier struct {
	issuer     string
	audience   []string
	nonce      string
	staticKeys *KeySet
	httpClient *http.Client
	now        func() time.Time
	leeway     time.Duration
	maxSize    int

	mu         sync.Mutex
	discovered bool
	jwksURI    string
	cachedKeys *KeySet

	discoveryMu sync.Mutex
}

// NewVerifier returns a Verifier bound to cfg.Issuer. The issuer's keys are
// isolated from any other verifier: a key cached here can never be used to
// verify a token for a different issuer.
func NewVerifier(cfg Config) (*Verifier, error) {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, errors.New("netsec: Config.Issuer is required")
	}
	if _, err := url.Parse(cfg.Issuer); err != nil {
		return nil, fmt.Errorf("netsec: invalid issuer URL: %w", err)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	maxSize := cfg.MaxTokenSize
	if maxSize <= 0 {
		maxSize = 64 * 1024
	}
	return &Verifier{
		issuer:     cfg.Issuer,
		audience:   append([]string(nil), cfg.Audience...),
		nonce:      cfg.Nonce,
		staticKeys: cfg.Keys,
		httpClient: client,
		now:        now,
		leeway:     cfg.Leeway,
		maxSize:    maxSize,
	}, nil
}

// Issuer returns the exact issuer identifier the verifier was configured for.
func (v *Verifier) Issuer() string {
	return v.issuer
}
