package credentialhost

import (
	"context"
	"sync"
	"time"
)

// Principal is the requester identity established by the host's authentication
// middleware. It must never be populated from an AssumeRequest body.
//
// TenantIDs lists the tenants that this principal is allowed to act for. Both
// the principal and the tenant claim are bound into authorization decisions.
type Principal struct {
	Subject   string
	TenantIDs []string
}

type principalContextKey struct{}

// WithPrincipal attaches a host-authenticated principal to ctx.
//
// This function is intended for authentication middleware. Credential host
// request parsing code must not call it with untrusted request fields.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, principalContextKey{}, clonePrincipal(principal))
}

// PrincipalFromContext returns the host-authenticated principal.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok {
		return Principal{}, false
	}
	return clonePrincipal(principal), true
}

func clonePrincipal(principal Principal) Principal {
	principal.TenantIDs = append([]string(nil), principal.TenantIDs...)
	return principal
}

func (p Principal) ownsTenant(tenantID string) bool {
	for _, candidate := range p.TenantIDs {
		if candidate == tenantID {
			return true
		}
	}
	return false
}

// TenantConfig is the security-relevant tenant registration supplied by the
// platform. The host generates the 128-bit ExternalID; callers cannot choose or
// replace it directly.
type TenantConfig struct {
	TenantID          string
	RoleID            string
	SourceResources   []string
	MaxSessionSeconds int64
	PolicyDigest      string
}

// TenantRecord is an immutable snapshot of a registered tenant generation.
// SourceResources is sorted and copied for deterministic comparison.
type TenantRecord struct {
	TenantID          string
	RoleID            string
	ExternalID        string
	SourceResources   []string
	MaxSessionSeconds int64
	PolicyDigest      string
	Version           uint64
	Revoked           bool
}

// AllowsSourceResource reports whether resource is in this snapshot's set.
func (r TenantRecord) AllowsSourceResource(resource string) bool {
	for _, candidate := range r.SourceResources {
		if candidate == resource {
			return true
		}
	}
	return false
}

// AssumeRequest is the untrusted request body. Requester identity is
// deliberately absent; it comes from PrincipalFromContext.
//
// The canonical authorization decision digest covers six request values
// (TenantID, RoleID, ExternalID, SourceResource, DurationSeconds,
// ParametersDigest) plus the authenticated principal subject. RequestID is an
// idempotency key, and SessionName is audit-only, so neither is in that digest.
type AssumeRequest struct {
	RequestID        string
	TenantID         string
	RoleID           string
	ExternalID       string
	SourceResource   string
	SessionName      string
	DurationSeconds  int64
	ParametersDigest string
}

// Decision is the normalized authorization result. Digest covers the six
// request values plus Principal, while the remaining fields bind the decision
// to a tenant generation and to the exact values the STS callback must return.
type Decision struct {
	Principal         string
	TenantID          string
	RoleID            string
	ExternalID        string
	SourceResource    string
	DurationSeconds   int64
	ParametersDigest  string
	Digest            string
	Audience          string
	SourceIdentity    string
	SessionID         string
	NotAfter          time.Time
	PolicyDigest      string
	TenantVersion     uint64
	ResourceVersion   uint64
	RequestID         string
	SessionName       string
	CanonicalDecision []byte
}

// STSRequest is passed to the external STS callback. SessionName is provided
// for audit and may be used by STS for its own audit event, but authorization
// has already been decided without it.
type STSRequest struct {
	RequestID   string
	Decision    Decision
	SessionName string
}

// STS is the external credential service callback.
type STS interface {
	AssumeRole(context.Context, STSRequest) (*Credentials, error)
}

// STSClient is an alias kept for callers that prefer the Client suffix.
type STSClient = STS

// STSFunc adapts a function to STS.
type STSFunc func(context.Context, STSRequest) (*Credentials, error)

// AssumeRole implements STS.
func (f STSFunc) AssumeRole(ctx context.Context, request STSRequest) (*Credentials, error) {
	return f(ctx, request)
}

// Credentials is the metadata returned by the external STS callback. A real
// implementation may keep secret material behind this value; DestroyFunc must
// revoke or zeroize that material.
type Credentials struct {
	Audience       string
	RoleID         string
	SourceIdentity string
	ExpiresAt      time.Time
	SessionID      string

	// The host fills the following audit fields after a credential matches its
	// authorization decision. They are overwritten by Host.Assume.
	DecisionDigest  string
	TenantID        string
	TenantVersion   uint64
	ResourceVersion uint64
	RequestID       string

	// DestroyFunc is called at most once by Destroy. It may be nil.
	DestroyFunc func()

	destroyOnce sync.Once
}

// Credential is an alias for Credentials.
type Credential = Credentials

// Destroy destroys the credential at most once and clears its metadata.
func (c *Credentials) Destroy() {
	if c == nil {
		return
	}
	c.destroyOnce.Do(func() {
		if c.DestroyFunc != nil {
			c.DestroyFunc()
		}
		c.DestroyFunc = nil
		c.Audience = ""
		c.RoleID = ""
		c.SourceIdentity = ""
		c.ExpiresAt = time.Time{}
		c.SessionID = ""
		c.DecisionDigest = ""
		c.TenantID = ""
		c.TenantVersion = 0
		c.ResourceVersion = 0
		c.RequestID = ""
	})
}

// Option configures a Host.
type Option func(*Host)

// WithClock replaces the host clock. It is primarily useful for deterministic
// expiry and late-callback tests.
func WithClock(now func() time.Time) Option {
	return func(host *Host) {
		if now != nil {
			host.now = now
		}
	}
}
