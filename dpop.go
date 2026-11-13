package netsec

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultMaxAge    = 5 * time.Minute
	defaultClockSkew = 30 * time.Second
)

// Result is returned after a proof has been verified and bound to the access
// token.
type Result struct {
	// JKT is the RFC 7638 thumbprint shared by the proof JWK and token cnf.jkt.
	JKT string
	// TokenClaims are the parsed (but not independently authenticated) access
	// token claims.
	TokenClaims map[string]any
	// ProofClaims are the verified DPoP proof claims.
	ProofClaims map[string]any
}

// ReplayCache stores proof identifiers so the same proof cannot be used twice.
// CheckAndStore must atomically return true when jti was already present.
type ReplayCache interface {
	CheckAndStore(jti string, expires time.Time) (bool, error)
}

// Option configures a Validator.
type Option func(*validatorConfig)

type validatorConfig struct {
	now        func() time.Time
	maxAge     time.Duration
	clockSkew  time.Duration
	requireATH bool
	replay     ReplayCache
}

// WithNow overrides the clock. It is primarily useful in tests.
func WithNow(now func() time.Time) Option {
	return func(c *validatorConfig) {
		if now != nil {
			c.now = now
		}
	}
}

// WithMaxAge sets how old a proof's iat may be. Values <= 0 leave the default
// of five minutes in place.
func WithMaxAge(maxAge time.Duration) Option {
	return func(c *validatorConfig) {
		if maxAge > 0 {
			c.maxAge = maxAge
		}
	}
}

// WithClockSkew allows for limited clock disagreement between the client and
// resource server.
func WithClockSkew(skew time.Duration) Option {
	return func(c *validatorConfig) {
		if skew >= 0 {
			c.clockSkew = skew
		}
	}
}

// WithRequireATH requires the proof to carry an ath claim binding it to the
// exact access token. RFC 9449 requires ath on resource requests; it is
// optional by default here for compatibility with existing clients.
func WithRequireATH(require bool) Option {
	return func(c *validatorConfig) {
		c.requireATH = require
	}
}

// WithReplayCache enables single-use proof enforcement. Without a cache the
// validator still checks signatures and bindings, but does not remember jti
// values.
func WithReplayCache(cache ReplayCache) Option {
	return func(c *validatorConfig) {
		c.replay = cache
	}
}

// Validator validates DPoP proofs and their binding to access tokens. A
// Validator is safe for concurrent use as long as the configured ReplayCache is
// safe for concurrent use.
type Validator struct {
	cfg validatorConfig
}

// NewValidator builds a Validator. The defaults are a five-minute proof
// lifetime, 30 seconds of clock skew, and no replay cache.
func NewValidator(opts ...Option) *Validator {
	cfg := validatorConfig{
		now:       time.Now,
		maxAge:    defaultMaxAge,
		clockSkew: defaultClockSkew,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &Validator{cfg: cfg}
}

// DefaultValidator is a Validator with the package defaults.
var DefaultValidator = NewValidator()

// Validate verifies a DPoP proof for an access token and the request it
// accompanies. It returns ErrTokenNotBound when the token has no cnf.jkt and
// ErrBindingMismatch when the proof key is not the key bound in cnf.jkt.
func (v *Validator) Validate(accessToken, proof, method, requestURI string) (*Result, error) {
	if v == nil {
		v = DefaultValidator
	}
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("%w: empty token", ErrInvalidToken)
	}
	if strings.TrimSpace(proof) == "" {
		return nil, ErrProofRequired
	}

	tokenClaims, err := ParseAccessTokenClaims(accessToken)
	if err != nil {
		return nil, err
	}
	tokenJKT, err := JKTFromClaims(tokenClaims)
	if err != nil {
		return nil, err
	}

	trimmedProof := strings.TrimSpace(proof)
	segments := strings.Split(trimmedProof, ".")
	if len(segments) != 3 {
		return nil, fmt.Errorf("%w: compact JWT must have three parts", ErrInvalidProof)
	}
	headerJSON, payloadJSON, signature, err := splitJWT(trimmedProof)
	if err != nil {
		return nil, err
	}
	signingInput := []byte(segments[0] + "." + segments[1])

	var header struct {
		Typ string          `json:"typ"`
		Alg string          `json:"alg"`
		JWK json.RawMessage `json:"jwk"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("%w: invalid header: %v", ErrInvalidProof, err)
	}
	if !strings.EqualFold(header.Typ, "dpop+jwt") {
		return nil, fmt.Errorf("%w: typ must be dpop+jwt", ErrInvalidProof)
	}
	if header.Alg == "" {
		return nil, fmt.Errorf("%w: missing alg", ErrInvalidProof)
	}
	if len(header.JWK) == 0 || string(header.JWK) == "null" {
		return nil, ErrMissingJWK
	}

	proofJWK, err := parseJWK(header.JWK)
	if err != nil {
		return nil, err
	}
	proofJKT, err := proofJWK.Thumbprint()
	if err != nil {
		return nil, err
	}
	if err := verifyProofSignature(proofJWK, header.Alg, signingInput, signature); err != nil {
		return nil, err
	}

	if proofJKT != tokenJKT {
		return nil, fmt.Errorf("%w: proof jkt %q, token jkt %q", ErrBindingMismatch, proofJKT, tokenJKT)
	}

	proofClaims, err := parseJSONObject(payloadJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid claim set: %v", ErrInvalidProof, err)
	}

	htm, ok, err := claimString(proofClaims, "htm")
	if err != nil {
		return nil, err
	}
	if !ok || htm == "" {
		return nil, fmt.Errorf("%w: missing htm", ErrInvalidProof)
	}
	if htm != method {
		return nil, fmt.Errorf("%w: htm %q, method %q", ErrMethodMismatch, htm, method)
	}

	htu, ok, err := claimString(proofClaims, "htu")
	if err != nil {
		return nil, err
	}
	if !ok || htu == "" {
		return nil, fmt.Errorf("%w: missing htu", ErrInvalidProof)
	}
	proofURI, err := canonicalHTU(htu)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid htu: %v", ErrInvalidProof, err)
	}
	request, err := canonicalHTU(requestURI)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid request URI: %v", ErrURIMismatch, err)
	}
	if proofURI != request {
		return nil, fmt.Errorf("%w: htu %q, request %q", ErrURIMismatch, htu, requestURI)
	}

	iat, ok, err := claimInt64(proofClaims, "iat")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: missing iat", ErrInvalidProof)
	}
	nowFn, maxAge, clockSkew := v.settings()
	now := nowFn()
	nowUnix := now.Unix()
	skewSeconds := int64(clockSkew / time.Second)
	if iat > nowUnix+skewSeconds || iat < nowUnix-int64(maxAge/time.Second)-skewSeconds {
		return nil, fmt.Errorf("%w: iat %d, now %d", ErrProofExpired, iat, nowUnix)
	}

	if exp, present, err := claimInt64(proofClaims, "exp"); err != nil {
		return nil, err
	} else if present && nowUnix > exp+skewSeconds {
		return nil, fmt.Errorf("%w: exp %d, now %d", ErrProofExpired, exp, nowUnix)
	}

	if err := v.checkATH(proofClaims, accessToken); err != nil {
		return nil, err
	}

	if v.cfg.replay != nil {
		jti, ok, err := claimString(proofClaims, "jti")
		if err != nil {
			return nil, err
		}
		if !ok || strings.TrimSpace(jti) == "" {
			return nil, fmt.Errorf("%w: missing jti for replay protection", ErrInvalidProof)
		}
		expires := time.Unix(iat, 0).Add(maxAge)
		if expClaim, present, err := claimInt64(proofClaims, "exp"); err != nil {
			return nil, err
		} else if present && time.Unix(expClaim, 0).Before(expires) {
			expires = time.Unix(expClaim, 0)
		}
		seen, err := v.cfg.replay.CheckAndStore(jti, expires)
		if err != nil {
			return nil, fmt.Errorf("dpop: replay cache: %w", err)
		}
		if seen {
			return nil, fmt.Errorf("%w: jti %q", ErrReplay, jti)
		}
	}

	return &Result{
		JKT:         tokenJKT,
		TokenClaims: tokenClaims,
		ProofClaims: proofClaims,
	}, nil
}

func (v *Validator) settings() (func() time.Time, time.Duration, time.Duration) {
	now := v.cfg.now
	if now == nil {
		now = time.Now
	}
	maxAge := v.cfg.maxAge
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}
	skew := v.cfg.clockSkew
	if skew < 0 {
		skew = 0
	}
	return now, maxAge, skew
}

func (v *Validator) checkATH(proofClaims map[string]any, accessToken string) error {
	ath, present, err := claimString(proofClaims, "ath")
	if err != nil {
		return err
	}
	if !present {
		if v.cfg.requireATH {
			return fmt.Errorf("%w: missing ath", ErrInvalidProof)
		}
		return nil
	}

	sum := sha256.Sum256([]byte(accessToken))
	expected := base64.RawURLEncoding.EncodeToString(sum[:])
	if ath != expected {
		return ErrAccessTokenHashMismatch
	}
	return nil
}

// ValidateRequest validates the Authorization and DPoP headers of an HTTP
// request. If accessToken is empty it is read from the Authorization header.
func (v *Validator) ValidateRequest(r *http.Request, accessToken string) (*Result, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil request", ErrInvalidProof)
	}
	proof := DPoPProofFromRequest(r)
	if proof == "" {
		return nil, ErrProofRequired
	}
	if accessToken == "" {
		accessToken = AccessTokenFromRequest(r)
	}
	return v.Validate(accessToken, proof, r.Method, RequestURI(r))
}

// ValidateDPoP is a convenience wrapper around DefaultValidator.Validate.
func ValidateDPoP(accessToken, proof, method, requestURI string, opts ...Option) (*Result, error) {
	return NewValidator(opts...).Validate(accessToken, proof, method, requestURI)
}

// Validate is an alias for ValidateDPoP.
func Validate(accessToken, proof, method, requestURI string, opts ...Option) (*Result, error) {
	return ValidateDPoP(accessToken, proof, method, requestURI, opts...)
}

// Verify validates a proof and discards the result. It is useful when only the
// allow/deny decision is needed.
func Verify(accessToken, proof, method, requestURI string, opts ...Option) error {
	_, err := ValidateDPoP(accessToken, proof, method, requestURI, opts...)
	return err
}

// ValidateRequest is a convenience wrapper that reads the DPoP proof and
// Authorization headers from r.
func ValidateRequest(r *http.Request, accessToken string, opts ...Option) (*Result, error) {
	return NewValidator(opts...).ValidateRequest(r, accessToken)
}

// VerifyRequest validates an HTTP request and discards the result.
func VerifyRequest(r *http.Request, accessToken string, opts ...Option) error {
	_, err := ValidateRequest(r, accessToken, opts...)
	return err
}

// ValidateHTTPRequest is an alias for ValidateRequest.
func ValidateHTTPRequest(r *http.Request, accessToken string, opts ...Option) (*Result, error) {
	return ValidateRequest(r, accessToken, opts...)
}

// DPoPProofFromRequest returns the proof from the standard DPoP header, with
// DPoP-Proof accepted as a compatibility fallback.
func DPoPProofFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if proof := strings.TrimSpace(r.Header.Get("DPoP")); proof != "" {
		return proof
	}
	return strings.TrimSpace(r.Header.Get("DPoP-Proof"))
}

// AccessTokenFromRequest extracts a DPoP (or Bearer) token from the
// Authorization header.
func AccessTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 {
		return ""
	}
	if !strings.EqualFold(fields[0], "DPoP") && !strings.EqualFold(fields[0], "Bearer") {
		return ""
	}
	return fields[1]
}

// RequestURI reconstructs the absolute request target without query or
// fragment, as required for the DPoP htu claim.
func RequestURI(r *http.Request) string {
	if r == nil {
		return ""
	}
	if r.URL != nil && r.URL.IsAbs() {
		return r.URL.String()
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	path := "/"
	rawPath := ""
	rawQuery := ""
	if r.URL != nil {
		if host == "" {
			host = r.URL.Host
		}
		if r.URL.Path != "" {
			path = r.URL.Path
		}
		rawPath = r.URL.RawPath
		rawQuery = r.URL.RawQuery
	} else if r.RequestURI != "" {
		if parsed, err := url.ParseRequestURI(r.RequestURI); err == nil {
			if parsed.Path != "" {
				path = parsed.Path
			}
			rawPath = parsed.RawPath
			rawQuery = parsed.RawQuery
		}
	}

	u := &url.URL{
		Scheme:   scheme,
		Host:     host,
		Path:     path,
		RawPath:  rawPath,
		RawQuery: rawQuery,
	}
	return u.String()
}

func canonicalHTU(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty URI")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if !u.IsAbs() || u.Host == "" || u.Opaque != "" {
		return "", fmt.Errorf("URI must be absolute")
	}
	if u.User != nil {
		return "", fmt.Errorf("URI must not contain userinfo")
	}

	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}

	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return scheme + "://" + host + path, nil
}
