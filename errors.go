package netsec

import "fmt"

var (
	// ErrInvalidRequest is returned for malformed requests.
	ErrInvalidRequest = fmt.Errorf("quota: invalid request")
	// ErrAccountNotFound is returned when no policy exists for an account.
	ErrAccountNotFound = fmt.Errorf("quota: account policy not found")
	// ErrQuotaExceeded is the broad error matched by every limit rejection.
	ErrQuotaExceeded = fmt.Errorf("quota: limit exceeded")
	// ErrIdempotencyConflict is returned when a key is reused with a different
	// account, channel, or amount.
	ErrIdempotencyConflict = fmt.Errorf("quota: idempotency key conflict")
	// ErrReservationNotFound is returned when releasing an unknown key.
	ErrReservationNotFound = fmt.Errorf("quota: reservation not found")
	// ErrReservationReleased is returned when an already released key is used
	// for a new reservation attempt.
	ErrReservationReleased = fmt.Errorf("quota: reservation already released")
)

// WindowKind identifies the quota window that rejected a reservation.
type WindowKind string

const (
	WindowDaily   WindowKind = "daily"
	WindowMonthly WindowKind = "monthly"
)

// Scope identifies whether an account-wide or channel-specific limit rejected
// a reservation.
type Scope string

const (
	ScopeAccount Scope = "account"
	ScopeChannel Scope = "channel"
)

// QuotaExceededError describes the exact limit that rejected a reservation.
type QuotaExceededError struct {
	Scope     Scope
	Window    WindowKind
	Limit     int64
	Used      int64
	Requested int64
}

func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf(
		"quota: %s %s limit exceeded (used=%d requested=%d limit=%d)",
		e.Scope,
		e.Window,
		e.Used,
		e.Requested,
		e.Limit,
	)
}

// Unwrap allows errors.Is(err, ErrQuotaExceeded) to match every rejection.
func (e *QuotaExceededError) Unwrap() error {
	return ErrQuotaExceeded
}

// IdempotencyConflictError reports a key reused with different request data.
type IdempotencyConflictError struct {
	Key string
}

func (e *IdempotencyConflictError) Error() string {
	return fmt.Sprintf("%s: %q", ErrIdempotencyConflict, e.Key)
}

// Unwrap allows errors.Is(err, ErrIdempotencyConflict).
func (e *IdempotencyConflictError) Unwrap() error {
	return ErrIdempotencyConflict
}
