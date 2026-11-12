package netsec

import (
	"errors"
	"fmt"
)

// Reason is a stable, machine-readable admission result code.
type Reason string

const (
	ReasonAllowed              Reason = ""
	ReasonMalformed            Reason = "malformed_request"
	ReasonUnknownService       Reason = "unknown_service"
	ReasonControlServiceDenied Reason = "control_service_denied"
	ReasonUnregisteredTarget   Reason = "unregistered_target"
	ReasonMissingPriority      Reason = "missing_priority"
	ReasonPriorityReserved     Reason = "priority_reserved"
	ReasonPriorityOutOfRange   Reason = "priority_out_of_range"
	ReasonEmptyWriteList       Reason = "empty_write_list"
)

// Violation describes one rejected write inside a request. Member is the
// zero-based index in Request.Writes for WritePropertyMultiple; it is -1 for
// a single WriteProperty or a service-level rejection.
type Violation struct {
	Member int
	Reason Reason
	Detail string
}

// Decision is the result of an admission check. Allowed is the only field a
// forwarding loop needs to inspect; the remaining fields are for logging and
// metrics.
type Decision struct {
	Allowed    bool
	Reason     Reason
	Member     int
	Detail     string
	Violations []Violation
}

// Err converts a denial into an error suitable for returning from the
// forwarding path.
func (d Decision) Err() error {
	if d.Allowed {
		return nil
	}
	return &DeniedError{Decision: d}
}

// DeniedError is returned by Policy.Authorize. It retains the full decision,
// including every violating member of a WritePropertyMultiple request.
type DeniedError struct {
	Decision Decision
}

func (e *DeniedError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Decision.Member >= 0 && e.Decision.Detail != "" {
		return fmt.Sprintf("admission denied at member %d: %s: %s",
			e.Decision.Member, e.Decision.Reason, e.Decision.Detail)
	}
	if e.Decision.Detail != "" {
		return fmt.Sprintf("admission denied: %s: %s",
			e.Decision.Reason, e.Decision.Detail)
	}
	return fmt.Sprintf("admission denied: %s", e.Decision.Reason)
}

// Is allows callers to match a denial kind with errors.Is.
func (e *DeniedError) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	denied, ok := target.(*DeniedError)
	return ok && denied.Decision.Reason == e.Decision.Reason
}

var (
	// ErrInvalidTarget is wrapped by target validation errors.
	ErrInvalidTarget = errors.New("invalid BACnet target")
	// ErrInvalidPolicy is wrapped by policy configuration errors.
	ErrInvalidPolicy = errors.New("invalid admission policy")
	// ErrReservedPriority is wrapped when a policy tries to enable priority
	// levels 1 or 2, which are reserved for life-safety use.
	ErrReservedPriority = errors.New("priority levels 1 and 2 are reserved")
)
