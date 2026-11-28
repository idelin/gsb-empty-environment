package fedscheduler

// Cluster describes a single member cluster that may receive replicas.
type Cluster struct {
	// Name is the unique cluster identifier. Ties in the largest-remainder
	// method and every other deterministic choice are broken by ascending
	// lexicographic Name order.
	Name string

	// Region is the top-level failure domain (for example a geographic
	// region). Required when Region-scoped spread constraints are used.
	Region string

	// Zone is the nested failure domain. Required when Zone-scoped spread
	// constraints are used.
	Zone string

	// Weight is the static scheduling weight; it must be strictly positive.
	// Relative weights determine each cluster's (or domain's) fair share.
	Weight int64

	// Capacity is the hard upper bound alloc <= Capacity, derived on the
	// control plane as floor(min(freeCPU/cpuPerReplica, freeMem/memPerReplica)).
	Capacity int64
}

// SpreadMode selects the semantics of a SpreadConstraint.
type SpreadMode string

const (
	// SpreadReplicasRange limits every single failure domain to the closed
	// interval [MinReplicas, MaxReplicas].
	SpreadReplicasRange SpreadMode = "ReplicasRange"

	// SpreadMinGroups requires replicas to be spread across at least
	// MinGroups distinct failure domains (each used domain receives at least
	// one replica).
	SpreadMinGroups SpreadMode = "MinGroups"
)

// SpreadScope identifies the failure-domain level a constraint applies to.
type SpreadScope string

const (
	// ScopeRegion constrains per-Region placement. It is the default scope.
	ScopeRegion SpreadScope = "Region"

	// ScopeZone constrains per-Zone placement.
	ScopeZone SpreadScope = "Zone"
)

// SpreadConstraint expresses a cross failure-domain placement rule.
//
// For Mode == SpreadReplicasRange, MinReplicas and MaxReplicas bound the
// replica count carried by any single domain. For Mode == SpreadMinGroups,
// MinGroups bounds the number of distinct domains that must be used.
type SpreadConstraint struct {
	// Scope is Region (default) or Zone.
	Scope SpreadScope

	// Mode selects ReplicasRange or MinGroups semantics.
	Mode SpreadMode

	// MinReplicas is the per-domain lower bound in ReplicasRange mode.
	MinReplicas int64

	// MaxReplicas is the per-domain upper bound in ReplicasRange mode.
	// Zero is treated as "no upper bound".
	MaxReplicas int64

	// MinGroups is the minimum number of distinct used domains in
	// MinGroups mode.
	MinGroups int
}

// Allocation is the resolved per-cluster replica plan together with its
// placement diagnostics.
type Allocation struct {
	// Replicas maps cluster name to the allocated replica count.
	Replicas map[string]int64

	// Total equals the requested replica count R.
	Total int64

	// Saturated lists the names of clusters pinned to their hard capacity.
	Saturated []string

	// Rounds is the number of largest-remainder redistribution rounds the
	// recursive overflow algorithm required.
	Rounds int

	// Migrated is the number of evicted replicas
	// sum_i max(0, existing_i - new_i). It is zero for stateless calls and
	// populated by rebalancing calls.
	Migrated int64

	// Added is the number of newly placed replicas
	// sum_i max(0, new_i - existing_i), populated by rebalancing calls.
	Added int64
}

// RebalanceResult is a rebalancing outcome. It embeds the resulting
// Allocation and additionally exposes the previous distribution.
type RebalanceResult struct {
	Allocation

	// Previous maps cluster name to the previously existing replica count.
	Previous map[string]int64

	// MinEvictions is the theoretical minimum number of evictions forced by
	// the new caps and domain constraints. Migrated equals this value when
	// the request is feasible.
	MinEvictions int64
}

// RebalanceMode selects the rebalancing strategy.
type RebalanceMode string

const (
	// KeepExisting freezes every in-range existing replica and only evicts
	// what the new hard caps / spread constraints force, distributing the
	// remaining delta by weight. It minimizes the number of migrated
	// replicas.
	KeepExisting RebalanceMode = "KeepExisting"
)
