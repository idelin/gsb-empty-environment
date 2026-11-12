// Package referrerfilter hardens OCI/Distribution referrer consumption.
//
// A registry returns every artifact attached to a subject through either the
// Referrers API or the sha256-<hex> fallback tag. Any push-capable actor can
// attach a malicious signature or attestation there, so a verification client
// must not trust the list as-is.
//
// FilterAndVerify applies four independent gates before a descriptor reaches a
// Verifier:
//
//   - The descriptor's artifactType must be present in an explicit allow list.
//   - The descriptor digest must be well formed (sha256 or sha512).
//   - The manifest is fetched and its bytes must digest to the descriptor's
//     digest (and match the descriptor size), which defeats content swapped
//     behind a pinned digest as well as tampered fallback indexes.
//   - The fetched manifest must declare the queried subject digest, which
//     defeats referrers pointing at a forged or unrelated subject.
//
// Descriptors sharing a digest are verified only once. Rejected descriptors are
// reported with a machine-readable Reason; by construction they are never
// handed to the Verifier.
package referrerfilter
