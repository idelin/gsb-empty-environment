package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// ExternalIDSize is the number of random bytes in a generated external ID.
	ExternalIDSize = 16
	// MaxSessionNameBytes is the maximum accepted RoleSessionName length.
	MaxSessionNameBytes = 64

	maxTenantIDBytes       = 256
	maxPrincipalIDBytes    = 1024
	maxRoleIDBytes         = 2048
	maxExternalIDBytes     = 512
	maxResourceBytes       = 2048
	maxRequestIDBytes      = 256
	maxParametersDigest    = 8192
	maxPolicyDigestBytes   = 2048
	maxAudienceBytes       = 2048
	maxSessionSecondsLimit = int64(^uint64(0)>>1) / int64(time.Second)
)

// TenantRecord is the platform's registration for one customer tenant.
//
// ExternalID should be a high-entropy, tenant-specific value. Use
// GenerateExternalID to create a canonical 128-bit identifier.
type TenantRecord struct {
	TenantID string
	RoleID   string
	// ExternalID is compared in constant time. It is never accepted from the
	// tenant record at request time; only the registered value is trusted.
	ExternalID string
	// SourceResources is the exact set of resources from which this tenant may
	// request an assume. Ownership is checked separately against the registry.
	SourceResources []string
	// MaxSessionSeconds caps the credential lifetime for this tenant.
	MaxSessionSeconds int64
	// PolicyDigest summarises the policy/configuration that produced this
	// record. It is bound into every authorization decision.
	PolicyDigest string
	// Audience is optional. When empty it defaults to TenantID, which keeps
	// credentials unambiguously attributable to one tenant.
	Audience string
}

// AuthContext is the trusted output of the host's authentication layer. It
// must never be populated from the untrusted Assume request body.
type AuthContext struct {
	PrincipalID string
	TenantIDs   []string
}

// AllowsTenant reports whether the authenticated principal is delegated by the
// tenant. Tenant membership comes only from AuthContext.
func (a AuthContext) AllowsTenant(tenantID string) bool {
	if tenantID == "" {
		return false
	}
	for _, id := range a.TenantIDs {
		if id == tenantID {
			return true
		}
	}
	return false
}

// AssumeRequest is the untrusted request body. It intentionally has no
// principal field: the requester identity is supplied separately by the host.
// SessionName is audit-only and is never compared with tenant configuration.
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

// Decision is the canonical authorization decision. Digest is a SHA-256 over a
// length-prefixed canonical encoding of the authorization fields, the
// authenticated principal, the policy digest, and the configuration
// generation.
//
// SessionName is retained in the decision for audit, but is deliberately not
// part of Digest. It is never compared with a registered value and never
// influences allow/deny.
//
// A Decision contains registered tenant material such as ExternalID. It is
// internal audit data and must not be returned to the original requester or
// written to broad, untrusted logs.
type Decision struct {
	Digest  string
	Allowed bool
	// DenyCode is stable and suitable for audit/metrics. DenyReason is for
	// local diagnostics only and is not part of Digest.
	DenyCode   string
	DenyReason string

	RequestID string
	// PrincipalID is copied from the trusted AuthContext.
	PrincipalID string

	TenantID         string
	RoleID           string
	ExternalID       string
	SourceResource   string
	SessionName      string
	ParametersDigest string

	RequestedDurationSeconds int64
	EffectiveDurationSeconds int64

	PolicyDigest     string
	TenantGeneration uint64
	Audience         string
	SourceIdentity   string
	SessionID        string

	IssuedAt time.Time
	// NotAfter is the latest expiration the STS callback may return.
	NotAfter time.Time
}

// STSRequest contains only values that survived authorization. ExternalID is
// included because it is required to call STS, but it must not be returned to
// the original requester.
type STSRequest struct {
	TenantID         string
	RoleID           string
	ExternalID       string
	SourceResource   string
	SessionName      string
	DurationSeconds  int64
	Audience         string
	SourceIdentity   string
	SessionID        string
	ParametersDigest string
	PolicyDigest     string
	DecisionID       string
	NotAfter         time.Time
}

// STSCredential is the callback's response. Audience, RoleID, SourceIdentity,
// SessionID, and Expiration are all verified against the authorization
// decision. On a mismatch the broker calls Destroy before returning an error.
type STSCredential struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	Audience       string
	RoleID         string
	SourceIdentity string
	Expiration     time.Time
	SessionID      string
}

// Destroy overwrites secret material on a rejected credential. Go strings are
// immutable, so this is a best-effort scrub of the credential object; callers
// should still avoid copying secrets unnecessarily.
func (c *STSCredential) Destroy() {
	if c == nil {
		return
	}
	c.AccessKeyID = ""
	c.SecretAccessKey = ""
	c.SessionToken = ""
	c.Audience = ""
	c.RoleID = ""
	c.SourceIdentity = ""
	c.Expiration = time.Time{}
	c.SessionID = ""
}

// IssuedCredential is a credential that passed every decision check. It keeps
// the decision ID and tenant metadata so the host can trace every delivered
// credential to exactly one authorization decision.
type IssuedCredential struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	Audience       string
	RoleID         string
	SourceIdentity string
	Expiration     time.Time
	SessionID      string

	TenantID         string
	PrincipalID      string
	RequestID        string
	SessionName      string
	DecisionID       string
	TenantGeneration uint64
	PolicyDigest     string
}

// Destroy overwrites secret material on a returned credential.
func (c *IssuedCredential) Destroy() {
	if c == nil {
		return
	}
	c.AccessKeyID = ""
	c.SecretAccessKey = ""
	c.SessionToken = ""
	c.Audience = ""
	c.RoleID = ""
	c.SourceIdentity = ""
	c.Expiration = time.Time{}
	c.SessionID = ""
}

func (c *IssuedCredential) clone() *IssuedCredential {
	if c == nil {
		return nil
	}
	out := *c
	return &out
}

// STS is the external assume-role callback. The callback is invoked only after
// all tenant, role, external-ID, resource, ownership, session-name, and
// duration checks succeed.
type STS interface {
	AssumeRole(context.Context, STSRequest) (*STSCredential, error)
}

// STSFunc adapts a function to STS.
type STSFunc func(context.Context, STSRequest) (*STSCredential, error)

// AssumeRole implements STS.
func (f STSFunc) AssumeRole(ctx context.Context, req STSRequest) (*STSCredential, error) {
	return f(ctx, req)
}

// Clock supplies the broker's notion of time. Tests can inject a fake clock;
// production callers should use the default.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// GenerateExternalID returns a canonical 128-bit random external ID encoded as
// 32 lowercase hexadecimal characters.
func GenerateExternalID() (string, error) {
	var raw [ExternalIDSize]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func newSessionID() (string, error) {
	var raw [ExternalIDSize]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func validText(s string, max int) bool {
	if s == "" || len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == 0 || r == '\n' || r == '\r' || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validSessionName accepts 1..64 bytes of unambiguous printable ASCII. This
// deliberately excludes spaces, quotes, backslashes, and non-ASCII bytes so the
// value is safe in logs and in downstream role-session fields.
func validSessionName(s string) bool {
	if len(s) < 1 || len(s) > MaxSessionNameBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '+', c == '=', c == ',', c == '.', c == '@', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func addSeconds(t time.Time, seconds int64) (time.Time, bool) {
	if seconds <= 0 || seconds > maxSessionSecondsLimit {
		return time.Time{}, false
	}
	return t.Add(time.Duration(seconds) * time.Second), true
}
