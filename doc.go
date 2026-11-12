// Package netsec resolves multi-account key aliases for a build service.
//
// An alias is never a global identity: AliasRef is fully qualified by
// account, region, and alias name, so the same name in two accounts or
// regions can bind different key material. Resolver.Begin opens a
// transaction whose StoreSnapshot and per-reference result cache pin every
// resolution in that transaction; rebinding aliases concurrently cannot
// change the versions, hops, or fingerprints observed by the transaction.
//
// Alias chains may span multiple hops and accounts, but every hop is checked
// against the CallerIdentity authorization set: entering an unauthorized
// account fails with ErrAccountNotAuthorized. Cycles fail with ErrCycle and
// acyclic chains longer than the configured hop limit fail with ErrMaxHops.
//
// BuildSignRequest turns a pinned Resolution into a SignRequest bound to the
// final public key fingerprint rather than to a bare alias name, giving the
// key host a tamper-evident object to verify before signing.
package netsec
