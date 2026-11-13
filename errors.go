package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors. Concrete failures are wrapped in *AccessError so callers
// can use errors.Is while retaining a human-readable detail string.
var (
	ErrUnknownDevice         = errors.New("unknown device")
	ErrDeviceExists          = errors.New("requester already attached")
	ErrStaleDeviceHandle     = errors.New("device handle was revoked")
	ErrUnknownDomain         = errors.New("unknown domain")
	ErrDomainExists          = errors.New("domain already registered")
	ErrForeignDomain         = errors.New("caller is not the device owner")
	ErrStaleDeviceGeneration = errors.New("stale device generation")
	ErrGenerationExhausted   = errors.New("generation space exhausted")
	ErrUnknownVCPU           = errors.New("vCPU not registered")
	ErrStaleVCPUGeneration   = errors.New("stale vCPU generation")
	ErrVCPUGenerationRegress = errors.New("vCPU generation did not advance")
	ErrVectorInvalid         = errors.New("invalid vector or vector range")
	ErrVectorOutOfRange      = errors.New("vector outside domain range")
	ErrDeliveryModeNotFixed  = errors.New("delivery mode is not Fixed")
	ErrNoMapping             = errors.New("no present remapping entry")
	ErrVectorMismatch        = errors.New("message vector does not match registered vector")
	ErrNilCallback           = errors.New("nil delivery callback")
	ErrDuplicateTableIndex   = errors.New("duplicate table index in batch")
	ErrBatchAborted          = errors.New("batch item not applied")
	ErrZeroVCPUGeneration    = errors.New("vCPU generation must be non-zero")
)

// AccessError adds operation and request context to a sentinel error.
type AccessError struct {
	Operation string
	Reason    error
	Detail    string
}

func (e *AccessError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %v", e.Operation, e.Reason)
	}
	return fmt.Sprintf("%s: %v: %s", e.Operation, e.Reason, e.Detail)
}

// Unwrap supports errors.Is/errors.As.
func (e *AccessError) Unwrap() error { return e.Reason }

func fail(operation string, reason error, format string, args ...any) error {
	detail := ""
	if format != "" {
		detail = fmt.Sprintf(format, args...)
	}
	return &AccessError{Operation: operation, Reason: reason, Detail: detail}
}

// BatchItemResult reports the outcome of one request in a batch.
type BatchItemResult struct {
	TableIndex TableIndex
	Applied    bool
	Err        error
}

// BatchError is returned when a batch partially or completely fails. Items
// that were successfully committed remain committed; items that failed remain
// non-deliverable. The remapper deliberately does not roll back safely closed
// items to an earlier configuration.
type BatchError struct {
	Operation string
	Results   []BatchItemResult
}

func (e *BatchError) Error() string {
	var failures []string
	for _, result := range e.Results {
		if result.Err == nil {
			continue
		}
		state := "not applied"
		if result.Applied {
			state = "applied"
		}
		failures = append(failures, fmt.Sprintf("index %d (%s): %v",
			result.TableIndex, state, result.Err))
	}
	if len(failures) == 0 {
		return e.Operation + ": batch failed"
	}
	return fmt.Sprintf("%s: %s", e.Operation, strings.Join(failures, "; "))
}

// Unwrap exposes every per-item error to errors.Is/errors.As.
func (e *BatchError) Unwrap() []error {
	errs := make([]error, 0, len(e.Results))
	for _, result := range e.Results {
		if result.Err != nil {
			errs = append(errs, result.Err)
		}
	}
	return errs
}
