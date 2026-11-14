package netsec

import (
	"strings"
	"time"
)

// Identifier aliases make the API self-documenting while remaining easy to
// use from existing settlement services that already store ids as strings.
type (
	AcquirerID = string
	MerchantID = string
	TerminalID = string
	AccountID  = string
	Currency   = string
)

// Acquirer is a card acquirer (or another settlement institution) that is
// allowed to originate settlements.
type Acquirer struct {
	ID   string
	Name string
}

// Merchant is the merchant that owns a signed terminal.
type Merchant struct {
	ID   string
	Name string

	// MaxSettlementCents optionally lowers the global per-settlement limit for
	// this merchant. Zero means that the global limit applies.
	MaxSettlementCents int64
}

// SettlementAccount is a bank account contracted to one merchant and one
// acquirer. It is deliberately not possible to change the owner of an account:
// a new contracted account must be registered and selected through a terminal
// rebind instead.
type SettlementAccount struct {
	ID         string
	MerchantID string
	AcquirerID string

	// Currency is an ISO-4217-style code. Empty means CNY.
	Currency string

	// MaxSettlementCents optionally lowers the global per-settlement limit for
	// this account. Zero means that the merchant/global limit applies.
	MaxSettlementCents int64
}

// Account is a short alias for SettlementAccount.
type Account = SettlementAccount

// TerminalBinding is the complete attribution tuple created at signing. A
// terminal can only pay the account in this tuple.
type TerminalBinding struct {
	TerminalID string
	MerchantID string
	AcquirerID string
	AccountID  string

	// Currency is optional when all other records use the same currency.
	// Empty is resolved to the account currency at signing time.
	Currency string
}

// Terminal is a short alias for TerminalBinding.
type Terminal = TerminalBinding

// SettlementRequest describes a settlement without giving the caller control
// over the destination account. AccountID may be supplied by a caller that
// knows the expected account; when present it is checked against the signed
// binding and is never used as the payment destination directly.
type SettlementRequest struct {
	// Reference is an optional idempotency key. Reusing a reference with
	// different terminal, merchant, institution, account, amount, or currency
	// is rejected.
	Reference string

	TerminalID string
	MerchantID string
	AcquirerID string

	// AccountID is optional. If supplied, it must equal the account in the
	// signed terminal binding.
	AccountID string

	AmountCents int64
	Currency    string
}

// AmountCents is an alias for int64 to make amount fields explicit at call
// sites that use named conversions.
type AmountCents = int64

// Settlement is the trusted attribution decision for a settlement. AccountID
// is always derived from the signed terminal binding, never from the request's
// destination value.
type Settlement struct {
	Reference   string
	TerminalID  string
	MerchantID  string
	AcquirerID  string
	AccountID   string
	AmountCents int64
	Currency    string
	SettledAt   time.Time
}

// DestinationAccountID returns the only account to which the settlement may
// be paid. The method exists to make payout integrations explicit about using
// the authoritative result rather than an incoming request field.
func (s Settlement) DestinationAccountID() string {
	return s.AccountID
}

// RebindStatus is the lifecycle state of a terminal rebind request.
type RebindStatus string

const (
	RebindPendingReview RebindStatus = "pending_review"
	RebindApproved      RebindStatus = "approved"
	RebindRejected      RebindStatus = "rejected"
	RebindActive        RebindStatus = "active"
	RebindCancelled     RebindStatus = "cancelled"
)

// RebindRequest is an auditable request to change a terminal's complete
// attribution tuple. A new binding is never applied at request time.
type RebindRequest struct {
	ID         string
	TerminalID string
	Current    TerminalBinding
	Proposed   TerminalBinding
	Status     RebindStatus

	RequestedBy string
	RequestedAt time.Time

	ReviewedBy  string
	ReviewedAt  time.Time
	EffectiveAt time.Time
	ActivatedAt time.Time
	Reason      string
}

// RebindOptions carries optional review metadata.
type RebindOptions struct {
	Reason string
}

func normalizedID(id string) (string, bool) {
	if id == "" || strings.TrimSpace(id) != id {
		return "", false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return id, true
}

func normalizedCurrency(currency string, fallback string) (string, bool) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = fallback
	}
	if currency == "" {
		currency = "CNY"
	}
	if len(currency) != 3 {
		return "", false
	}
	for _, r := range currency {
		if r < 'A' || r > 'Z' {
			return "", false
		}
	}
	return currency, true
}

func sameBinding(a, b TerminalBinding) bool {
	return a.TerminalID == b.TerminalID &&
		a.MerchantID == b.MerchantID &&
		a.AcquirerID == b.AcquirerID &&
		a.AccountID == b.AccountID &&
		a.Currency == b.Currency
}
