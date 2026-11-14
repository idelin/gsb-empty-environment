package netsec

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Sentinel errors returned by Validate. Callers should match them with
// errors.Is; every failure wraps exactly one of these.
var (
	ErrMalformedToken   = errors.New("netsec: malformed token")
	ErrInvalidSignature = errors.New("netsec: invalid token signature")
	ErrIssuerMismatch   = errors.New("netsec: unexpected token issuer")
	ErrAudienceMismatch = errors.New("netsec: token audience does not match this service")
	ErrTokenExpired     = errors.New("netsec: token expired or not yet valid")
	ErrTokenRevoked     = errors.New("netsec: token revoked")
	ErrClusterMismatch  = errors.New("netsec: token belongs to a different cluster")
	ErrMirroredToken    = errors.New("netsec: mirrored tokens are not accepted")
	ErrLegacyToken      = errors.New("netsec: legacy token rejected by migration policy")
	ErrMissingIdentity  = errors.New("netsec: token lacks service account identity claims")
)

// LegacyPolicy decides how tokens in the pre-projected format (no audience
// claim) are handled. The zero value rejects them.
type LegacyPolicy int

const (
	// LegacyReject refuses every legacy-format token. This is the default
	// and the only safe choice once migration is complete.
	LegacyReject LegacyPolicy = iota
	// LegacyAllow accepts legacy tokens that pass signature, issuer, expiry
	// and revocation checks. Accepted tokens are flagged via
	// Identity.Legacy so authorization can treat them conservatively.
	LegacyAllow
)

// Config declares who this service is and which tokens it trusts. Every
// field that binds a token property (issuer, audience, cluster) must be set
// explicitly; the validator refuses to run with an incomplete trust anchor.
type Config struct {
	// Issuer is the exact iss value of the trusted token issuer.
	Issuer string
	// Audience is this service's own declared identity. A token is only
	// accepted if its audience equals exactly this value.
	Audience string
	// ClusterID, when non-empty, requires tokens to carry a matching
	// cluster claim, preventing cross-cluster token reuse.
	ClusterID string
	// Keys verifies token signatures. Required.
	Keys KeySet
	// Revocations is consulted for per-token and per-generation
	// revocation. Nil disables revocation checks.
	Revocations RevocationStore
	// LegacyPolicy selects the migration strategy for legacy-format
	// tokens. Defaults to LegacyReject.
	LegacyPolicy LegacyPolicy
	// LegacyNotAfter optionally bounds LegacyAllow: legacy tokens are only
	// accepted until this time, after which they are rejected.
	LegacyNotAfter time.Time
	// MaxTokenLifetime bounds how far into the future a token's expiry may
	// lie, enforcing the server-side expiry binding of projected tokens.
	// Zero disables the bound.
	MaxTokenLifetime time.Duration
	// Leeway tolerates clock skew for exp/nbf checks. Zero is exact.
	Leeway time.Duration
	// Clock supplies the current time; nil uses time.Now. Mainly for tests.
	Clock func() time.Time
}

// Validator verifies Kubernetes service account tokens for one service.
// Construct it once with NewValidator and share it across requests.
type Validator struct {
	cfg Config
	now func() time.Time
}

// NewValidator validates the configuration and returns a ready Validator.
func NewValidator(cfg Config) (*Validator, error) {
	if cfg.Issuer == "" {
		return nil, errors.New("netsec: Config.Issuer is required")
	}
	if cfg.Audience == "" {
		return nil, errors.New("netsec: Config.Audience is required")
	}
	if cfg.Keys == nil {
		return nil, errors.New("netsec: Config.Keys is required")
	}
	now := cfg.Clock
	if now == nil {
		now = time.Now
	}
	return &Validator{cfg: cfg, now: now}, nil
}

// Identity is the authenticated, authorization-relevant content of a valid
// token. Namespace, ServiceAccount and Generation are inputs to the
// caller's authorization decision.
type Identity struct {
	Issuer            string
	Subject           string
	Audience          []string
	Namespace         string
	ServiceAccount    string
	ServiceAccountUID string
	Cluster           string
	// Generation is the permission generation of the token; callers should
	// compare it against the generation their policy snapshot was built
	// from to avoid acting on stale grants.
	Generation int64
	TokenID    string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	// Legacy is true when the token used the pre-projected format and was
	// admitted under an explicit LegacyAllow migration policy.
	Legacy bool
}

// Validate verifies the signature, issuer, audience, temporal validity and
// revocation status of token, and returns its identity for authorization.
// The context is accepted for future store integrations; it is currently
// unused beyond cancellation checks.
func (v *Validator) Validate(ctx context.Context, token string) (*Identity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	claims, err := parseAndVerifyJWT(token, v.cfg.Keys)
	if err != nil {
		return nil, err
	}

	if claims.Issuer != v.cfg.Issuer {
		return nil, fmt.Errorf("%w: got %q, want %q", ErrIssuerMismatch, claims.Issuer, v.cfg.Issuer)
	}

	// Mirrored tokens are replicas issued for another cluster's cache and
	// must never be replayed against this service.
	if claims.Mirrored {
		return nil, fmt.Errorf("%w: token %q is a mirror replica", ErrMirroredToken, claims.TokenID)
	}

	// A configured ClusterID pins tokens to this cluster; tokens from
	// sibling clusters (or with no cluster binding at all) are rejected.
	if v.cfg.ClusterID != "" && claims.Cluster != v.cfg.ClusterID {
		return nil, fmt.Errorf("%w: got %q, want %q", ErrClusterMismatch, claims.Cluster, v.cfg.ClusterID)
	}

	legacy := claims.Audience == nil
	if legacy {
		if err := v.checkLegacyPolicy(); err != nil {
			return nil, err
		}
	} else if err := v.checkAudience(*claims.Audience); err != nil {
		return nil, err
	}

	if err := v.checkTemporal(claims, legacy); err != nil {
		return nil, err
	}

	if claims.Kubernetes == nil || claims.Kubernetes.Namespace == "" || claims.Kubernetes.ServiceAccount.Name == "" {
		return nil, fmt.Errorf("%w: namespace and serviceaccount claims are required", ErrMissingIdentity)
	}

	if err := v.checkRevocation(claims); err != nil {
		return nil, err
	}

	identity := &Identity{
		Issuer:            claims.Issuer,
		Subject:           claims.Subject,
		Namespace:         claims.Kubernetes.Namespace,
		ServiceAccount:    claims.Kubernetes.ServiceAccount.Name,
		ServiceAccountUID: claims.Kubernetes.ServiceAccount.UID,
		Cluster:           claims.Cluster,
		Generation:        claims.Generation,
		TokenID:           claims.TokenID,
		IssuedAt:          time.Unix(claims.IssuedAt, 0),
		ExpiresAt:         time.Unix(claims.ExpiresAt, 0),
		Legacy:            legacy,
	}
	if claims.Audience != nil {
		identity.Audience = append([]string(nil), (*claims.Audience)...)
	}
	return identity, nil
}

// checkAudience enforces that the token's audience is exactly this
// service's declared identity: a token minted for the monitoring service
// (or for several services at once) is not a credential for this one.
func (v *Validator) checkAudience(audience []string) error {
	if len(audience) != 1 || audience[0] != v.cfg.Audience {
		return fmt.Errorf("%w: got %q, want exactly %q", ErrAudienceMismatch, audience, v.cfg.Audience)
	}
	return nil
}

// checkLegacyPolicy applies the explicit migration strategy to tokens in
// the legacy (audience-less) format.
func (v *Validator) checkLegacyPolicy() error {
	switch v.cfg.LegacyPolicy {
	case LegacyAllow:
		if !v.cfg.LegacyNotAfter.IsZero() && !v.now().Before(v.cfg.LegacyNotAfter) {
			return fmt.Errorf("%w: legacy acceptance window ended at %s", ErrLegacyToken, v.cfg.LegacyNotAfter)
		}
		return nil
	default:
		return fmt.Errorf("%w: no audience claim and policy is LegacyReject", ErrLegacyToken)
	}
}

// checkTemporal enforces expiry, not-before and the server-side lifetime
// bound that projected tokens derive their audience and expiry from.
func (v *Validator) checkTemporal(claims *tokenClaims, legacy bool) error {
	now := v.now()
	leeway := v.cfg.Leeway

	if claims.ExpiresAt == 0 {
		if !legacy {
			return fmt.Errorf("%w: projected token has no expiry", ErrMalformedToken)
		}
	} else if !now.Before(time.Unix(claims.ExpiresAt, 0).Add(leeway)) {
		return fmt.Errorf("%w: expired at %s", ErrTokenExpired, time.Unix(claims.ExpiresAt, 0))
	}

	if claims.NotBefore != 0 && now.Add(leeway).Before(time.Unix(claims.NotBefore, 0)) {
		return fmt.Errorf("%w: not valid before %s", ErrTokenExpired, time.Unix(claims.NotBefore, 0))
	}

	if v.cfg.MaxTokenLifetime > 0 && claims.ExpiresAt != 0 {
		if time.Unix(claims.ExpiresAt, 0).Sub(now) > v.cfg.MaxTokenLifetime+leeway {
			return fmt.Errorf("%w: expiry exceeds server-bound lifetime %s", ErrTokenExpired, v.cfg.MaxTokenLifetime)
		}
	}
	return nil
}

// checkRevocation rejects individually revoked tokens and tokens whose
// permission generation has been superseded for their service account.
func (v *Validator) checkRevocation(claims *tokenClaims) error {
	store := v.cfg.Revocations
	if store == nil {
		return nil
	}
	if claims.TokenID != "" && store.IsRevoked(claims.TokenID) {
		return fmt.Errorf("%w: token id %q", ErrTokenRevoked, claims.TokenID)
	}
	ns := claims.Kubernetes.Namespace
	sa := claims.Kubernetes.ServiceAccount.Name
	if min := store.MinGeneration(ns, sa); claims.Generation < min {
		return fmt.Errorf("%w: generation %d superseded by %d for %s/%s",
			ErrTokenRevoked, claims.Generation, min, ns, sa)
	}
	return nil
}
