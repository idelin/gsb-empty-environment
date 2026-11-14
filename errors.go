package netsec

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInvalidRequest reports a malformed or unsupported data subject
	// request.
	ErrInvalidRequest = errors.New("netsec: invalid data subject request")

	// ErrInvalidConfiguration reports an unusable Tracker configuration.
	ErrInvalidConfiguration = errors.New("netsec: invalid configuration")

	// ErrInvalidSystem reports an unusable copy system definition.
	ErrInvalidSystem = errors.New("netsec: invalid copy system")

	// ErrIdentityVerificationFailed reports that a request's identity proof
	// did not establish that the caller is the named data subject. Callers
	// should treat all verification failures identically to avoid leaking
	// whether a subject is enrolled.
	ErrIdentityVerificationFailed = errors.New("netsec: identity verification failed")

	// ErrSubjectAlreadyEnrolled reports a duplicate enrollment attempt.
	ErrSubjectAlreadyEnrolled = errors.New("netsec: subject already enrolled")

	// ErrRequestNotFound reports an unknown request identifier.
	ErrRequestNotFound = errors.New("netsec: request not found")

	// ErrRequestBusy reports that another goroutine is currently processing
	// the same request.
	ErrRequestBusy = errors.New("netsec: request is already being processed")

	// ErrInvalidReceipt reports a receipt that does not prove completion of
	// the task it was returned for.
	ErrInvalidReceipt = errors.New("netsec: invalid receipt")

	// ErrIncomplete reports that a request still has copy systems without
	// completed receipts.
	ErrIncomplete = errors.New("netsec: request incomplete")

	// ErrNoCopySystems reports a request that cannot be tracked because no
	// copy systems are configured.
	ErrNoCopySystems = errors.New("netsec: no copy systems configured")

	// ErrIncompleteInventory reports a Tracker inventory that does not cover
	// every standard copy role.
	ErrIncompleteInventory = errors.New("netsec: incomplete copy system inventory")
)

// IncompleteError describes copy systems that have not completed a request.
// Missing contains every system without a valid receipt. Blocked is the
// subset that exhausted its retry budget.
type IncompleteError struct {
	RequestID string
	Missing   []string
	Blocked   []string
}

func (e *IncompleteError) Error() string {
	if e == nil {
		return ErrIncomplete.Error()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: request %q is missing %d system(s)", ErrIncomplete, e.RequestID, len(e.Missing))
	if len(e.Missing) > 0 {
		fmt.Fprintf(&b, ": %s", strings.Join(e.Missing, ", "))
	}
	if len(e.Blocked) > 0 {
		fmt.Fprintf(&b, " (retry budget exhausted: %s)", strings.Join(e.Blocked, ", "))
	}
	return b.String()
}

// Unwrap allows errors.Is(err, ErrIncomplete) checks.
func (e *IncompleteError) Unwrap() error { return ErrIncomplete }

func invalidRequestf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}

func invalidReceiptf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReceipt, fmt.Sprintf(format, args...))
}
