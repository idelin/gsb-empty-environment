// Package rpki implements an offline, top-down RPKI relying-party validator
// and per-CA anti-rollback state machine.
//
// The entry point is Validator.ValidateTAL. It loads the trust-anchor
// certificate named by an offline TAL, binds its SubjectPublicKeyInfo to the
// pinned TAL key, verifies ECDSA P-256 certificate and CMS signatures,
// expands RFC 3779 inherit choices, enforces strict resource containment,
// verifies RFC 9286 manifests and CRLs, and recursively follows subordinate
// CA publication points.
//
// The package never accesses the network. Repository implementations are
// provided for in-memory maps and local operating-system directories.
package rpki
