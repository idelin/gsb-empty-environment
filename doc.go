// Package netsec implements a password-reset commit path for an authentication
// host.
//
// The central invariant is that a successful password reset is a single
// linearizable operation which rotates both the credential epoch and the
// 64-bit session epoch, revokes every refresh-token family, and cancels every
// other pending login or reset transaction for the account. Access and refresh
// tokens are bound to those epochs, so tokens minted before the reset cannot be
// used to obtain a new session after the reset linearization point.
//
// MemoryStore is a reference, linearizable implementation of the state model.
// A durable host should provide the same atomic commit semantics: the password
// hash, both epoch increments, the reset receipt, family revocation, and
// transaction cancellation must all become visible together or not at all.
package netsec
