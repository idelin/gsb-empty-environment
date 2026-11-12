package netsec

import "errors"

// ErrInvalidIdentity is returned when an identity is incomplete.
var ErrInvalidIdentity = errors.New("netsec: identity must carry tenant and service id")

// Identity is a service principal scoped to a tenant. Two principals are only
// equal when both the tenant and the service id match: identically named
// services in different tenants are distinct principals and therefore do not
// form a delegation cycle.
type Identity struct {
	TenantID  string
	ServiceID string
}

// Validate rejects zero-value or partially populated identities.
func (id Identity) Validate() error {
	if id.TenantID == "" || id.ServiceID == "" {
		return ErrInvalidIdentity
	}
	return nil
}

// Equal reports whether two identities denote the same principal.
func (id Identity) Equal(other Identity) bool {
	return id.TenantID == other.TenantID && id.ServiceID == other.ServiceID
}

func (id Identity) String() string {
	return id.TenantID + "/" + id.ServiceID
}
