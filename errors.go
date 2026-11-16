package netsec

import "errors"

// Admission errors are intentionally stable so a forwarding loop can decide
// whether to log, count, or silently drop a rejected request.
var (
	// ErrNotConfigured is returned when Admit is called on a nil policy.
	ErrNotConfigured = errors.New("netsec: admission policy is not configured")

	// ErrMalformedRequest is returned when a request cannot be evaluated
	// safely (for example, a WriteProperty carrying an unexpected batch).
	ErrMalformedRequest = errors.New("netsec: malformed request")

	// ErrDenied is the common parent of all policy denials.
	ErrDenied = errors.New("netsec: request denied")

	// ErrServiceDenied is returned for services that are not explicitly
	// supported by the policy.
	ErrServiceDenied = errors.New("netsec: service denied")

	// ErrControlServiceDenied is returned for device-control services such as
	// DeviceCommunicationControl and ReinitializeDevice.
	ErrControlServiceDenied = errors.New("netsec: control service denied")

	// ErrTargetNotAllowed is the common parent of object/property denials.
	ErrTargetNotAllowed = errors.New("netsec: target not registered")

	// ErrObjectNotAllowed is returned when the object is not registered.
	ErrObjectNotAllowed = errors.New("netsec: object not registered")

	// ErrPropertyNotAllowed is returned when the object is registered but the
	// property is not registered for that object.
	ErrPropertyNotAllowed = errors.New("netsec: property not registered for object")

	// ErrPriorityDenied is the common parent of write-priority denials.
	ErrPriorityDenied = errors.New("netsec: write priority denied")

	// ErrPriorityRequired is returned when a write has no priority. A missing
	// priority is not treated as an implicitly safe write.
	ErrPriorityRequired = errors.New("netsec: write priority is required")

	// ErrPriorityReserved is returned for BACnet priorities 1 and 2, which
	// are reserved and can never be granted by this policy.
	ErrPriorityReserved = errors.New("netsec: write priority is reserved")

	// ErrPriorityOutOfRange is returned when priority is outside the
	// configured range or outside the valid BACnet range 1..16.
	ErrPriorityOutOfRange = errors.New("netsec: write priority is out of range")

	// ErrInvalidRule is returned when a registration refers to an invalid
	// BACnet object identifier or property identifier.
	ErrInvalidRule = errors.New("netsec: invalid registration rule")

	// ErrInvalidPriorityRange is returned when the configured priority range
	// would include the reserved priorities or is otherwise invalid.
	ErrInvalidPriorityRange = errors.New("netsec: invalid priority range")
)
