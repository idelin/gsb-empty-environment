package netsec

import "errors"

var (
	// ErrUnknownTarget is returned when a signal targets an ID that is not
	// registered with the host.
	ErrUnknownTarget = errors.New("netsec: unknown target")

	// ErrTenantUnknown is returned when a tenant has no quota entry on the host.
	ErrTenantUnknown = errors.New("netsec: unknown tenant")

	// ErrNotAuthorized is returned when the enqueue-time policy rejects the
	// (tenant, target generation, signal) triple. The policy is consulted again
	// at delivery time.
	ErrNotAuthorized = errors.New("netsec: tenant not authorized for signal")

	// ErrInvalidSignal is returned for signal numbers outside the host range.
	ErrInvalidSignal = errors.New("netsec: invalid signal number")

	// ErrStandardPayload is returned when a standard (coalesced) signal carries
	// a payload; standard signals carry no queued payload.
	ErrStandardPayload = errors.New("netsec: standard signals cannot carry a payload")

	// ErrPayloadTooLarge is returned when a realtime payload exceeds
	// Config.MaxPayloadBytes.
	ErrPayloadTooLarge = errors.New("netsec: payload too large")

	// ErrTenantQuota is returned when the tenant's own queue allowance is
	// exhausted.
	ErrTenantQuota = errors.New("netsec: tenant queue quota exhausted")

	// ErrCapacity is returned when the shared queue is full.
	ErrCapacity = errors.New("netsec: queue capacity exhausted")

	// ErrReservedSlots is returned when admitting the signal would eat into
	// slots reserved for tenants that have not consumed their guaranteed share.
	ErrReservedSlots = errors.New("netsec: cannot consume reserved queue slots")

	// ErrAlreadyClosed is returned after Host.Close.
	ErrAlreadyClosed = errors.New("netsec: host closed")

	// ErrCanceled is returned by Cancel for envelopes that were already
	// delivered, already canceled, or otherwise no longer queued.
	ErrCanceled = errors.New("netsec: envelope not queued")
)
