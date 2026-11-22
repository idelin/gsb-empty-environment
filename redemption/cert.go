package redemption

// PruneReason explains why a search subtree was discarded without further
// branching. The certificate verifier independently re-checks each reason.
type PruneReason string

const (
	// PruneBound: every completion of the node is provably no better than
	// the incumbent (relaxed upper bound < incumbent value, or equal value
	// with an already lexicographically better incumbent).
	PruneBound PruneReason = "bound"
	// PrunePrefix: the decision to include candidate Index conflicts with
	// the confirmed prefix, so that branch has no completion.
	PrunePrefix PruneReason = "prefix_conflict"
	// PruneDependency: including Index forces an excluded predecessor
	// (closure cannot be satisfied), so that branch has no completion.
	PruneDependency PruneReason = "missing_dependency"
	// PruneConflict: including Index reuses a nullifier/outpoint already
	// spent in the current selection or exceeds a remaining quota.
	PruneConflict PruneReason = "infeasible"
	// PruneInfeasibleLeaf: a fully decided mask fails the global feasibility
	// check (missing predecessor closure or a dependency cycle).
	PruneInfeasibleLeaf PruneReason = "infeasible_leaf"
)

// CertNode is one node of the branch-and-bound search tree. The tree is
// serialized in pre-order; node 0 is the root.
type CertNode struct {
	Parent int `json:"parent"`
	// Depth is the number of candidates already decided at this node.
	Depth int `json:"depth"`
	// Index is the candidate decided at this node (-1 for the root).
	Index int `json:"index"`
	// Included is the decision taken on Index at this node.
	Included bool `json:"included"`
	// Mask is the selection of canonical candidate indices fixed at this node.
	Mask uint32 `json:"mask"`
	// Pruned != "" means the subtree ends here with the stated reason.
	Pruned PruneReason `json:"pruned,omitempty"`
	// UpperBound is the relaxed maximum value attainable by any completion
	// (recorded on bound-pruned nodes and leaves).
	UpperBound int64 `json:"upper_bound"`
}

// Certificate is the independently checkable optimality artifact. It contains
// no signature or proof material: those are verified separately through the
// host-injected verifiers.
type Certificate struct {
	Digest       string     `json:"digest"`
	NCandidates  int        `json:"n_candidates"`
	OptimalMask  uint32     `json:"optimal_mask"`
	OptimalValue int64      `json:"optimal_value"`
	Nodes        []CertNode `json:"nodes"`
}
