package netsec

// Request describes one signing request that must be placed on exactly one
// isolation domain.
type Request struct {
	// ID is the unique request identifier.
	ID string
	// TenantID groups requests belonging to the same tenant.
	TenantID string
	// Sensitive marks a tenant request that carries an approval role. Two
	// sensitive requests of the same TenantID with non-empty ApprovalRole
	// values are forced onto different domains.
	Sensitive bool
	// ApprovalRole is the approval role carried by a sensitive request.
	ApprovalRole string
	// KeyPurpose is the shared-key purpose required by the request. A domain
	// only serves the request when it advertises capacity for this purpose.
	KeyPurpose string
	// Capacity is the number of capacity units consumed on the chosen
	// domain's KeyPurpose pool. It must be positive.
	Capacity int
}

// Domain describes a hardware isolation domain and the capacities it
// advertises per shared-key purpose.
type Domain struct {
	// ID is the unique domain identifier.
	ID string
	// Purposes maps a key purpose to the available capacity units. A missing
	// purpose or a zero value means the domain does not support that purpose.
	Purposes map[string]int
}

// Pair is an unordered pair of request IDs.
type Pair struct {
	A string
	B string
}

// Input is the planning problem handed to the scheduling host.
//
// Cost holds the per-request latency cost of using each domain:
// Cost[requestIndex][domainIndex]. All values must be non-negative.
//
// MustSeparate lists explicit request incompatibility pairs on top of the
// approval-role separation rule. Both requests of every pair must land on
// different domains.
type Input struct {
	Requests     []Request
	Domains      []Domain
	MustSeparate []Pair
	Cost         [][]int
}

// Plan is an executable assignment produced by Solve.
type Plan struct {
	// Assignment maps each request index to the assigned domain index.
	Assignment []int
	// EnabledDomains is the sorted list of domain indices used by the plan.
	EnabledDomains []int
	// EnabledCount is len(EnabledDomains), the primary optimization objective.
	EnabledCount int
	// TotalCost is the summed latency cost, the secondary optimization
	// objective among plans with the minimum EnabledCount.
	TotalCost int
}

// PairConflict records one isolation constraint inside a conflict set.
type PairConflict struct {
	Pair   Pair
	Source string // "explicit" (from Input.MustSeparate) or "approval_role"
}

// ConflictReason is a reviewable fact explaining why a request subset cannot
// be placed.
type ConflictReason struct {
	// Kind is one of "unsupported_purpose", "purpose_capacity" or
	// "single_request_capacity".
	Kind string
	// Detail is a human-readable explanation.
	Detail string
	// RequestIDs are the request indices (IDs) involved in the reason.
	RequestIDs []string
	// DomainID is the domain referenced by a capacity reason, when applicable.
	DomainID string
	// Purpose is the key purpose referenced by the reason.
	Purpose string
	// Required is the demanded capacity.
	Required int
	// Available is the capacity actually offered.
	Available int
}

// ConflictSet is returned when no feasible assignment exists. RequestSubset
// is an inclusion-minimal infeasible subset: removing any single request from
// it makes the remaining problem feasible.
type ConflictSet struct {
	// RequestSubset holds the IDs of the minimal infeasible request subset.
	RequestSubset []string
	// PairConflicts lists the pairwise isolation constraints fully contained
	// in RequestSubset.
	PairConflicts []PairConflict
	// Reasons lists capacity and key-purpose facts that block placement of
	// RequestSubset.
	Reasons []ConflictReason
}
