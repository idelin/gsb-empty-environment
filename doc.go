// Package netsec implements a fail-closed compiled-plan cache for a Go
// database host.
//
// Plan identity covers the canonical query AST digest, parameter type digest,
// principal, role closure digest, every referenced object's stable ID and
// schema version, required operations, row-level-security digest and the
// global policy epoch. Parameter values are never part of identity; they are
// bound structurally at execution time.
//
// Every cache hit is reviewed against a pinned SecuritySnapshot immediately
// before execution. A changed principal, role closure, policy epoch, object
// generation, RLS digest or privilege set is a miss and is recompiled, never
// an inherited allow. Rejected plans execute zero operators.
package netsec
