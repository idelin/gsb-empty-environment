// Package netsec implements safe version-graph selection for a Go update
// host client.
//
// A Manifest lists at most MaxNodes VersionNode values and the Edges that are
// legal migrations. Each node carries a cryptographically signed
// SecurityLevel, compatibility conditions, and an optional revocation status.
// Select only traverses manifest edges, rejects revoked or incompatible
// nodes and nodes whose level signature fails, and requires that security
// level never decreases along the chosen route. It returns the reachable
// eligible destination other than the current version with the minimum total
// migration cost (ties broken by hop count and then lexicographic path). If
// no eligible migration exists, an eligible current version is returned as a
// zero-length fallback; otherwise a structured RejectReason explains the
// refusal.
package netsec
