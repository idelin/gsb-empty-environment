package netsec

import (
	"errors"
	"fmt"
)

var (
	// Registration and input errors.
	ErrInvalidRequest  = errors.New("invalid settlement attribution request")
	ErrInvalidID       = errors.New("invalid identifier")
	ErrInvalidAmount   = errors.New("invalid settlement amount")
	ErrAmountTooSmall  = fmt.Errorf("%w: below the minimum", ErrInvalidAmount)
	ErrAmountTooLarge  = fmt.Errorf("%w: exceeds the allowed limit", ErrInvalidAmount)
	ErrInvalidCurrency = errors.New("invalid currency")
	ErrAlreadyExists   = errors.New("record already exists")
	ErrNotFound        = errors.New("record not found")

	// Attribution and terminal errors.
	ErrUnsignedTerminal = errors.New("terminal is not signed")
	ErrInactiveTerminal = errors.New("terminal is inactive")
	ErrTerminalMismatch = errors.New("terminal binding does not exist")
	ErrCrossMerchant    = errors.New("settlement crosses merchant boundaries")
	ErrCrossAcquirer    = errors.New("settlement crosses acquirer boundaries")
	ErrAccountMismatch  = errors.New("settlement account is not the signed account")
	ErrCurrencyMismatch = errors.New("settlement currency is not the signed currency")
	ErrMerchantInactive = errors.New("merchant is inactive")
	ErrAcquirerInactive = errors.New("acquirer is inactive")
	ErrAccountInactive  = errors.New("settlement account is inactive")

	// Rebinding errors.
	ErrRebindPending        = errors.New("terminal already has a pending rebind")
	ErrRebindNotFound       = errors.New("rebind request not found")
	ErrRebindNotPending     = errors.New("rebind request is not pending review")
	ErrRebindNotApproved    = errors.New("rebind request is not approved")
	ErrRebindNotEffective   = errors.New("rebind is not effective yet")
	ErrRebindNoChange       = errors.New("proposed binding is unchanged")
	ErrReviewerRequired     = errors.New("reviewer is required")
	ErrRequesterRequired    = errors.New("requester is required")
	ErrRebindAccountInvalid = errors.New("proposed account is not contracted to the proposed merchant and acquirer")

	// Idempotency errors.
	ErrIdempotencyConflict = errors.New("reference was already used with different settlement data")
)

// Aliases for callers that use alternate terminology.
var (
	ErrUnknownTerminal        = ErrUnsignedTerminal
	ErrSignedTerminalRequired = ErrUnsignedTerminal
	ErrMerchantMismatch       = ErrCrossMerchant
	ErrAcquirerMismatch       = ErrCrossAcquirer
	ErrCrossInstitution       = ErrCrossAcquirer
	ErrUnboundAccount         = ErrAccountMismatch
	ErrAmountInvalid          = ErrInvalidAmount
	ErrAmountAnomaly          = ErrInvalidAmount
	ErrRebindNotReviewed      = ErrRebindNotPending
)
