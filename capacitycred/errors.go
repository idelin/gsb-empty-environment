package capacitycred

import "errors"

// Domain errors. Callers should use errors.Is rather than matching strings.
var (
	// ErrMissingCallback is returned at construction and at every receive
	// entry when a required host callback is absent or nil. The component
	// never degrades to a skipped verification.
	ErrMissingCallback = errors.New("capacitycred: required host callback is missing or nil")
	ErrInvalidEvent    = errors.New("capacitycred: malformed or inconsistent event")
	ErrBadSignature    = errors.New("capacitycred: event signature failed verification")
	ErrBadEventID      = errors.New("capacitycred: event id does not bind the event body")
	ErrDuplicateEvent  = errors.New("capacitycred: conflicting event reuses an event id")
	ErrTenantMismatch  = errors.New("capacitycred: event or request crosses tenant boundary")
	ErrUnknownCred     = errors.New("capacitycred: unknown credential")
	ErrConflict        = errors.New("capacitycred: double spend on an overlapping interval")
	ErrEncumbered      = errors.New("capacitycred: credential still holds delivered buffers")
	ErrNoFinality      = errors.New("capacitycred: no valid finality receipt for the credential root")
	ErrBlocked         = errors.New("capacitycred: credential is revoked or quarantined")
	ErrWrongOwner      = errors.New("capacitycred: credential is not owned by the local node")
	ErrInsufficient    = errors.New("capacitycred: not enough free capacity on credential")
	ErrParamsMismatch  = errors.New("capacitycred: parameters do not match the credential commitment")
	ErrOverflow        = errors.New("capacitycred: 64-bit capacity arithmetic overflow")
	ErrBadReceipt      = errors.New("capacitycred: finality receipt does not bind root and event prefix")
	ErrUnresolved      = errors.New("capacitycred: event set references unknown causal predecessors")
	ErrTaskNotFound    = errors.New("capacitycred: task not found")
	ErrTaskReentry     = errors.New("capacitycred: task request differs from the original reservation")
	ErrTaskRunning     = errors.New("capacitycred: task callback is already running")
	ErrTaskCompleted   = errors.New("capacitycred: task already completed and released")
	ErrAllocator       = errors.New("capacitycred: allocator refused the reservation")
)
