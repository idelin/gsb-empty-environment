// Package netsec implements admission control for importing shared disk
// snapshots into a tenant.
//
// A snapshot manifest lists its logical blocks, the physical source of each
// block, and the digests of its parent (incremental base) snapshots. An
// import must never validate only the top-level manifest: incremental bases
// are hidden from the importer and may belong to other tenants.
//
// Verify walks the entire transitive parent closure, requires every
// snapshot in that closure (including hidden incremental bases) to be
// authorized through the host Authorizer, rejects cyclic parent references,
// and resolves each logical block to a single source using a deterministic
// nearest-to-root priority rule. Block sources that cannot be resolved inside
// the authorized closure are rejected.
package netsec
