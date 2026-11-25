// Package reuseauth authenticates cross-navigation reuse of downloaded,
// segmented HTTP objects in a Go browser host.
//
// The package binds an Ed25519-signed redirect chain, signed final response,
// segment manifest, request context, navigation ID, navigation epoch, and an
// append-only origin-policy history prefix into one signed proof. Reuse is
// limited to exact absolute ranges with matching range-bound digests.
//
// Broker keeps private download identifiers and audit records on the trusted
// host side. Navigation-visible output is limited to ChangeSet values; failed
// private lookups are collapsed to ErrNotFound by NavigationFacingError.
//
// The host remains responsible for canonicalizing browser origins and
// top-level site keys, protecting Ed25519 private keys, and calling
// PrepareHit/CommitHit immediately before materializing cached bytes.
package reuseauth
