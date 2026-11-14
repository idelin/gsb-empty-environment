package netsec

import "errors"

// Sentinel errors returned by Guard. They can be inspected with errors.Is.
var (
	// ErrConfig is returned when NewGuard receives an invalid configuration.
	ErrConfig = errors.New("netsec: invalid guard configuration")
	// ErrTenantInvalid is returned when the caller supplies an empty or
	// excessively large tenant identifier.
	ErrTenantInvalid = errors.New("netsec: invalid tenant identifier")
	// ErrTooManyLabels is returned when a metric carries more labels than
	// Config.MaxLabels permits.
	ErrTooManyLabels = errors.New("netsec: too many labels on metric")
	// ErrUnknownKey is returned for a label key that has no KeyPolicy.
	ErrUnknownKey = errors.New("netsec: label key not allowed")
	// ErrValueNotAllowed is returned for a value that violates a
	// Reject policy or that is not in a whitelist-only policy.
	ErrValueNotAllowed = errors.New("netsec: label value not allowed")
	// ErrValueTooLong is returned for an overlong value whose policy
	// rejects instead of bucketing.
	ErrValueTooLong = errors.New("netsec: label value too long")
	// ErrSeriesQuota is returned when a tenant already owns its maximum
	// number of distinct label sets (series).
	ErrSeriesQuota = errors.New("netsec: per-tenant series quota exceeded")
	// ErrRateLimited is returned when a tenant exceeds its request rate.
	ErrRateLimited = errors.New("netsec: tenant rate limit exceeded")
)
