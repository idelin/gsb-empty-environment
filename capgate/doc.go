// Package capgate is an importable capability-isolation acceptance gate for
// Go build hosts.
//
// It compares old and new dependency graphs over a small exhaustive universe
// of platform/feature assignments, tracks temporary credentials and optional
// branch provenance through unsandboxed install scripts and shared writable
// caches, and emits exact new high-privilege instances, disjoint minimal
// condition blocks, prime implicants, coverage proofs, and independently
// replayable grant chains.
//
// The main entry point is Compare; Acceptance is a host-facing alias.
package capgate
