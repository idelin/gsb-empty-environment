// Package netsec implements a copy-on-write integrity (Merkle) tree for a
// secure block storage host.
//
// A Tree has a fixed fanout and leaf size supplied by the caller. Updates are
// staged in a transaction that may touch arbitrary, non-adjacent leaves; the
// transaction rebuilds only the nodes on the affected root-to-leaf paths and
// Commit atomically swaps the root digest. The live tree therefore never
// transiently mixes new leaves with old parents: unreachable nodes are
// constructed off to the side and become visible only through the root swap.
//
// Readers pin a Snapshot (the old root) and keep reading a consistent
// version while writers commit. Every read re-verifies the authentication
// path from the leaf up to the snapshot root before bytes are delivered.
package netsec
