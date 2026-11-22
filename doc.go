// Package destplan plans wrapping-edge deactivations that destroy a tenant's
// ability to decrypt chosen logical ranges while keeping legally retained
// ranges readable.
//
// # Model
//
// Inputs are immutable snapshot roots over a shared mapping tree (with
// interval translation via NodeShift and sparse holes via NodeHole) and a
// directed key-wrapping graph. A root key can unwrap any key reachable along
// a surviving path of edges. The same ciphertext block may have different
// coordinates in different snapshots, so leaves carry both a stable block
// identity range and a storage-coordinate range.
//
// Requests name half-open logical intervals that must be destroyed or kept
// readable. Deactivating an edge acts on every alias of that edge. There are
// at most 20 independently deactivatable edge groups, so the planner
// enumerates all 2^M subsets, runs a full reachability search for each, and
// returns the minimum-cost feasible set. Reachability is recomputed over
// every surviving edge, never along a single witness path, so every destroy
// path is provably cut and every retain key keeps at least one live path.
//
// All interval arithmetic is symbolic: node spans and block ranges are held
// as 128-bit endpoints (see Addr and SpanFull) so that the 2^64 exclusive end
// of the logical space is representable without per-sector expansion.
//
// # Ranking and proofs
//
// Feasible sets rank by total cost, then by number of deactivated groups,
// then by the lexicographic order of the sorted canonical edge-ID sequence.
// An impossible request returns an Infeasibility proof: a direct key/block
// conflict, an initially unreachable retain key, or a coupling proof that
// enumerates every inclusion-minimal destroy cut and shows each one also
// severs a retain key. The verifier subpackage independently rebuilds the
// model and re-enumerates every subset to check plans, lower bounds and
// proofs without trusting this package.
package destplan
