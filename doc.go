// Package impact computes revocation impact for signed Go build artifacts.
//
// Inputs are declarative call summaries carrying exact provenance identities,
// a legal-configuration ROBDD over at most 20 build options, and symbol
// bindings. For every entry point the analysis returns two canonical ROBDDs in
// the same variable order:
//
//   - Affected: configurations with a fully evidenced feasible call path that
//     touches a revoked object.
//   - Unknown: configurations not already Affected where a feasible path
//     reaches missing evidence (missing/stale certificate, missing summary, or
//     unresolved/ambiguous symbol binding).
//
// Symbol resolution uses exact provenance keys, never bare names. Summary
// equations are solved as a least fixed point, so unbounded mutual recursion
// terminates. Handles make callbacks independently registrable and removable;
// a callback completes at its registration site.
//
// Engine applies changes incrementally by dependency: replacing a summary,
// revoking/restoring an object, or changing a binding only recomputes entries
// whose dependency closure intersects the change. Snapshot values and their
// certificates are immutable, so old certificates continue to verify.
//
// Verifier provides an independent Boolean interpreter and full legal-
// configuration enumeration to check result ROBDDs, provenance identities, and
// call/return pairing.
package impact
