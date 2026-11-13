package netsec

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by the broker. Callers should usually map all
// policy denials to a generic response and keep the detailed reason in their
// own audit trail.
var (
	ErrUnauthenticated       = errors.New("netsec: requester is not authenticated")
	ErrInvalidRequest        = errors.New("netsec: invalid assume request")
	ErrTenantNotAllowed      = errors.New("netsec: tenant is not delegated to requester")
	ErrUnknownTenant         = errors.New("netsec: tenant is not registered")
	ErrRoleMismatch          = errors.New("netsec: role does not match tenant registration")
	ErrExternalIDMismatch    = errors.New("netsec: external ID does not match tenant registration")
	ErrResourceNotAllowed    = errors.New("netsec: source resource is not allowed for tenant")
	ErrResourceOwnerMismatch = errors.New("netsec: source resource is not owned by tenant")
	ErrInvalidSessionName    = errors.New("netsec: invalid session name")
	ErrInvalidDuration       = errors.New("netsec: invalid session duration")
	ErrRequestConflict       = errors.New("netsec: request ID reused with different parameters")
	ErrStaleConfiguration    = errors.New("netsec: tenant configuration changed before result publication")
	ErrSTSFailure            = errors.New("netsec: STS callback failed")
	ErrNoSTS                 = errors.New("netsec: no STS callback configured")
	ErrCredentialMismatch    = errors.New("netsec: STS credential does not match authorization decision")
	ErrCredentialExpired     = errors.New("netsec: STS credential is expired")
	ErrTenantExists          = errors.New("netsec: tenant is already registered")
	ErrTenantNotFound        = errors.New("netsec: tenant is not registered")
	ErrInvalidTenantRecord   = errors.New("netsec: invalid tenant record")
	ErrBrokerClosed          = errors.New("netsec: broker is closed")
)

// denial attaches a stable machine-readable code to a policy decision while
// preserving errors.Is behavior for the exported sentinel.
type denial struct {
	code   string
	err    error
	detail string
}

func (d *denial) Error() string {
	if d.detail == "" {
		return d.code
	}
	return fmt.Sprintf("%s: %s", d.code, d.detail)
}

func (d *denial) Unwrap() error {
	return d.err
}

func deny(code string, err error, format string, args ...any) error {
	return &denial{code: code, err: err, detail: fmt.Sprintf(format, args...)}
}

func denialCode(err error) string {
	var d *denial
	if errors.As(err, &d) {
		return d.code
	}
	return "internal_error"
}
