// Package krb5policy implements a fail-closed Kerberos V5 cross-realm
// trust and constrained-delegation policy verifier.
//
// The package deliberately does not perform network I/O.  It operates on
// already-decoded ticket metadata, a host-configured trust graph, a static
// directory snapshot, and an HMAC-SHA256 keytab.  Its main security
// invariants are:
//
//   - RFC 4120 DOMAIN-X500-COMPRESS transited fields are parsed with hard
//     bounds, expanded relative to the client realm, and never silently
//     interpreted as a permission to take an untrusted shortcut.
//   - Every realm in a transited path must correspond to an allowed,
//     directed trust hop.  A configured capath is an explicit policy, not
//     merely a hint.
//   - Ticket and PAC integrity is checked with real HMAC-SHA256 keys.
//   - Constrained delegation honors AllowedToDelegateTo, propagates
//     NOT_DELEGATED/SENSITIVE, never invents forwardable/proxiable flags,
//     and never enlarges ticket lifetimes.
//   - Clock skew boundaries are inclusive at the boundary and fail outside
//     it; callers can inject a clock for deterministic testing.
//
// A bounded adversarial search helper is included for auditing trust
// graphs.  It finds the shortest reachable unauthorized-transit or
// protected-user delegation path and has a separate exhaustive
// implementation that is intended for small-graph cross-checking.
package krb5policy
