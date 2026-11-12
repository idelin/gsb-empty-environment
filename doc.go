// Package taskpkg implements authenticated incremental update validation for a
// Go name service.
//
// Authorized master nodes submit updates as a chain of signed segments. Each
// segment signature binds the zone identity, starting version, target version,
// digest of the previous segment and the full add/delete operation sequence.
// The final segment additionally signs the digest of the entire round and the
// number of segments.
//
// A round is accepted only when it is a complete chain anchored at the store's
// current version, every signature comes from an authorized master, and every
// delete matches the digest of an existing record. The whole round is verified
// against an in-memory staging copy before any record is published, so a
// rejected round never deletes or mutates live records. A segment carrying a
// cryptographically valid signature is rejected whenever its contextual fields
// (zone, versions, predecessor digest, round digest) do not match the current
// context.
package taskpkg
