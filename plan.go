package destplan

// Plan is a feasible, minimum-cost set of deactivated wrapping edges.
type Plan struct {
	// DisabledGroups lists canonical edge IDs (the smallest ID of every
	// alias group that is deactivated), sorted lexicographically.
	DisabledGroups []string
	// DisabledAliases lists every edge ID and alias covered by the plan,
	// sorted lexicographically, so the host can action every alias.
	DisabledAliases []string
	// Cost is the sum of one group cost per deactivated group.
	Cost uint64
	// Certificate records the independent lower bound and enumeration size.
	Certificate OptimalityCertificate
}

// OptimalityCertificate lets an acceptor re-derive optimality without
// trusting the planner: every subset of the decision variables was examined.
type OptimalityCertificate struct {
	// Variables is the canonical ID of each alias group, in bit order.
	Variables []string
	// Subsets is 2^len(Variables), the number of edge subsets enumerated.
	Subsets uint64
	// LowerBound equals the cost of the returned plan; no cheaper feasible
	// subset exists.
	LowerBound uint64
	// FeasibleSubsets is the number of subsets satisfying every requirement.
	FeasibleSubsets uint64
}

// ConflictProof identifies two storage obligations that no edge decision can
// reconcile because they touch the same ciphertext block or data key.
type ConflictProof struct {
	Kind       string
	Key        string
	Block      Addr
	DestroyRef Requirement
	RetainRef  Requirement
}

// CutWitness shows that one inclusion-minimal set of edges killing every
// destroy key necessarily also kills a retain key.
type CutWitness struct {
	// DestroyCut is a canonical edge-ID set, sorted.
	DestroyCut []string
	// KilledRetain is a retain-required data key unreachable after the cut.
	KilledRetain string
}

// CouplingProof proves infeasibility by enumerating every inclusion-minimal
// destroy cut: each one severs at least one retain key.
type CouplingProof struct {
	// DestroyKeys are data keys backing destroy intervals.
	DestroyKeys []string
	// RetainKeys are data keys backing retain intervals.
	RetainKeys []string
	// Subsets is the number of edge subsets enumerated.
	Subsets uint64
	// MinimalCuts lists every inclusion-minimal destroy cut and the retain
	// key each one kills. Together they cover all feasible destroy cuts.
	MinimalCuts []CutWitness
}

// Infeasibility explains why no edge set can satisfy the request. Exactly
// one proof field is populated.
type Infeasibility struct {
	// Kind is "retain-unreachable", "key-conflict", "block-conflict" or
	// "cut-coupling".
	Kind           string
	Detail         string
	Conflict       *ConflictProof
	Coupling       *CouplingProof
	UnreachableKey string
}

// Result is returned by Solve. Exactly one of Plan or Infeasible is set.
type Result struct {
	Plan       *Plan
	Infeasible *Infeasibility
	// DestroyKeys and RetainKeys are the resolved data-key obligations, in
	// sorted order, useful for auditing.
	DestroyKeys []string
	RetainKeys  []string
}
