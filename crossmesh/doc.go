// Package crossmesh implements graph-shaped cross-signing delegation pruning
// and three-dimensional slice verification for Go authentication hosts.
//
// A trust network is a directed graph of at most MaxNodes certificate nodes.
// Nodes may share sub-chains, form diamonds, or take part in rootless directed
// cycles. Every delegation edge carries an exact three-dimensional product:
//
//   - structural scope: AllowedPrefixes minus non-spliceable
//     ExcludedSubtrees (DNS suffixes or URI prefixes with label boundaries);
//   - a finite action set;
//   - a half-open [NotBefore, NotAfter) validity interval;
//
// plus a MaxPathLen hop budget using X.509 pathLenConstraint semantics: an
// edge with budget k may have at most k delegation edges after it on the same
// chain.
//
// A sub-authorization voxel is legitimate only when at least one acyclic
// trusted-root chain admits it at every hop, where "admits" means the voxel
// lies inside every hop's scope (and never inside any hop's excluded subtree),
// the action is allowed on every hop, the time is inside every hop's interval,
// and the hop-by-hop depth budget never goes negative. Voxels supported only
// by different chains are never combined: there is no per-dimension union that
// could stitch a time window from one parent chain onto an action from another.
//
// # Algorithm
//
// The name space is partitioned into atomic scope cells from every registered
// prefix; the time line is partitioned into atomic intervals from every edge's
// endpoints. A voxel is therefore a (scope cell, time interval, action mask)
// triple. The solver keeps states keyed by (node, remaining depth budget) and
// pushes edge-intersected voxel sets through the graph with a monotone worklist
// fixed point. State count is O(nodes * maxBudget) and every update is sparse.
// No root-to-leaf path is ever enumerated, so diamond and cyclic graphs cost
// polynomial rather than exponential work. Authority reachable only around a
// rootless cycle never reaches a trusted-root state and is discarded.
//
// # Revocation
//
// Revocation is a host-atomic compare-and-swap batch keyed on a SHA-256
// signature digest frozen over the canonicalized edge set. On digest mismatch
// the engine is untouched. On success the affected edges are removed together,
// the fixed point is re-solved, and each affected node receives disjoint
// retained and lost three-dimensional slice lists; nodes with no surviving
// chain are reported separately.
//
// IndependentAcceptor provides an independent, graph-reduction-free semantic
// acceptor that enumerates simple chains on small graphs only and is used by
// the exhaustive test suite to cross-check the polynomial solver.
package crossmesh
