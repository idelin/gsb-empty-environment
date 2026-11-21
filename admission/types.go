package admission

// Batch is an anonymous row from either department's de-identified table.
// Only an anonymous identifier and its synthetic sensitive category are
// known; there is no individual identifier.
type Batch struct {
	// ID is the anonymous batch identifier and must be globally unique.
	ID string
	// Side is the department side: "L" or "R".
	Side string
	// Category is the synthetic sensitive category attached to the batch.
	Category string
}

// Edge is a candidate pairing between one L batch and one R batch.
// JointCategory is the joint sensitive category of the pair; it may be
// coarser than the pair of per-side synthetic categories.
type Edge struct {
	L             string
	R             string
	JointCategory string
}

// DecisionGroup declares that every edge in MemberEdges must be accepted or
// rejected together as one decision. Members must be vertex-disjoint so that
// accepting a group never uses one batch twice. An edge not listed in any
// group behaves as a singleton group.
type DecisionGroup struct {
	// ID must be non-empty and unique across groups.
	ID string
	// MemberEdges are "L|R" edge keys.
	MemberEdges []string
}

// Quota is an aggregate upper bound on the number of accepted pairs whose
// joint category equals Category. Categories without an entry are unlimited.
// Total, when positive, bounds the total number of accepted pairs.
type Quota struct {
	// MaxPerCategory maps joint category to maximum count.
	MaxPerCategory map[string]int
	// Total, when greater than zero, caps the total number of pairs.
	Total int
}

// Block is one small piece of the untrusted decomposition, identified by the
// edges it owns. Two blocks may only be connected through shared batches.
type Block struct {
	// ID must be non-empty and unique across blocks.
	ID string
	// EdgeKeys are the "L|R" keys of edges owned by this block, in any order.
	EdgeKeys []string
}

// Decomposition is the untrusted block decomposition attached to a request.
// Its legality is always re-checked by the component; it is never trusted.
type Decomposition struct {
	Blocks []Block
}

// PublishRequest is one table release presented to the gateway.
type PublishRequest struct {
	Batches []Batch
	Edges   []Edge
	Groups  []DecisionGroup
	Quota   Quota
	// ProtectedBatches are the batches whose joint-category ambiguity must
	// meet PrivacyThreshold.
	ProtectedBatches []string
	// PrivacyThreshold is the minimum number of possible joint categories;
	// defaults to 2 when zero.
	PrivacyThreshold int
	// Decomposition is optional. When nil the whole problem is treated as a
	// single block, which is only efficient for small inputs.
	Decomposition *Decomposition
}

// BatchReport describes one protected batch. It never reveals which batches
// are paired with it, only how many categories remain possible.
type BatchReport struct {
	BatchID string
	// PossibleJointCategories is the number of distinct joint categories
	// compatible with at least one globally feasible complete pairing.
	PossibleJointCategories int
	// PossibleCounterpartCategories is the number of distinct synthetic
	// categories possible on the opposite side of the pairing. This supports
	// comparing joint-category determinacy against per-column determinacy
	// without exposing any individual correspondence.
	PossibleCounterpartCategories int
}

// Report is the admission result for a problem (one release or the merged
// state of a gateway).
type Report struct {
	Decision Decision
	// Reason is set for every non-publishable decision.
	Reason string
	// Threshold is the effective privacy threshold used.
	Threshold int
	// Batches holds one entry per protected batch when a feasible pairing
	// exists and is empty for the infeasible decision.
	Batches []BatchReport
}

// Options controls safety limits of the solver.
type Options struct {
	// MaxEnumerationGroups caps how many decision groups a single block may
	// contain before enumeration is refused. It is a safety valve against
	// malformed decompositions hiding a huge global problem in one block;
	// legitimate small blocks stay far below it. Defaults to 32.
	MaxEnumerationGroups int
}

// DefaultOptions returns the solver options used when none are supplied.
func DefaultOptions() Options {
	return Options{MaxEnumerationGroups: 32}
}

// edgeKey renders the canonical key of an edge.
func edgeKey(e Edge) string {
	return e.L + "|" + e.R
}
