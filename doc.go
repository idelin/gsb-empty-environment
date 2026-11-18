// Package netsec resolves crate audit chains for the Go dependency gate.
//
// Audit records come in two shapes:
//
//   - FullAudit certifies one version outright and is the only trusted
//     starting point of a chain.
//   - DeltaAudit certifies a From -> To transition and is only usable when
//     From is itself reachable from a full audit along the same criteria.
//
// A version passes a gate exactly when it is reachable from a full audit via
// delta edges of one single criteria whose audits imply the gate criteria,
// and it is not covered by any violation interval. Violations always take
// precedence over audit reachability. The built-in criteria hierarchy states
// that safe-to-deploy implies safe-to-run, never the reverse.
//
// Version comparison follows semver precedence: prereleases sort below their
// release and among themselves by identifier, while build metadata after '+'
// is parsed but ignored.
package netsec
