package dpop

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Claims is the verified DPoP proof material made available to downstream
// handlers via the request context.
type Claims struct {
	// JKT is the RFC 7638 thumbprint of the proof public key.
	JKT string
	// Key is the verified embedded public JWK.
	Key *JWK
	// JTI is the unique proof identifier.
	JTI string
	// HTM and HTU echo the validated method and canonical target URL.
	HTM string
	HTU string
	// ATH is the validated access token hash.
	ATH string
}

type contextKey struct{}

// FromContext returns the verified DPoP claims stored on ctx, or nil.
func FromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(contextKey{}).(*Claims)
	return claims
}

// TokenClaims represents the access token data relevant to DPoP binding.
type TokenClaims struct {
	// CnfJKT is the confirmation-method JWK thumbprint the token is bound to.
	// An empty value means the token carries no DPoP key binding.
	CnfJKT string
}

// TokenInspector extracts (and normally also cryptographically validates) the
// binding-relevant claims from an access token. The default inspector parses
// a JWT payload without signature verification; production hosts should wrap
// it with their own JWT validation or token introspection.
type TokenInspector func(accessToken string) (*TokenClaims, error)

// Options configures a Verifier.
type Options struct {
	// NonceManager enables DPoP-Nonce issuance and verification. When nil the
	// nonce claim is not required.
	NonceManager *NonceManager
	// ReplayCache stores jti values. Defaults to a ten-minute in-memory cache.
	ReplayCache ReplayCache
	// Inspector validates access tokens. Defaults to ParseJWTTokenClaims.
	Inspector TokenInspector
	// HTUFunc returns the canonical target URL for a request. Defaults to
	// DefaultHTU; use BaseURLHTU behind a reverse proxy.
	HTUFunc func(r *http.Request) (string, error)
	// ClockSkew is the permitted iat drift in both directions. Default 1m.
	ClockSkew time.Duration
	// ProofTTL is the maximum accepted age of a proof. Default 5m.
	ProofTTL time.Duration
	// ReplayWindow bounds jti retention for anti-replay. Default 10m.
	ReplayWindow time.Duration
	// AllowUnboundTokens permits access tokens that lack a cnf.jkt member. It
	// defaults to false: a DPoP-enforcing gateway should hard-require the key
	// binding so that stolen tokens cannot be replayed with an attacker key.
	AllowUnboundTokens bool
	// SuppressNonceEmit disables the rotated DPoP-Nonce header that is
	// otherwise added to successful responses when a NonceManager is set.
	SuppressNonceEmit bool
	// Now overrides the clock, primarily for tests.
	Now func() time.Time
}

// Verifier performs RFC 9449 DPoP proof verification.
type Verifier struct {
	opts Options
}

// NewVerifier constructs a Verifier and normalizes option defaults.
func NewVerifier(opts Options) (*Verifier, error) {
	if opts.ClockSkew <= 0 {
		opts.ClockSkew = time.Minute
	}
	if opts.ProofTTL <= 0 {
		opts.ProofTTL = 5 * time.Minute
	}
	if opts.ReplayWindow <= 0 {
		opts.ReplayWindow = 10 * time.Minute
	}
	if opts.ReplayCache == nil {
		opts.ReplayCache = NewMemoryReplayCache(opts.ReplayWindow)
	}
	if clocked, ok := opts.ReplayCache.(interface{ withClock(func() time.Time) }); ok {
		clocked.withClock(opts.Now)
	}
	if opts.Inspector == nil {
		opts.Inspector = ParseJWTTokenClaims
	}
	if opts.HTUFunc == nil {
		opts.HTUFunc = DefaultHTU
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NonceManager != nil {
		opts.NonceManager.withClock(opts.Now)
	}
	return &Verifier{opts: opts}, nil
}

// Middleware protects next by requiring a valid DPoP-bound access token.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := v.VerifyRequest(r)
		if err != nil {
			v.writeError(w, err)
			return
		}
		if v.opts.NonceManager != nil && !v.opts.SuppressNonceEmit {
			w.Header().Set("DPoP-Nonce", v.opts.NonceManager.Issue())
		}
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, claims))
		next.ServeHTTP(w, r)
	})
}

func (v *Verifier) writeError(w http.ResponseWriter, err error) {
	dpopErr := AsError(err)
	if dpopErr == nil {
		dpopErr = newError(ErrInvalidDPoP, err.Error())
	}

	header := `DPoP realm="dpop", error="` + dpopErr.Code + `"`
	if dpopErr.Description != "" {
		header += `, error_description="` + quoteHeaderValue(dpopErr.Description) + `"`
	}
	if dpopErr.Nonce != "" {
		w.Header().Set("DPoP-Nonce", dpopErr.Nonce)
	}
	w.Header().Set("WWW-Authenticate", header)

	status := dpopErr.Status
	if status == 0 {
		status = http.StatusUnauthorized
	}
	w.WriteHeader(status)
}

func quoteHeaderValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(value)
}

// VerifyRequest runs the complete RFC 9449 verification pipeline for one
// protected request and returns the validated proof claims.
func (v *Verifier) VerifyRequest(r *http.Request) (*Claims, error) {
	scheme, token, err := parseAuthorization(r)
	if err != nil {
		return nil, err
	}

	tokenClaims, err := v.opts.Inspector(token)
	if err != nil {
		return nil, newError(ErrInvalidToken, "access token validation failed")
	}
	if tokenClaims == nil {
		tokenClaims = &TokenClaims{}
	}

	// A token bound to a key can never be accepted as a plain Bearer token:
	// that would defeat the proof-of-possession binding.
	if strings.EqualFold(scheme, "Bearer") {
		if tokenClaims.CnfJKT != "" {
			return nil, newError(ErrInvalidToken, "sender-constrained access token must not be presented with the Bearer scheme")
		}
		return nil, newError(ErrInvalidToken, "DPoP-bound access token required")
	}

	proofCompact, err := parseDPoPHeader(r)
	if err != nil {
		return nil, err
	}

	if !v.opts.AllowUnboundTokens && tokenClaims.CnfJKT == "" {
		return nil, newError(ErrInvalidDPoPKeyBind, "access token is missing a cnf.jkt key binding")
	}

	proof, err := parseProof(proofCompact)
	if err != nil {
		return nil, err
	}
	if err := proof.verifySignature(); err != nil {
		return nil, err
	}

	jkt, err := proof.header.Key.Thumbprint()
	if err != nil {
		return nil, err
	}
	if tokenClaims.CnfJKT != "" && subtle.ConstantTimeCompare([]byte(jkt), []byte(tokenClaims.CnfJKT)) != 1 {
		return nil, newError(ErrInvalidDPoPKeyBind, "DPoP proof JWK thumbprint does not match the access token cnf.jkt")
	}

	if err := v.verifyClaims(r, proof, token, jkt); err != nil {
		return nil, err
	}

	return &Claims{
		JKT: jkt,
		Key: proof.header.Key,
		JTI: proof.claims.JTI,
		HTM: proof.claims.HTM,
		HTU: proof.claims.HTU,
		ATH: proof.claims.ATH,
	}, nil
}

func (v *Verifier) verifyClaims(r *http.Request, proof *parsedProof, accessToken, jkt string) error {
	claims := proof.claims

	if claims.HTM == "" || subtle.ConstantTimeCompare([]byte(claims.HTM), []byte(r.Method)) != 1 {
		return newError(ErrInvalidDPoP, "htm claim does not match the HTTP method")
	}

	expectedHTU, err := v.opts.HTUFunc(r)
	if err != nil {
		return newError(ErrInvalidDPoP, "cannot determine canonical htu: "+err.Error())
	}
	if err := compareHTU(claims.HTU, expectedHTU); err != nil {
		return err
	}

	expectedATH := tokenHash(accessToken)
	if claims.ATH == "" || subtle.ConstantTimeCompare([]byte(claims.ATH), []byte(expectedATH)) != 1 {
		return newError(ErrInvalidDPoP, "ath claim does not match the access token hash")
	}

	if claims.IAT == "" {
		return newError(ErrInvalidDPoP, "DPoP proof is missing the iat claim")
	}
	issuedAtUnix, err := strconv.ParseInt(claims.IAT.String(), 10, 64)
	if err != nil {
		return newError(ErrInvalidDPoP, "DPoP proof iat claim must be a numeric date")
	}
	now := v.opts.Now()
	issuedAt := time.Unix(issuedAtUnix, 0)
	earliest := now.Add(-(v.opts.ProofTTL + v.opts.ClockSkew))
	latest := now.Add(v.opts.ClockSkew)
	if issuedAt.Before(earliest) || issuedAt.After(latest) {
		return newError(ErrInvalidDPoP, "DPoP proof iat is outside the accepted time window")
	}

	if claims.JTI == "" {
		return newError(ErrInvalidDPoP, "DPoP proof is missing the jti claim")
	}

	if v.opts.NonceManager != nil {
		if !v.opts.NonceManager.Verify(claims.Nonce) {
			return nonceError("DPoP-Nonce missing, expired, or invalid", v.opts.NonceManager.Issue())
		}
	}

	// Replay check comes last so that a malformed or unauthorized proof never
	// pollutes the anti-replay cache.
	expiresAt := now.Add(v.opts.ReplayWindow)
	if v.opts.ReplayCache.SeenBefore(claims.JTI, expiresAt) {
		return newError(ErrInvalidDPoP, "DPoP proof jti has already been used")
	}
	return nil
}
