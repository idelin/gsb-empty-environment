// Package netsec provides defensive filtering for OCI referrers before
// signature or attestation verification.
//
// A registry's referrers response and the legacy sha256-<hex> index tag are
// untrusted inputs. FilterReferrers and FilterFallbackIndex keep only
// descriptors whose artifactType is allowlisted, fetch each referenced
// manifest, verify its content digest against the descriptor, and require the
// manifest subject to equal the queried subject. Rejected descriptors are never
// passed to a verifier.
package netsec
