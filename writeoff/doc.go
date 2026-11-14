// Package writeoff implements an importable, idempotent write-off component
// for reconciliation services.
//
// A payment is keyed by its transaction number and can be written off exactly
// once. Every confirmation is bound to the transaction number, amount,
// currency and accounting period of the payment: cross-period, cross-currency,
// partial and over-paid confirmations are rejected. The write-off lifecycle is
// open -> written-off -> reversed and every transition is applied atomically
// and can never move backwards.
//
// Replayed confirmations (and reversals) carry the caller supplied
// idempotency key and return the original result without touching the ledger a
// second time, so a replayed message can never create a second write-off.
//
// The component depends on the Store interface for atomicity. MemoryStore is a
// goroutine-safe in-process implementation; a database backed Store should run
// the mutate callback inside one transaction with a row lock (for example
// SELECT ... FOR UPDATE) and roll back when the callback returns an error.
package writeoff
