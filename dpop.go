package dpopcheck

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DPoP-related error codes used in WWW-Authenticate challenges (RFC 9449
// sections 7.2 and 11).
const (
	ErrInvalidDPoP  = "invalid_dpop_proof"
	ErrInvalidToken = "invalid_token"
	ErrUseDPoPNonce = "use_dpop_nonce"
	DPoPNonceHeader = "DPoP-Nonce"
	dpopAuthScheme  = "DPoP"
	defaultWindow   = 60 * time.Second
	defaultNonceTTL = 10 * time.Minute
)

// proofClaims are the registered JWT claims carried by a DPoP proof.
type proofClaims struct {
	HTM   string   `json:"htm"`
	HTU   string   `json:"htu"`
	IAT   *float64 `json:"iat"`
	JTI   string   `json:"jti"`
	Nonce string   `json:"nonce,omitempty"`
	ATH   string   `json:"ath,omitempty"`
}

// ProofError describes a failed DPoP check and carries the error code that
// belongs in the WWW-Authenticate challenge.
type ProofError struct {
	Code string
	Msg  string
}

func (e *ProofError) Error() string {
	return "dpop: " + e.Code + ": " + e.Msg
}

func proofError(code, msg string) *ProofError {
	return &ProofError{Code: code, Msg: msg}
}

// ProofInfo is stored in the request context after a proof has been verified.
type ProofInfo struct {
	KeyThumbprint string
	JTI           string
	HTM           string
	HTU           string
}

type contextKey struct{}

// JKTResolver returns the jkt claim bound to the presented access token, or
// an empty string when the request carries no access token. A non-nil error
// rejects the request as invalid_token.
type JKTResolver func(r *http.Request) (string, error)

// Option customizes a Verifier.
type Option func(*Verifier)

// WithWindow sets the maximum absolute age of a proof's iat.
func WithWindow(window time.Duration) Option {
	return func(v *Verifier) { v.window = window }
}

// WithNonceManager installs the nonce manager used to require DPoP nonces.
func WithNonceManager(manager *NonceManager) Option {
	return func(v *Verifier) { v.nonces = manager }
}

// WithJKTResolver overrides access-token jkt extraction.
func WithJKTResolver(resolver JKTResolver) Option {
	return func(v *Verifier) { v.resolveJKT = resolver }
}

// WithURLNormalizer overrides canonical htu construction. The default keeps
// scheme/host/path, drops query and fragment and removes default ports.
func WithURLNormalizer(normalizer func(*url.URL) string) Option {
	return func(v *Verifier) { v.normalizeURL = normalizer }
}

// WithClock injects a clock, mainly useful in tests.
func WithClock(now func() time.Time) Option {
	return func(v *Verifier) { v.now = now }
}

// Verifier performs the DPoP proof checks described in RFC 9449.
type Verifier struct {
	window       time.Duration
	nonces       *NonceManager
	jtis         *jtiStore
	resolveJKT   JKTResolver
	normalizeURL func(*url.URL) string
	now          func() time.Time
}

// NewVerifier builds a Verifier with the default 60-second iat window and the
// default access-token jkt resolver.
func NewVerifier(options ...Option) *Verifier {
	verifier := &Verifier{
		window:       defaultWindow,
		resolveJKT:   DefaultJKTResolver,
		normalizeURL: normalizeHTU,
		now:          time.Now,
	}
	for _, option := range options {
		option(verifier)
	}
	verifier.jtis = newJTIStore(verifier.window, verifier.now)
	if verifier.nonces == nil {
		verifier.nonces = newNonceManager(defaultNonceTTL, verifier.now)
	}
	return verifier
}

// NonceManager exposes the manager so applications can trigger nonce issuance.
func (v *Verifier) NonceManager() *NonceManager {
	return v.nonces
}

// Middleware rejects requests whose DPoP proof fails any check. On failure it
// writes a 401 with a DPoP WWW-Authenticate challenge; when nonce-related, a
// fresh DPoP-Nonce is included.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, err := v.Authenticate(r)
		if err != nil {
			v.writeError(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), contextKey{}, info)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// FromContext returns the verified proof information, if any.
func FromContext(ctx context.Context) *ProofInfo {
	info, _ := ctx.Value(contextKey{}).(*ProofInfo)
	return info
}

// Authenticate runs every DPoP check against the request and returns the
// derived proof information on success.
func (v *Verifier) Authenticate(r *http.Request) (*ProofInfo, *ProofError) {
	token := strings.TrimSpace(r.Header.Get("DPoP"))
	if token == "" {
		return nil, proofError(ErrInvalidDPoP, "missing DPoP header")
	}

	proof, err := parseProof(token)
	if err != nil {
		return nil, proofError(ErrInvalidDPoP, err.Error())
	}
	if err := proof.verifyES256(); err != nil {
		return nil, proofError(ErrInvalidDPoP, err.Error())
	}

	claims := proof.payload
	if claims.IAT == nil {
		return nil, proofError(ErrInvalidDPoP, "proof missing iat")
	}
	issuedAt := time.Unix(int64(*claims.IAT), 0)
	if delta := v.now().Sub(issuedAt); delta > v.window || delta < -v.window {
		return nil, proofError(ErrInvalidDPoP, "proof iat outside allowed window")
	}
	if claims.JTI == "" {
		return nil, proofError(ErrInvalidDPoP, "proof missing jti")
	}

	if claims.HTM != r.Method {
		return nil, proofError(ErrInvalidDPoP, "htm does not match the request method")
	}
	expectedHTU := v.normalizeURL(requestURL(r))
	if claims.HTU == "" {
		return nil, proofError(ErrInvalidDPoP, "proof missing htu")
	}
	claimURL, parseErr := url.Parse(claims.HTU)
	if parseErr != nil || v.normalizeURL(claimURL) != expectedHTU {
		return nil, proofError(ErrInvalidDPoP, "htu does not match the canonical request URL")
	}

	if v.nonces.IsRequired() && !v.nonces.Validate(claims.Nonce) {
		return nil, proofError(ErrUseDPoPNonce, "missing or stale DPoP nonce")
	}

	if !v.jtis.useOnce(claims.JTI) {
		return nil, proofError(ErrInvalidDPoP, "proof jti has already been used in this window")
	}

	thumbprint, err := proof.header.JWK.thumbprint()
	if err != nil {
		return nil, proofError(ErrInvalidDPoP, err.Error())
	}

	accessToken, _ := bearerToken(r)
	if accessToken != "" {
		boundJKT, resolveErr := v.resolveJKT(r)
		if resolveErr != nil {
			return nil, proofError(ErrInvalidToken, resolveErr.Error())
		}
		if boundJKT == "" {
			return nil, proofError(ErrInvalidToken, "access token is not bound to a key jkt")
		}
		if boundJKT != thumbprint {
			return nil, proofError(ErrInvalidToken, "token jkt does not match the proof key thumbprint")
		}
		if claims.ATH != "" && claims.ATH != hashAccessToken(accessToken) {
			return nil, proofError(ErrInvalidDPoP, "ath does not match the access token")
		}
	}

	return &ProofInfo{
		KeyThumbprint: thumbprint,
		JTI:           claims.JTI,
		HTM:           claims.HTM,
		HTU:           expectedHTU,
	}, nil
}

func (v *Verifier) writeError(w http.ResponseWriter, proofErr *ProofError) {
	challenge := fmt.Sprintf(`%s realm="dpop", error="%s", error_description="%s"`,
		dpopAuthScheme, proofErr.Code, escapeQuotes(proofErr.Msg))
	if proofErr.Code == ErrUseDPoPNonce {
		nonce, err := v.nonces.Issue()
		if err == nil {
			w.Header().Set(DPoPNonceHeader, nonce)
		}
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.WriteHeader(http.StatusUnauthorized)
}

func escapeQuotes(value string) string {
	return strings.ReplaceAll(value, `"`, `'`)
}

// requestURL reconstructs the absolute target URL of the request from the
// Host header and request target, assuming https outside plain-http test
// servers.
func requestURL(r *http.Request) *url.URL {
	target := *r.URL
	if target.Host == "" {
		target.Host = r.Host
	}
	if target.Scheme == "" {
		if r.TLS != nil || strings.ToLower(r.Header.Get("X-Forwarded-Proto")) == "https" {
			target.Scheme = "https"
		} else {
			target.Scheme = "http"
		}
	}
	return &target
}

func bearerToken(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], dpopAuthScheme) {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

// DefaultJKTResolver extracts the cnf.jkt member from a DPoP-scheme access
// token JWT without verifying the token signature; the token's own
// authentication middleware is expected to have validated it already.
func DefaultJKTResolver(r *http.Request) (string, error) {
	token, ok := bearerToken(r)
	if !ok {
		return "", nil
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("access token is not a compact JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("access token payload is not valid base64url")
	}
	var claims struct {
		Cnf struct {
			JKT string `json:"jkt"`
		} `json:"cnf"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("access token payload is not valid JSON")
	}
	return claims.Cnf.JKT, nil
}
