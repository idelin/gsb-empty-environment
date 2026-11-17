// Package netsec selects a safe version in an authenticated update graph.
//
// A manifest lists the only legal migrations. Selection rejects revoked
// versions, versions that do not satisfy the host compatibility conditions,
// versions whose signed security level cannot be authenticated, and paths on
// which the security level would decrease. Among the remaining reachable
// versions, the minimum-cost target is returned together with the path and
// structured rejection reasons for the versions that were not selected.
package netsec
