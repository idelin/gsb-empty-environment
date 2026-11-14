package netsec

import "errors"

// Sentinel errors returned by the watch authorizer. Callers should match
// these with errors.Is rather than inspecting message text.
var (
	// ErrEmptyPrefix is returned when a watch is requested with an empty
	// prefix. An empty prefix would observe the whole database.
	ErrEmptyPrefix = errors.New("netsec: empty watch prefix")

	// ErrPrefixOutsideNamespace is returned when a watch prefix is not
	// fully contained in one of the tenant's permitted prefixes.
	ErrPrefixOutsideNamespace = errors.New("netsec: watch prefix escapes tenant namespace")

	// ErrUnknownTenant is returned when the tenant has not been
	// registered with the authorizer.
	ErrUnknownTenant = errors.New("netsec: unknown tenant")

	// ErrEmptyTenant is returned when a tenant name is empty.
	ErrEmptyTenant = errors.New("netsec: empty tenant name")

	// ErrTenantExists is returned by RegisterTenant for a duplicate
	// registration.
	ErrTenantExists = errors.New("netsec: tenant already registered")

	// ErrWatchLimitExceeded is returned when a tenant already holds the
	// maximum number of concurrent watches.
	ErrWatchLimitExceeded = errors.New("netsec: concurrent watch limit exceeded")

	// ErrRateLimited is returned by Publish when an event cannot be
	// delivered to a tenant because its event-rate budget is exhausted.
	ErrRateLimited = errors.New("netsec: event rate limit exceeded")

	// ErrStartInFuture is returned when a watch asks to start from a
	// version greater than the latest version the authorizer has seen.
	ErrStartInFuture = errors.New("netsec: start version is in the future")

	// ErrAuthorizerClosed is returned after the authorizer has been shut
	// down via Close.
	ErrAuthorizerClosed = errors.New("netsec: authorizer closed")
)

// ErrPermissionRevoked is returned as the terminal error of a Watch whose
// prefix is no longer covered by the tenant's namespace after a permission
// change. The event channel is closed immediately before this error is set.
type ErrPermissionRevoked struct {
	Tenant string
	Prefix string
}

func (e *ErrPermissionRevoked) Error() string {
	return "netsec: permission revoked for watch prefix " + e.Prefix
}
