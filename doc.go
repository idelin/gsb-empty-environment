// Package netsec implements a dual-confirmation transaction for replacing an
// account's recovery channel (for example its recovery email address).
//
// # Security model
//
// A replacement never takes effect until proofs from BOTH the old channel and
// the new channel have been collected for the same change digest and the same
// deadline. Until that commit point the previously verified channel keeps all
// recovery power, so an unverified new address can never be used for account
// recovery.
//
// Each proof is an HMAC token bound to the triple (change digest, channel
// role, deadline). A proof minted for another transaction, for the other role
// or for a different term cannot be replayed against the current transaction.
//
// Explicit revocation, or starting a second change while one is in flight,
// archives the current transaction. Every proof issued for the archived
// transaction immediately stops verifying.
//
// The active recovery channel is swapped atomically at commit and is never
// cleared, so every intermediate state retains one definite recovery channel.
package netsec
