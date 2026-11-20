// Package netsec verifies NuGet packages.lock.json files for a Go build
// gateway.
//
// A lock file is parsed with ParseLockFile and checked with Verify. Verify
// enforces, per target framework:
//
//   - Every entry has one of the types Direct, Transitive or
//     CentralTransitive and carries a contentHash.
//   - contentHash is canonical standard (RFC 4648, padded) base64 and equals
//     the SHA-512 base64 hash of the actual .nupkg bytes supplied by the
//     caller. URL-safe base64, missing padding and other non-canonical
//     encodings are rejected before any hash comparison.
//   - The resolved version satisfies the requested NuGet version range
//     (including floating wildcards); a resolved version below the lower
//     bound is reported as a downgrade.
//   - Direct entries appear in the framework's root dependency list.
//   - The same package id (case-insensitive) cannot resolve to two different
//     versions within one framework; divergence across frameworks is allowed.
//   - Every non-Direct entry is referenced by another entry's dependencies
//     (no orphan packages).
//   - The dependency graph is acyclic and every entry is reachable from a
//     Direct entry.
//
// All detected problems are returned together as VerifyErrors, each carrying
// a stable Err* code suitable for programmatic handling.
package netsec
