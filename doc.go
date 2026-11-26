// Package spiffefed provides offline, fail-closed verification primitives for
// SPIFFE federation and workload attestation policy.
//
// The package treats a SPIFFE trust domain as an isolation boundary:
//
//   - ParseID rejects URL decoding, dot segments, empty path segments,
//     queries, fragments, and non-canonical trust domains.
//   - X509SVIDVerifier selects a trust bundle by the URI SAN's trust domain
//     and never falls back to another domain's bundle.
//   - JWTSVIDVerifier selects JWKS keys by the subject's trust domain and
//     binds kid lookup and signature verification to that domain.
//   - FederationPolicy requires external domains to be explicitly listed in
//     a target service's federates_with set.
//   - WorkloadMatcher requires exact node and process selector set equality,
//     rejects wildcard selectors, and validates namespace-consistent process
//     ancestry.
//
// The package uses only the Go standard library and performs no network I/O.
package spiffefed
