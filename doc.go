// Package netsec implements k-anonymous publication with global recoding for
// the UID 1275 security export service.
//
// An export declares at most MaxQuasiIdentifiers quasi-identifier columns,
// each backed by a category tree of height at most MaxTreeHeight. The solver
// picks one ancestor level per column, partitions records by the resulting
// generalized tuples, and withholds every equivalence class smaller than K in
// full. A level vector is feasible only when the withheld record count stays
// within the suppression budget. The objective is the exact minimum of level
// cost plus suppression cost, with lexicographically smallest level vectors
// winning ties. Anonymize verifies its branch-and-bound result against an
// exhaustive enumeration of the lattice and refuses publication with
// ErrNoFeasiblePublication when no feasible vector exists.
package netsec
