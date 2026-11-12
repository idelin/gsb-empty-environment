// Package netsec implements the metadata verification half of a TUF-style
// software update client. It is called by the download flow after each
// metadata file is fetched from a mirror, and it makes the trust decisions that
// defeat rollback (replay of old metadata) and freeze (the mirror stops
// publishing) attacks.
//
// Roles are verified in the mandated order root, timestamp, snapshot, targets.
// Every role's authorized keys and signature threshold come from the currently
// trusted root metadata. All expiry decisions use an injected Clock so tests
// can deterministically reproduce a freeze.
//
// Root rotation is the most sensitive operation: a new root must
//
//   - have a version exactly one greater than the trusted root,
//   - reach the root threshold under the OLD root's keys (the current trust
//     anchor authorizes the change), and
//   - reach the root threshold under its OWN keys (the new policy is already
//     internally consistent).
//
// On successful rotation the cached timestamp and snapshot are discarded.
//
// For timestamp, snapshot and targets the stored version number may never
// decrease; equal versions are accepted as a refresh. Snapshot pins the
// version and hash of targets metadata, and timestamp pins the version and
// hash of snapshot metadata; mismatches are rejected.
package netsec
