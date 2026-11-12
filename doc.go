// Package netsec provides an importable admission policy for a Git hosting
// service's pre-receive hook.
//
// The policy enforces two rules for every push:
//
//   - Each commit newly introduced by a ref update must carry a valid
//     signature from an allowed signer. Reachability is computed over the
//     full commit graph (all parents), so unsigned commits hidden on a merge
//     side branch are rejected rather than skipped by a first-parent walk.
//   - The total object byte size of the newly introduced commits is reserved
//     from a shared, concurrency-safe quota in one atomic step. Any rejection
//     (unsigned commit, disallowed signer, insufficient quota) releases the
//     reservation so rejected pushes never consume repository capacity.
//
// Cryptographic signature verification is deliberately supplied by the hook
// host through a [VerifySignature] callback, keeping this package independent
// of any specific OpenPGP/SSH signing implementation.
package netsec
