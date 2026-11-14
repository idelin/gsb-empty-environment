// Package netsec implements workload identity verification for a service mesh
// control plane.
//
// A workload principal is never inferred from a DNS SAN or from a wildcard.
// It is the conjunction of:
//
//   - an exact SPIFFE trust domain and path carried in a URI SAN;
//   - the complete, typed SAN set of the leaf certificate;
//   - a certificate chain that verifies against the current trust bundle for
//     the SPIFFE trust domain;
//   - a fresh node attestation bound to the same trust domain, workload ID,
//     and connection nonce; and
//   - the caller and target identity policies that are in force when the
//     connection is established.
//
// DNS wildcards are policy declarations only. They are accepted only in the
// leftmost label, match exactly one label, and are restricted to hostname
// matching. A wildcard can never create or widen a SPIFFE principal. DNS, IP,
// and URI SANs are kept in distinct namespaces and are never substituted for
// one another.
//
// Trust bundles, revocations, short-lived certificate rotations, and
// connection policies are versioned by generation. Updates are monotonic,
// every verification uses one immutable snapshot, and authorization is
// re-evaluated for every connection instead of being cached.
package netsec
