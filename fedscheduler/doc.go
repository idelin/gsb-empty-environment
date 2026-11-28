// Package fedscheduler provides deterministic replica apportionment and
// minimal-disruption rebalancing primitives for a Karmada-style federated
// multi-cluster control plane.
//
// # Capped weighted apportionment
//
// Allocate implements recursive overflow redistribution under hard per-cluster
// caps. Every round:
//
//  1. each unsaturated cluster receives floor(R_remaining * w_i / W);
//  2. the fractional remainders are distributed by the largest-remainder
//     method (Hamilton method), with ties broken by ascending cluster name;
//  3. clusters whose grant reaches their cap are locked, and the still
//     unplaced overflow re-enters the loop redistributed over the remaining
//     clusters by their relative weights.
//
// All arithmetic uses integer or exact big.Int rational comparisons, so no
// floating-point rounding can perturb tie breaking. The result always
// satisfies 0 <= alloc_i <= cap_i and sum(alloc_i) == R when feasible.
//
// # Failure-domain constraints
//
// AllocateWithSpread applies a two-level apportionment: replicas are first
// split across Region or Zone domains under [minReplicas, maxReplicas] bounds
// or a minGroups coverage requirement, then each domain's share is split
// across its member clusters. Domain weight and capacity are the sums of the
// members, keeping both levels weight-consistent.
//
// # KeepExisting minimal migration
//
// Rebalance / RebalanceWithSpread freeze every existing replica that still
// fits the new caps and constraints. Evictions occur only when:
//
//   - a cluster capacity shrank below its current load;
//   - a ReplicasRange domain exceeds maxReplicas;
//   - a minGroups / minReplicas rule forces coverage of another domain;
//   - an aggregate downscale (R decreased) removes replicas.
//
// Freed replicas enter a pool that is re-placed before any brand-new replica.
//
// Minimal-migration argument. Let E(x) = sum_i max(0, existing_i - x_i) be
// the eviction count of a candidate plan x. Each cap shrink alone forces
// sum_i max(0, existing_i - cap_i) evictions; each domain upper-bound breach
// forces the excess above the bound; each downscale to R forces at least
// sum(existing) - R removals; and each newly required domain forces at least
// one replica to leave its current domain. These lower bounds are
// independent, so any feasible plan must evict at least their sum. The
// algorithm performs exactly those forced evictions and never evicts a
// replica that a constraint did not require to move, hence its eviction count
// equals the lower bound and is the global minimum. The accompanying
// brute-force tests enumerate every feasible capped plan on small instances
// and assert equality with this minimum.
//
// # Insufficient capacity
//
// When sum(cap_i) < R every public entry point returns
// *InsufficientCapacityError (matched by errors.Is(err,
// ErrInsufficientCapacity)) carrying Requested, Capacity, Deficit and the
// unique maximum-sum MaxPlan that saturates every cluster.
package fedscheduler
