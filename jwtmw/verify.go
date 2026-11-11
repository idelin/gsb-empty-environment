package jwtmw

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxJWKSBytes = 1 << 20

// Sentinel errors distinguishing authentication failures (401) from upstream
// JWKS infrastructure failures (503).
var (
	ErrMalformedToken   = errors.New("jwtmw: malformed token")
	ErrAlgNone          = errors.New("jwtmw: alg none is not accepted")
	ErrUnsupportedAlg   = errors.New("jwtmw: unsupported signing algorithm")
	ErrUnknownKid       = errors.New("jwtmw: unknown kid")
	ErrBadSignature     = errors.New("jwtmw: signature verification failed")
	ErrInvalidClaims    = errors.New("jwtmw: token claims are not valid")
	ErrCacheExpired     = errors.New("jwtmw: JWKS cache expired and refresh failed")
	ErrJWKSUnavailable  = errors.New("jwtmw: JWKS unavailable")
	ErrRefreshThrottled = errors.New("jwtmw: JWKS refresh throttled")
)

// Config configures the verifier and its HTTP middleware.
type Config struct {
	// JWKSURI is the HTTPS endpoint serving the JSON Web Key Set.
	JWKSURI string

	// CacheTTL marks a fetched JWKS document stale after this duration.
	CacheTTL time.Duration

	// GracePeriod keeps rotated-out keys usable after they disappear from JWKS.
	GracePeriod time.Duration

	// MinRefreshInterval rate limits JWKS fetch attempts while the cache is
	// fresh. Concurrent requests are always coalesced into one fetch.
	MinRefreshInterval time.Duration

	// AllowedAlgs restricts the accepted header alg values. When empty, all
	// asymmetric algorithms supported by the key type are allowed.
	AllowedAlgs []string

	// Issuer, when set, must match the "iss" claim.
	Issuer string

	// Audience, when set, must be present in the "aud" claim.
	Audience string

	// Leeway tolerates small clock skew for exp/nbf checks.
	Leeway time.Duration

	// HTTPClient fetches the JWKS document. Defaults to a client with a
	// 10 second timeout.
	HTTPClient *http.Client

	// now is overridable in tests; production code uses time.Now.
	now func() time.Time
}

// Verifier verifies JWTs against a cached, self-refreshing JWKS endpoint.
type Verifier struct {
	cfg    Config
	client *http.Client

	allowedAlgs map[string]bool

	mu          sync.Mutex
	keys        map[string]*keyEntry
	fetchedAt   time.Time
	expiresAt   time.Time
	lastAttempt time.Time
	inflight    *refreshCall
}

// New builds a Verifier and validates the configuration.
func New(cfg Config) (*Verifier, error) {
	if cfg.JWKSURI == "" {
		return nil, errors.New("jwtmw: JWKSURI is required")
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 10 * time.Minute
	}
	if cfg.GracePeriod < 0 {
		return nil, errors.New("jwtmw: GracePeriod must not be negative")
	}
	if cfg.MinRefreshInterval < 0 {
		return nil, errors.New("jwtmw: MinRefreshInterval must not be negative")
	}
	if cfg.Leeway < 0 {
		return nil, errors.New("jwtmw: Leeway must not be negative")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}

	allowed := make(map[string]bool, len(cfg.AllowedAlgs))
	for _, alg := range cfg.AllowedAlgs {
		if !knownAsymmetricAlg(alg) {
			return nil, fmt.Errorf("jwtmw: allowed alg %q is not an accepted asymmetric algorithm", alg)
		}
		allowed[alg] = true
	}

	return &Verifier{
		cfg:         cfg,
		client:      cfg.HTTPClient,
		allowedAlgs: allowed,
		keys:        map[string]*keyEntry{},
	}, nil
}

func (v *Verifier) now() time.Time {
	return v.cfg.now()
}

// Header is the subset of the JWT protected header that matters here.
type Header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// Claims holds the registered claims used for validation.
type Claims struct {
	Issuer    string          `json:"iss,omitempty"`
	Subject   string          `json:"sub,omitempty"`
	Audience  audience        `json:"aud,omitempty"`
	Expiry    json.Number     `json:"exp,omitempty"`
	NotBefore json.Number     `json:"nbf,omitempty"`
	IssuedAt  json.Number     `json:"iat,omitempty"`
	JWTID     string          `json:"jti,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// audience accepts both a single string and an array of strings per RFC 7519.
type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = audience{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*a = audience(many)
	return nil
}

type contextKey struct{}

// ClaimsFromContext returns the validated claims stored by Middleware.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(contextKey{}).(*Claims)
	return c, ok
}

func contextWithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, contextKey{}, claims)
}

// Verify validates a compact-serialized JWT. The signature algorithm is chosen
// from the JWK key type, never trusted from the token header alone.
func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	header, signingInput, signature, err := parseCompact(token)
	if err != nil {
		return nil, err
	}
	if header.Alg == "" || strings.EqualFold(header.Alg, "none") {
		return nil, ErrAlgNone
	}
	if !v.algAllowed(header.Alg) {
		return nil, ErrUnsupportedAlg
	}

	entry, err := v.resolveKey(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	if entry.alg != "" && entry.alg != header.Alg {
		return nil, fmt.Errorf("%w: jwk pins alg %q", ErrUnsupportedAlg, entry.alg)
	}
	if !algMatchesKey(entry.public, header.Alg) {
		// The key type decides the algorithm family, so an RSA key can never
		// verify an HS256 (or any other mismatched) token.
		return nil, fmt.Errorf("%w: alg %q incompatible with key type", ErrUnsupportedAlg, header.Alg)
	}
	if err := verifySignature(entry.public, header.Alg, signingInput, signature); err != nil {
		return nil, err
	}

	claims, err := parseClaims(signingInput)
	if err != nil {
		return nil, err
	}
	if err := v.validateClaims(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// resolveKey looks the kid up in the cache, refreshing JWKS once (coalesced
// across concurrent requests) when the kid is missing or the cache is stale.
func (v *Verifier) resolveKey(ctx context.Context, kid string) (*keyEntry, error) {
	if entry, err := v.lookup(kid); err == nil {
		return entry, nil
	}
	if kid == "" {
		return nil, ErrUnknownKid
	}

	refreshErr := v.refreshCoalesced(ctx, true)

	entry, lookupErr := v.lookup(kid)
	if lookupErr == nil {
		return entry, nil
	}

	// Preserve the infrastructure error when the document itself is unusable.
	if errors.Is(lookupErr, ErrCacheExpired) || errors.Is(lookupErr, ErrJWKSUnavailable) {
		if refreshErr != nil && !errors.Is(refreshErr, errRefreshRateLimited) {
			return nil, fmt.Errorf("%w: %v", ErrCacheExpired, refreshErr)
		}
		return nil, ErrCacheExpired
	}
	if errors.Is(refreshErr, errRefreshRateLimited) {
		return nil, ErrRefreshThrottled
	}
	return nil, ErrUnknownKid
}

func parseCompact(token string) (*Header, []byte, []byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return nil, nil, nil, ErrMalformedToken
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	var header Header
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: header: %v", ErrMalformedToken, err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: signature: %v", ErrMalformedToken, err)
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	return &header, signingInput, signature, nil
}

func parseClaims(signingInput []byte) (*Claims, error) {
	dot := strings.IndexByte(string(signingInput), '.')
	body, err := base64.RawURLEncoding.DecodeString(string(signingInput[dot+1:]))
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformedToken, err)
	}
	var claims Claims
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformedToken, err)
	}
	claims.Raw = body
	return &claims, nil
}

func (v *Verifier) validateClaims(c *Claims) error {
	now := v.cfg.now()
	leeway := v.cfg.Leeway

	if c.Expiry.String() != "" {
		expiryTime, err := unixTime(c.Expiry)
		if err != nil {
			return fmt.Errorf("%w: invalid exp", ErrInvalidClaims)
		}
		if now.After(expiryTime.Add(leeway)) {
			return fmt.Errorf("%w: token expired", ErrInvalidClaims)
		}
	}
	if c.NotBefore.String() != "" {
		notBefore, err := unixTime(c.NotBefore)
		if err != nil {
			return fmt.Errorf("%w: invalid nbf", ErrInvalidClaims)
		}
		if now.Add(leeway).Before(notBefore) {
			return fmt.Errorf("%w: token not yet valid", ErrInvalidClaims)
		}
	}
	if v.cfg.Issuer != "" && c.Issuer != v.cfg.Issuer {
		return fmt.Errorf("%w: unexpected issuer", ErrInvalidClaims)
	}
	if v.cfg.Audience != "" {
		matched := false
		for _, aud := range c.Audience {
			if aud == v.cfg.Audience {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w: audience mismatch", ErrInvalidClaims)
		}
	}
	return nil
}

func unixTime(n json.Number) (time.Time, error) {
	seconds, err := n.Float64()
	if err != nil {
		return time.Time{}, err
	}
	whole := int64(seconds)
	nanos := int64((seconds - float64(whole)) * float64(time.Second))
	return time.Unix(whole, nanos), nil
}

func (v *Verifier) algAllowed(alg string) bool {
	if !knownAsymmetricAlg(alg) {
		return false
	}
	if len(v.allowedAlgs) == 0 {
		return true
	}
	return v.allowedAlgs[alg]
}

func knownAsymmetricAlg(alg string) bool {
	switch alg {
	case "RS256", "RS384", "RS512",
		"PS256", "PS384", "PS512",
		"ES256", "ES384", "ES512",
		"EdDSA":
		return true
	}
	return false
}

// rsaAlgSpec maps an RSA alg to its hash and PSS flag.
func rsaAlgSpec(alg string) (crypto.Hash, bool, bool) {
	switch alg {
	case "RS256", "PS256":
		return crypto.SHA256, alg == "PS256", true
	case "RS384", "PS384":
		return crypto.SHA384, alg == "PS384", true
	case "RS512", "PS512":
		return crypto.SHA512, alg == "PS512", true
	}
	return 0, false, false
}

func ecdsaHash(alg string, key *ecdsa.PublicKey) (crypto.Hash, error) {
	var hash crypto.Hash
	switch alg {
	case "ES256":
		hash = crypto.SHA256
	case "ES384":
		hash = crypto.SHA384
	case "ES512":
		hash = crypto.SHA512
	default:
		return 0, fmt.Errorf("%w: alg %s cannot use ec key", ErrUnsupportedAlg, alg)
	}
	expectedByCurve := map[crypto.Hash]int{
		crypto.SHA256: 256,
		crypto.SHA384: 384,
		crypto.SHA512: 521,
	}
	if expectedByCurve[hash] != key.Curve.Params().BitSize {
		return 0, fmt.Errorf("%w: alg %s does not match curve size %d", ErrUnsupportedAlg, alg, key.Curve.Params().BitSize)
	}
	return hash, nil
}

func hashInput(hash crypto.Hash, input []byte) []byte {
	h := hash.New()
	h.Write(input)
	return h.Sum(nil)
}

func verifySignature(pub any, alg string, signingInput, signature []byte) error {
	switch key := pub.(type) {
	case *rsa.PublicKey:
		hasher, pss, ok := rsaAlgSpec(alg)
		if !ok {
			return fmt.Errorf("%w: alg %s cannot use rsa key", ErrUnsupportedAlg, alg)
		}
		digest := hashInput(hasher, signingInput)
		if pss {
			if err := rsa.VerifyPSS(key, hasher, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}); err != nil {
				return ErrBadSignature
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(key, hasher, digest, signature); err != nil {
			return ErrBadSignature
		}
		return nil

	case *ecdsa.PublicKey:
		hash, err := ecdsaHash(alg, key)
		if err != nil {
			return err
		}
		if !ecdsa.VerifyASN1(key, hashInput(hash, signingInput), signature) {
			return ErrBadSignature
		}
		return nil

	case ed25519.PublicKey:
		if alg != "EdDSA" {
			return fmt.Errorf("%w: alg %s cannot use ed25519 key", ErrUnsupportedAlg, alg)
		}
		if !ed25519.Verify(key, signingInput, signature) {
			return ErrBadSignature
		}
		return nil

	default:
		// No *rsa.PublicKey (or other asymmetric key) can ever be interpreted
		// as an HMAC secret, which closes the algorithm-confusion path.
		return ErrUnsupportedAlg
	}
}
