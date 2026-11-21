// Package admission implements a privacy pre-screening admission component for
// the Go release gateway.
//
// Two departments release de-identified tables. Each table is described only by
// synthetic sensitive categories, aggregate quotas and anonymous batch
// identifiers; no individual correspondence is ever part of the input or the
// output. A release proposes which anonymous batches may be paired between the
// two departments and which groups of candidate pairs must share the same
// accept/reject decision.
//
// For every protected batch the component counts how many distinct joint
// sensitive categories remain possible across ALL complete pairings that
// satisfy every quota and shared-decision constraint. The count is computed
// from globally feasible pairings only; the number of locally incident
// candidate edges is never used as a substitute. Releases are classified into
// three separately reported decisions: no feasible pairing, below the privacy
// threshold, or publishable.
//
// An untrusted block decomposition may be supplied. Each block is connected to
// other blocks through at most four shared anonymous batches. The component
// independently verifies that the decomposition is legal (coverage, edge
// ownership, group containment and a tree of blocks with separators of size at
// most four) and then performs tree dynamic programming that reuses boundary
// states, so a thousand-row problem never requires enumerating global
// pairings.
package admission

// Decision is the admission verdict for one release.
type Decision string

const (
	// DecisionInfeasible means no complete pairing satisfies all constraints.
	DecisionInfeasible Decision = "infeasible"
	// DecisionBelowThreshold means feasible pairings exist, but at least one
	// protected batch has fewer than PrivacyThreshold possible joint
	// categories, so releasing would narrow the joint category too far.
	DecisionBelowThreshold Decision = "below_threshold"
	// DecisionPublishable means every protected batch keeps at least
	// PrivacyThreshold possible joint categories.
	DecisionPublishable Decision = "publishable"
)
