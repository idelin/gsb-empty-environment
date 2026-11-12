// Package netsec splits a wide cloud-data authorization into non-overlapping
// work shards.
//
// The authorization geometry is an n-dimensional half-open integer box
// (Box). The pipeline is:
//
//   - Subtract removes the union of denied boxes and returns a disjoint box
//     partition of the still-accessible points.
//   - Split cuts that partition into pieces whose volume (number of integer
//     points) never exceeds a configured maximum.
//   - Mint/MintShards issue HMAC-authenticated tokens binding each shard's
//     region to the digest of the parent authorization. Verify and
//     VerifyFor validate tokens, and VerifyFor additionally proves a
//     requested region stays inside the shard, so retrying a shard can never
//     widen its scope.
//
// The union of all shards is exactly the accessible region: no holes, no
// overlaps. This is verified exhaustively over small grids in the tests.
package netsec
