// Package plancache implements an importable, security-preserving compiled
// plan cache model for a Go database host.
//
// # Security invariants
//
//  1. Plan identity is the full CacheKey: canonical AST digest, parameter
//     types, caller principal, role-closure digest, the fully expanded
//     dependency graph (nested view IDs/definition versions and base table
//     object IDs/schema versions), required operations, per-table RLS policy
//     digests and the global policy epoch. Raw SQL text and relation names
//     are deliberately absent from CompileInput, so a plan can never be
//     reused by SQL text or view name alone.
//  2. Parameter values never enter plan identity. Values are supplied at
//     execution time through structured, type-checked bindings.
//  3. Every cache hit is re-validated against the transaction permission
//     snapshot before a single operator callback runs: principal liveness,
//     role closure, policy epoch, every nested view definition version,
//     every base object ID/schema version, RLS digests and privileges. Any
//     drift is a cache miss and triggers recompilation via a fresh input
//     resolution; a stale plan never executes an operator.
//  4. A transaction pins its permission/catalog snapshot at begin. An
//     immediate REVOKE or immediate view/RLS tightening pierces the
//     snapshot: waiting plans fail before any operator callback. Ordinary
//     (non-immediate) revocation only affects later transactions.
//  5. Switching the principal on a pooled connection instantly invalidates
//     every session-local handle and clears the session cache. GLOBAL cache
//     entries stay available, but each is only reachable through the new
//     principal's own full key match.
package plancache
