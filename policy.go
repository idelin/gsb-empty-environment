package netsec

import (
	"fmt"
	"time"
)

// Operation describes an ACL-protected operation type in the orchestrator.
type Operation string

const (
	OpServiceRegister   Operation = "service:register"
	OpServiceDeregister Operation = "service:deregister"
	OpServiceRead       Operation = "service:read"
	OpKVWrite           Operation = "kv:write"
	OpPolicyWrite       Operation = "policy:write"
)

// WildcardIdentity matches any service identity within the policy's
// namespace. Namespaces are never wildcarded: cross-namespace intents
// are always denied.
const WildcardIdentity = "*"

// Policy binds a namespace, a service identity and the allowed operations.
// A token may bind several policies; an intent passes when at least one
// bound policy matches it on every dimension.
type Policy struct {
	ID              string
	Namespace       string
	ServiceIdentity string
	Operations      []Operation
}

// matchesIdentity reports whether the policy covers the service identity,
// honouring WildcardIdentity.
func (p Policy) matchesIdentity(serviceIdentity string) bool {
	return p.ServiceIdentity == WildcardIdentity || p.ServiceIdentity == serviceIdentity
}

// allows reports whether the policy covers the given namespace, service
// identity and operation.
func (p Policy) allows(namespace, serviceIdentity string, op Operation) bool {
	if p.Namespace != namespace {
		return false
	}
	if !p.matchesIdentity(serviceIdentity) {
		return false
	}
	for _, allowed := range p.Operations {
		if allowed == op {
			return true
		}
	}
	return false
}

// Token is an ACL token issued to a caller. It binds a set of policies
// and carries an expiry time.
type Token struct {
	ID        string
	PolicyIDs []string
	ExpiresAt time.Time
	Revoked   bool
}

// Intent is an operation request submitted by a caller. Every dimension
// must match a bound policy before execution.
type Intent struct {
	TokenID         string
	Namespace       string
	ServiceIdentity string
	Operation       Operation
}

// DenyReason explains why an intent was rejected.
type DenyReason string

const (
	DenyTokenUnknown   DenyReason = "token_unknown"
	DenyTokenExpired   DenyReason = "token_expired"
	DenyTokenRevoked   DenyReason = "token_revoked"
	DenyNoPolicy       DenyReason = "no_matching_policy"
	DenyNamespace      DenyReason = "namespace_mismatch"
	DenyIdentity       DenyReason = "identity_mismatch"
	DenyPrivilegeEscal DenyReason = "privilege_escalation"
)

// Decision is the authorization outcome. When Allowed is false, Reason
// carries the most specific mismatch found during evaluation.
type Decision struct {
	Allowed bool
	Reason  DenyReason
	Detail  string
}

func (d Decision) Error() string {
	if d.Allowed {
		return "allowed"
	}
	return fmt.Sprintf("denied(%s): %s", d.Reason, d.Detail)
}
