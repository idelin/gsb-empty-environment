// Package ledger implements an account model that keeps available, frozen
// and settled amounts strictly separated. Every freeze is bound to a
// currency and a client request id; after a partial settlement only the
// remaining frozen amount may be released. Concurrent settle/release
// commits are adjudicated by optimistic version checks, batch cross-account
// operations apply atomically, and a per-currency conservation invariant
// plus idempotent receipts guard fault replays against duplicate effects.
package ledger

import "errors"

// Money is expressed in integer minor units of a currency.
type Money int64

// FreezeStatus describes the lifecycle of a freeze record.
type FreezeStatus string

const (
	FreezeOpen     FreezeStatus = "OPEN"
	FreezeSettled  FreezeStatus = "SETTLED"
	FreezeReleased FreezeStatus = "RELEASED"
)

// Freeze binds a frozen amount to its originating request id and currency.
// Remaining is the portion still held: settlement consumes it, and only
// Remaining may ever be released back to the account.
type Freeze struct {
	ID        string
	RequestID string
	AccountID string
	Currency  string
	Amount    Money
	Remaining Money
	Status    FreezeStatus
	Version   uint64
}

// Account keeps the three amount buckets separated per currency.
type Account struct {
	ID        string
	Available map[string]Money
	Frozen    map[string]Money
	Version   uint64
}

func (a *Account) clone() *Account {
	c := &Account{
		ID:        a.ID,
		Version:   a.Version,
		Available: make(map[string]Money, len(a.Available)),
		Frozen:    make(map[string]Money, len(a.Frozen)),
	}
	for k, v := range a.Available {
		c.Available[k] = v
	}
	for k, v := range a.Frozen {
		c.Frozen[k] = v
	}
	return c
}

// OpKind enumerates the operations that produce receipts.
type OpKind string

const (
	OpDeposit OpKind = "DEPOSIT"
	OpFreeze  OpKind = "FREEZE"
	OpSettle  OpKind = "SETTLE"
	OpRelease OpKind = "RELEASE"
	OpBatch   OpKind = "BATCH"
)

// Receipt is the durable, replayable outcome of an operation. Replaying a
// request id returns the identical receipt without re-applying effects.
type Receipt struct {
	RequestID string
	Kind      OpKind
	AccountID string
	Currency  string
	Amount    Money
	FreezeID  string
	Version   uint64
	Seq       uint64
}

// Sentinel errors. Go 1.11 compatible: compare with ==.
var (
	ErrAccountNotFound    = errors.New("ledger: account not found")
	ErrAccountExists      = errors.New("ledger: account already exists")
	ErrFreezeNotFound     = errors.New("ledger: freeze not found")
	ErrFreezeClosed       = errors.New("ledger: freeze already closed")
	ErrInsufficientFunds  = errors.New("ledger: insufficient available funds")
	ErrInsufficientFrozen = errors.New("ledger: amount exceeds remaining frozen")
	ErrVersionConflict    = errors.New("ledger: version conflict on commit")
	ErrInvalidAmount      = errors.New("ledger: amount must be positive")
	ErrEmptyRequestID     = errors.New("ledger: request id required")
	ErrEmptyBatch         = errors.New("ledger: batch contains no operations")
)
