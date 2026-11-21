package admission

import "math/big"

// Identity is a signed-commitment participant identity. It is an opaque public
// identifier; it must never contain key material.
type Identity string

// MessageType names a public protocol message.
type MessageType string

// ProbabilityTable maps each outcome label of a probabilistic draw to its
// exact rational probability. Tables must be non-empty, probabilities must be
// strictly positive, and they must sum to one.
type ProbabilityTable map[string]*big.Rat

// Participant declares a subject and the public partition that determines
// which message types the subject can observe before acting.
type Participant struct {
	// Subject is the signed-commitment identity of the participant.
	Subject Identity

	// VisibleMessages is the partition cell of the public history visible to
	// Subject: exactly the message types that may reach this subject. Every
	// message referenced by a decision of this subject must be listed here.
	VisibleMessages []MessageType

	// FailureDomain groups subjects whose choices are correlated by the
	// protocol (e.g. co-located services). Subjects sharing a non-empty
	// failure domain cannot choose independently: one host-supplied condition
	// governs the whole domain per public observation. The empty string
	// denotes an independent subject.
	FailureDomain string
}

// DrawSpec labels a probabilistic node.
type DrawSpec struct {
	// Name is a human-readable label of the random quantity being drawn.
	Name string

	// Table gives the outcome probabilities of this draw.
	Table ProbabilityTable

	// FixedVariable names a random quantity that, once drawn, stays fixed
	// across retries. Every fixed variable name may appear on exactly one
	// node in the model, which structurally prevents re-drawing it on retry.
	// Leave empty for a fresh draw that may recur in each attempt.
	FixedVariable string
}

// DecisionSpec labels a public admission decision point.
type DecisionSpec struct {
	// Subject is the participant that chooses here.
	Subject Identity

	// ObservedMessages is the public prefix visible to Subject at this node,
	// in canonical observation order. Two nodes carrying the same Subject and
	// the same ObservedMessages are indistinguishable to the subject and are
	// therefore governed by one decision variable, even when their hidden
	// states differ.
	ObservedMessages []MessageType

	// Actions are all publicly admissible choices, e.g. "publish" and
	// "abstain". Nodes sharing one decision key must declare identical
	// action lists.
	Actions []string

	// AbstainAction names the action declared by the public rules as an
	// abstention eligible for retry. It must be one of Actions.
	AbstainAction string
}

// TerminalSpec labels a terminal node.
type TerminalSpec struct {
	// Category is the audit category assigned on this terminal. The special
	// value FailureCategory marks an aborted, uncompleted round; all other
	// categories are successful audit categories that must appear in Model.Ideal.
	Category string
}

// FailureCategory is the terminal category of a round that cannot complete.
const FailureCategory = "admission.failure"

// Edge is a directed DAG edge.
type Edge struct {
	// To is the destination node ID.
	To string

	// OnAction is set on edges leaving decision nodes and names the action
	// that selects this edge. It must be empty on edges leaving draw,
	// retry-entry and terminal nodes.
	OnAction string

	// OnOutcome is set on edges leaving draw nodes and names the table
	// outcome that selects this edge. It must be empty elsewhere.
	OnOutcome string

	// Retry marks an abstention edge that consumes one retry and rejoins at
	// a retry-entry node.
	Retry bool
}

// NodeKind enumerates node kinds.
type NodeKind int

const (
	// KindEntry is a retry-entry node: the root attempt or a re-entry point
	// after a rule-allowed abstention. Entry nodes have exactly one edge.
	KindEntry NodeKind = iota
	// KindDraw is a probabilistic node.
	KindDraw
	// KindDecision is a public admission decision point.
	KindDecision
	// KindTerminal ends the round, either in an audit category or in failure.
	KindTerminal
)

// Node is one DAG vertex. Exactly one of Draw, Decision and Terminal is set
// according to Kind; entry nodes leave all three nil.
type Node struct {
	ID       string
	Kind     NodeKind
	Draw     *DrawSpec
	Decision *DecisionSpec
	Terminal *TerminalSpec
	Out      []Edge
}

// HostCondition is the explicit, host-supplied choice rule for one decision
// variable that belongs to a failure domain. The rule text is opaque to the
// package: it only has to be non-empty so the model records who fixed the
// condition. Independent subjects are enumerated freely and must not appear.
type HostCondition struct {
	// Domain is the failure domain this condition governs.
	Domain string

	// ObservedMessages identifies the public observation at which the domain
	// chooses.
	ObservedMessages []MessageType

	// Rule is the host's declared deterministic condition. It is carried into
	// the certificate digest but never interpreted by the package.
	Rule string

	// Action is the single action the domain takes at this observation.
	Action string
}

// Model is the complete, public description of one admission protocol.
type Model struct {
	// Participants are the signed-commitment subjects and their public
	// partitions.
	Participants []Participant

	// PublicMessages lists every message type used by the protocol, in a
	// stable order chosen by the host.
	PublicMessages []MessageType

	// Nodes is the round DAG. Node IDs must be unique.
	Nodes []Node

	// Root is the ID of the root retry-entry node.
	Root string

	// Ideal is the ideal allocation: for every successful audit category, the
	// exact rational share that completed rounds would target absent selective
	// abstention. Shares must be non-negative and sum to one.
	Ideal map[string]*big.Rat

	// MaxRetries is the publicly declared retry limit. Abstained rounds are
	// retried only when the DAG models a retry edge; no path may consume
	// more than MaxRetries retries, and MaxRetries must not exceed 2.
	MaxRetries int

	// HostConditions fixes the choice of every failure-domain decision
	// variable. Independent subjects must not appear here.
	HostConditions []HostCondition
}
