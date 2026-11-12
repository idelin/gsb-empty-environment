// Package netsec implements an importable check-validity analysis for a Go
// security-review service.
//
// The motivating weakness is a check/use race: code validates a shared length
// and later reads through an index, but another alias may write the shared
// field in between, turning the later read into an out-of-bounds access.
//
// The analyzer is field-sensitive and flow-sensitive over an explicit object
// graph and control-flow graph rather than Go syntax, so it can be driven by a
// front end of the review service:
//
//   - ObjectGraph models roots, objects and reference edges. Aliasing (two
//     roots reaching one object) and recursive/cyclic types are first class.
//   - Check nodes bind a check fact to the version of each concrete object
//     field target the checked expression resolves to.
//   - Write nodes and Call nodes (via a CallSummary possible-write set) bump
//     field versions. A write through any alias invalidates the facts whose
//     targets overlap the written target, using field-path prefixes.
//   - A worklist fixpoint computes per-node versions and surviving facts on a
//     finite lattice, so loops and irreducible CFGs terminate.
//   - A Read whose fact cannot be proven valid yields a Gap with a concrete
//     entry-to-read witness path and the invalidating node.
//
// The entry point is Analyze.
package netsec
