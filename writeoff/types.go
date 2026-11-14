package writeoff

import (
	"errors"
	"time"
)

// Status is the write-off lifecycle stage of a payment.
type Status int

const (
	// StatusOpen means the payment is registered but not yet written off.
	StatusOpen Status = iota
	// StatusWrittenOff means exactly one matching confirmation has been applied.
	StatusWrittenOff
	// StatusReversed means a written-off payment has been reversed once.
	StatusReversed
)

// String returns the stable textual representation of a status.
func (s Status) String() string {
	switch s {
	case StatusOpen:
		return "open"
	case StatusWrittenOff:
		return "written-off"
	case StatusReversed:
		return "reversed"
	default:
		return "unknown"
	}
}

// Payment is a receivable that reconciliation may write off. Amount is expressed
// in the smallest currency unit (minor units) and must be positive.
type Payment struct {
	TxnID    string
	Amount   int64
	Currency string
	Period   string
}

// Confirmation is a write-off instruction. ConfirmID is the caller supplied
// idempotency key: re-sending the same confirmation returns the original
// result instead of writing the payment off again.
type Confirmation struct {
	ConfirmID string
	TxnID     string
	Amount    int64
	Currency  string
	Period    string
}

// Reversal cancels a previously applied write-off. ReversalID is its
// idempotency key. A reversal must match the original payment in amount,
// currency and accounting period.
type Reversal struct {
	ReversalID string
	TxnID      string
	Amount     int64
	Currency   string
	Period     string
}

// Aggregate is the persisted state of one payment, including the single
// applied confirmation and the single applied reversal.
type Aggregate struct {
	Payment      Payment
	Status       Status
	Confirm      *Confirmation
	Reversal     *Reversal
	CreatedAt    time.Time
	WrittenOffAt time.Time
	ReversedAt   time.Time
	UpdatedAt    time.Time
}

// ConfirmResult is the outcome of a Confirm call. Replayed is true when the
// call was a duplicate confirmation and the stored original result was
// returned, so the payment was not written off a second time.
type ConfirmResult struct {
	TxnID     string
	ConfirmID string
	Status    Status
	Replayed  bool
}

// ReversalResult is the outcome of a Reverse call. Replayed is true when the
// call was a duplicate reversal and the stored original result was returned.
type ReversalResult struct {
	TxnID      string
	ReversalID string
	Status     Status
	Replayed   bool
}

// Sentinel errors. Callers should inspect failures with errors.Is.
var (
	// ErrInvalidPayment is returned for a malformed payment registration.
	ErrInvalidPayment = errors.New("writeoff: invalid payment")
	// ErrPaymentExists is returned when a transaction number is registered twice.
	ErrPaymentExists = errors.New("writeoff: payment already registered")
	// ErrPaymentNotFound is returned when the transaction number is unknown.
	ErrPaymentNotFound = errors.New("writeoff: payment not found")
	// ErrInvalidConfirmation is returned for a malformed confirmation.
	ErrInvalidConfirmation = errors.New("writeoff: invalid confirmation")
	// ErrInvalidReversal is returned for a malformed reversal.
	ErrInvalidReversal = errors.New("writeoff: invalid reversal")
	// ErrPeriodMismatch is returned when the accounting period does not match.
	ErrPeriodMismatch = errors.New("writeoff: accounting period mismatch")
	// ErrCurrencyMismatch is returned when the currency does not match.
	ErrCurrencyMismatch = errors.New("writeoff: currency mismatch")
	// ErrPartialAmount is returned when the confirmed amount is below the
	// payment amount; partial write-offs are not allowed.
	ErrPartialAmount = errors.New("writeoff: partial write-off amount is not allowed")
	// ErrAmountMismatch is returned when the confirmed amount exceeds the
	// payment amount.
	ErrAmountMismatch = errors.New("writeoff: amount mismatch")
	// ErrAlreadyWrittenOff is returned when a new confirmation targets a
	// payment that already has an applied write-off.
	ErrAlreadyWrittenOff = errors.New("writeoff: payment has already been written off")
	// ErrAlreadyReversed is returned when a new operation targets a payment
	// that has already been reversed.
	ErrAlreadyReversed = errors.New("writeoff: payment has already been reversed")
	// ErrInvalidState is returned when the requested transition is not legal
	// for the current lifecycle status, for example reversing an open payment.
	ErrInvalidState = errors.New("writeoff: operation not allowed in current state")
	// ErrConflict is returned when an idempotency key is reused with a payload
	// that differs from the originally stored instruction.
	ErrConflict = errors.New("writeoff: idempotency key reused with different payload")
)
