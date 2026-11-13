// Package netsec resolves loader symbols using the review rules defined by
// this package, rather than the rules of a particular platform linker.
//
// A Module has a unique ID, a trust bit, ordered dependencies, and exports.
// An export is identified by the pair (name, version) and is either strong or
// weak. The host supplies the initial global module order. Resolve searches
// that order and the origin module's dependency graph in a deterministic
// order, then chooses the first strong definition, or the first weak
// definition when no strong definition exists.
//
// AnalyzeBudget considers insertion of at most three supplied plugins into
// the global order. It finds every minimum-size insertion plan that makes a
// trusted-required relocation bind an export from an untrusted module. The
// search is exhaustive unless the caller's layout budget runs out, in which
// case the result is Unknown rather than a partial minimum.
package netsec
