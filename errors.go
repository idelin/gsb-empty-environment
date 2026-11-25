package credentialhost

import "errors"

var (
	// ErrInvalidRequest means the assume request is incomplete or malformed.
	ErrInvalidRequest = errors.New("credentialhost: invalid assume request")
	// ErrInvalidTenantConfig means a tenant registration or rotation is invalid.
	ErrInvalidTenantConfig = errors.New("credentialhost: invalid tenant configuration")
	// ErrUnauthenticated means the host-authenticated principal is missing.
	ErrUnauthenticated = errors.New("credentialhost: missing authenticated principal")
	// ErrTenantNotFound means the tenant has never been registered.
	ErrTenantNotFound = errors.New("credentialhost: tenant not found")
	// ErrTenantExists means a tenant registration already exists.
	ErrTenantExists = errors.New("credentialhost: tenant already exists")
	// ErrTenantRevoked means the tenant has been revoked.
	ErrTenantRevoked = errors.New("credentialhost: tenant revoked")
	// ErrTenantClaimMismatch means the authenticated principal does not own the
	// requested tenant.
	ErrTenantClaimMismatch = errors.New("credentialhost: tenant is not owned by principal")
	// ErrRoleMismatch means roleID is not the tenant's exact registered role.
	ErrRoleMismatch = errors.New("credentialhost: role does not match tenant registration")
	// ErrExternalIDMismatch means the external ID does not match the tenant.
	ErrExternalIDMismatch = errors.New("credentialhost: external ID does not match tenant registration")
	// ErrSourceResourceNotAllowed means sourceResource is absent from the
	// tenant's allowed source-resource set.
	ErrSourceResourceNotAllowed = errors.New("credentialhost: source resource is not allowed")
	// ErrResourceOwnerMismatch means the resource is no longer owned by the
	// requested tenant.
	ErrResourceOwnerMismatch = errors.New("credentialhost: source resource owner does not match tenant")
	// ErrInvalidSessionName means sessionName is not safe printable ASCII or is
	// outside the 1..64 byte range.
	ErrInvalidSessionName = errors.New("credentialhost: invalid session name")
	// ErrInvalidDuration means the requested session duration is invalid.
	ErrInvalidDuration = errors.New("credentialhost: invalid session duration")
	// ErrRequestIDConflict means a request ID was reused with different
	// parameters or by a different principal.
	ErrRequestIDConflict = errors.New("credentialhost: request ID reused with different parameters")
	// ErrStaleRequest means the request belongs to a revoked or superseded
	// tenant generation, or the resource ownership changed.
	ErrStaleRequest = errors.New("credentialhost: request is stale")
	// ErrSTSFailure means the external STS callback failed.
	ErrSTSFailure = errors.New("credentialhost: external STS callback failed")
	// ErrCredentialMismatch means STS returned credentials that do not match the
	// authorization decision. The credentials are destroyed before this error
	// is returned.
	ErrCredentialMismatch = errors.New("credentialhost: STS credential does not match decision")
	// ErrCredentialExpired means STS returned an expired or already-expiring
	// credential. The credential is destroyed before this error is returned.
	ErrCredentialExpired = errors.New("credentialhost: STS credential is expired")
)
