// Package netsec implements a tenant-aware fuzzing dictionary.
//
// A shared fuzzing platform automatically extracts tokens from inputs that
// belong to different tenants. A token may therefore carry a tenant secret
// that must never leak into another tenant's samples. This package binds every
// dictionary entry to its full provenance and enforces the following
// invariants:
//
//   - Each entry records the source tenant, the digest of the input it was
//     extracted from and whether the tenant attests that the token is publicly
//     provable (for example a documented protocol constant).
//   - Derivation unions the provenance of all parents: restrictions can only
//     accumulate. A public attestation can never downgrade or remove another
//     origin's secret taint, and derivation depth is tracked across levels.
//   - Entries are keyed by identity, never by content. Equal bytes observed in
//     different tenants stay separate entries with separate restrictions and
//     independent life cycles.
//   - A Snapshot is an immutable view over the authorized intersection: an
//     entry is visible only when every secret tenant attached to it is in the
//     authorized tenant set. Entries with only public origins are visible to
//     every tenant but remain bound to their origins for audit and revocation.
//   - Revoking a source (a tenant/input pair) invalidates every entry whose
//     provenance closure contains that source, including transitive derived
//     entries, and blocks new entries from being created from the revoked
//     source.
//
// All operations are safe for concurrent use by multiple goroutines.
package netsec
