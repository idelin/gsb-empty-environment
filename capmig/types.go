// Package capmig implements multi-hop transactional capability migration and
// minimum re-authentication solving for a distributed permission message bus.
//
// The solver models every confirmation-lost handoff as one global Boolean
// variable (at most 18, hence at most 2^18 possible histories) and enumerates
// every history jointly: the same assignment is used for all derived tickets,
// so one ambiguous handoff can never be resolved one way for one child ticket
// and the opposite way for another.
package capmig

// Input is the signed migration evidence collected during the handover window.
type Input struct {
	// BarrierEpoch is the global revocation-epoch barrier. Any transfer
	// receipt whose receipt epoch is older than the barrier is rejected as
	// inadmissible late evidence.
	BarrierEpoch uint64

	// AliasBindings resolve cross-namespace aliases to physical object IDs.
	AliasBindings []AliasBinding

	// ResourceMaps are many-to-many old-object to new-object edges.
	ResourceMaps []ResourceMapEdge

	// ActionSplits declare coarse actions that split into mutually
	// non-entailing fine-grained actions.
	ActionSplits []ActionSplit

	// Grants are signed genesis grants, one record per transfer token.
	Grants []Grant

	// Revocations are signed revocation records keyed by token.
	Revocations []Revocation

	// Handoffs are multi-hop transfer delegation receipts in causal order.
	Handoffs []Handoff

	// Tickets are migration requests, including derived child tickets that
	// may share upstream ancestor tokens.
	Tickets []Ticket
}

// AliasBinding binds an alias to the physical object it denotes.
type AliasBinding struct {
	Alias      string
	PhysicalID string
}

// ResourceMapEdge is one old-to-new object mapping edge.
type ResourceMapEdge struct {
	OldObject string
	NewObject string
}

// ActionSplit declares that OldAction expands into NewActions. The fine
// actions are siblings and never entail one another.
type ActionSplit struct {
	OldAction  string
	NewActions []string
}

// Grant is the signed genesis capability bound to a transfer token.
type Grant struct {
	Token   string
	Subject string
	Object  string
	Action  string
	Epoch   uint64
}

// Revocation voids a token at the given epoch.
type Revocation struct {
	Token string
	Epoch uint64
}

// Handoff is one transfer-delegation hop.
type Handoff struct {
	ID string

	Token string
	From  string
	To    string

	// Epoch is the causal epoch of the transfer event.
	Epoch uint64

	// ReceiptEpoch is the epoch carried by the receipt. When zero it
	// defaults to Epoch. Receipts older than the barrier are rejected.
	ReceiptEpoch uint64

	// LostConfirmation marks an ambiguous handoff: the transfer either
	// completed or never happened, producing one Boolean history variable.
	LostConfirmation bool
}

// Ticket is one migration request for a target physical object and the full
// set of post-split actions the subject claims.
type Ticket struct {
	ID      string
	Subject string

	NewObject  string
	NewActions []string

	// AncestorTokens are the transfer tokens this derivation rests on.
	AncestorTokens []string

	// DependencyHandoffs optionally pins handoff IDs whose ambiguity the
	// derivation explicitly depends on. Every pinned handoff must touch one
	// of the ancestor tokens.
	DependencyHandoffs []string
}

// Decision classifies a ticket.
type Decision string

const (
	// DecisionMigrate means the claim holds in every admissible history.
	DecisionMigrate Decision = "migrate"
	// DecisionReauth means the claim holds in some but not all histories.
	DecisionReauth Decision = "reauth"
	// DecisionDeny means no admissible history supports the claim.
	DecisionDeny Decision = "deny"
)

// Result is the fully auditable solver output.
type Result struct {
	BarrierEpoch        uint64
	TotalHistories      uint64
	AdmissibleHistories uint64

	// Inconsistent is true when no history honours every confirmed receipt.
	Inconsistent bool

	ExclusivityHeld      bool
	JointConsistencyHeld bool

	AmbiguousVariables    []VariableInfo
	RejectedReceipts      []RejectedReceipt
	AliasTable            []AliasResolution
	MinimumReauthSubjects []string
	TicketReports         []TicketReport

	SolveMillis int64
}

// VariableInfo describes one global ambiguous-handoff Boolean variable.
type VariableInfo struct {
	Index     int
	HandoffID string
	Token     string
	From      string
	To        string
	// Effective is false when flipping the variable never changes any
	// admissible state, i.e. the ambiguity is causally dead.
	Effective bool
}

// RejectedReceipt records a transfer receipt refused by the epoch barrier.
type RejectedReceipt struct {
	HandoffID    string
	Token        string
	ReceiptEpoch uint64
	BarrierEpoch uint64
}

// AliasResolution exposes how an alias was collapsed to a physical object.
type AliasResolution struct {
	Alias      string
	PhysicalID string
}

// RequirementBasis records why one (object, action) requirement is or is not
// covered in every admissible history.
type RequirementBasis struct {
	PhysicalObject string
	Action         string
	CoveringTokens []TokenCoverage
	// Unsupported is true when no ancestor token can ever cover the pair.
	Unsupported bool
}

// TokenCoverage explains how one token reaches a target object/action pair.
type TokenCoverage struct {
	Token       string
	ObjectPath  []string
	ActionPath  []string
	Subject     string
	HolderInAll bool
	AliveInAll  bool
}

// ChainHop summarises one hop of a token's multi-hop delegation chain.
type ChainHop struct {
	HandoffID         string
	From              string
	To                string
	Epoch             uint64
	LostConfirmation  bool
	RejectedByBarrier bool
	// SuccessHistories is the number of admissible histories in which the
	// hop actually transfers the exclusive handle.
	SuccessHistories uint64
}

// Counterexample is one admissible history where a ticket fails, carrying the
// global variable assignment that produces it.
type Counterexample struct {
	Mask            uint64
	VariableChoices map[string]bool
	FailedReason    string
}

// TicketReport is the per-ticket audit record.
type TicketReport struct {
	ID       string
	Subject  string
	Decision Decision

	TargetPhysical string
	TargetActions  []string

	AdmissibleHistories uint64
	SupportingHistories uint64

	Requirements     []RequirementBasis
	Chain            map[string][]ChainHop
	ReauthCandidates []string

	TouchesRevocation  []string
	BarrierRejects     []string
	CausalFailures     []string
	EffectiveVariables []string

	Counterexamples []Counterexample
}
