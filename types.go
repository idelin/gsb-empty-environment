package netsec

import "errors"

// GateKind selects how a node combines its preconditions.
//
// The zero value is GateAND so that a node with no explicit gate behaves as an
// AND gate: an empty AND gate is vacuously satisfied and models an initially
// available action (a fact), while an empty OR gate is never satisfied.
type GateKind int

const (
	// GateAND requires every precondition to be reachable before the node
	// itself becomes reachable.
	GateAND GateKind = iota
	// GateOR requires at least one precondition to be reachable before the
	// node itself becomes reachable.
	GateOR
)

// Node is one attack step (action) of the dependency graph.
//
// The node is reachable exactly when its gate is satisfied by the currently
// reachable precondition nodes. Preconditions are nodes, so a shared
// precondition used by several gates is a single fact established once by the
// reachability fixpoint rather than a collection of independent paths.
type Node struct {
	// ID is the unique action identifier referenced by preconditions and
	// patches.
	ID string
	// Gate is GateAND or GateOR.
	Gate GateKind
	// Preconditions lists the IDs of the nodes that feed the gate.
	Preconditions []string
}

// Patch is a candidate remediation that disables one action when selected.
// Several patches may disable the same action; selecting any of them removes
// that action from the graph.
type Patch struct {
	// ID is the unique patch identifier used for deterministic tie breaks.
	ID string
	// Action is the ID of the node disabled by this patch.
	Action string
	// Cost is the non-negative integer cost of deploying the patch.
	Cost int
}

// Problem is the patch-selection instance handed to the decision service.
type Problem struct {
	// Nodes describes the full attack dependency graph.
	Nodes []Node
	// Target is the ID of the node whose reachability must be cut.
	Target string
	// Patches are the candidate remediations; at most twenty are allowed.
	Patches []Patch
}

// Reason explains why the target stays unreachable under the selected patches.
type Reason struct {
	// DisabledActions are the actions disabled by the selected patches, in
	// ascending node-index order.
	DisabledActions []string
	// ReachableClosure is every node reachable after the disabled actions are
	// removed. Target is guaranteed not to appear in this list.
	ReachableClosure []string
}

// Witness is a concrete activation sequence proving the target is reachable
// once one selected patch is removed. Every entry is a node whose gate is
// satisfied by the entries preceding it, and the final entry is the target.
type Witness struct {
	// OmittedPatch is the selected patch whose removal makes the target
	// reachable again.
	OmittedPatch string
	// ActivationOrder is the ordered list of activated node IDs ending in the
	// target.
	ActivationOrder []string
}

// Proof bundles the blocking certificate and the per-omission reachability
// witnesses that establish inclusion-minimality of the selected set.
type Proof struct {
	// Target is the protected node ID (copied from the problem).
	Target string
	// Blocking demonstrates that the target is not reachable under the
	// selected patch set.
	Blocking Reason
	// Minimality contains one reachability witness per selected patch, proving
	// that dropping any single patch re-opens an attack path to the target.
	Minimality []Witness
}

// Solution is the exact answer produced by Solve.
type Solution struct {
	// Selected are the chosen patch IDs in ascending lexicographic order.
	Selected []string
	// Cost is the sum of the costs of the selected patches.
	Cost int
	// Proof certifies blocking and inclusion-minimality.
	Proof Proof
}

// Sentinel errors returned by Solve and Reachability.
var (
	// ErrNoNodes is returned when the problem contains no nodes.
	ErrNoNodes = errors.New("netsec: problem has no nodes")
	// ErrUnknownTarget is returned when the target ID is not a node.
	ErrUnknownTarget = errors.New("netsec: target is not a known node")
	// ErrTooManyPatches is returned when more than twenty patches are given.
	ErrTooManyPatches = errors.New("netsec: at most twenty patches are allowed")
	// ErrUnblockable is returned when even selecting every patch cannot make
	// the target unreachable.
	ErrUnblockable = errors.New("netsec: target cannot be blocked by any patch subset")
)
