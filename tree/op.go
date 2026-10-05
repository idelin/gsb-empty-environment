// Package tree implements a deterministic merger for an offline file tree.
//
// Semantics: total-order replay. The merge result of a set of operations is
// the union of all operations sorted by (logical time, op ID) and replayed
// from the empty tree. Operations that cannot be applied at replay time are
// recorded as rejected (with a reason) and kept in the output. There is no
// incremental hiding of late-arriving early operations: any new operation
// triggers a full deterministic re-replay, so the same operation set always
// yields the same parent mapping and rejection list regardless of batching
// or arrival order.
package tree

import "fmt"

// RootID is the stable ID of the immovable, undeletable root node.
const RootID = "root"

// OpKind enumerates the supported operations.
type OpKind int

const (
	OpCreate OpKind = iota // create Node under Parent with Name
	OpMove                 // move Node under Parent
	OpRename               // rename Node to Name (ID unchanged)
	OpDelete               // mark Node deleted (visibility marker)
)

func (k OpKind) String() string {
	switch k {
	case OpCreate:
		return "create"
	case OpMove:
		return "move"
	case OpRename:
		return "rename"
	case OpDelete:
		return "delete"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Op is a single tree operation. ID and Time form the total order:
// operations sort by (Time, ID). Node is the target node; Parent is the
// new parent for create/move; Name is the name for create/rename.
type Op struct {
	ID     string
	Time   int64
	Kind   OpKind
	Node   string
	Parent string
	Name   string
}

func (o Op) String() string {
	return fmt.Sprintf("op{%s t=%d %s node=%s parent=%s name=%q}",
		o.ID, o.Time, o.Kind, o.Node, o.Parent, o.Name)
}

// RejectReason explains why an operation was rejected during replay.
type RejectReason string

const (
	// ReasonDuplicateOpID is recorded for an operation whose ID collides
	// with a different operation payload. The canonically smaller payload
	// (by Time, Kind, Node, Parent, Name) wins; the loser is rejected.
	ReasonDuplicateOpID RejectReason = "duplicate-op-id-conflict"
	ReasonNodeExists    RejectReason = "node-already-exists"
	ReasonNodeMissing   RejectReason = "node-missing"
	ReasonParentMissing RejectReason = "parent-missing"
	// ReasonParentHidden is recorded when the target parent exists but is
	// hidden at replay time (itself deleted or under a deleted ancestor).
	ReasonParentHidden    RejectReason = "parent-hidden"
	ReasonRootImmovable   RejectReason = "root-immovable"
	ReasonRootUndeletable RejectReason = "root-undeletable"
	ReasonTargetIsSelf    RejectReason = "target-is-self"
	ReasonTargetIsDesc    RejectReason = "target-is-descendant"
)

// Rejection is a rejected operation kept alongside its reason.
type Rejection struct {
	Op     Op
	Reason RejectReason
}

func (r Rejection) String() string {
	return fmt.Sprintf("rejected[%s]: %s", r.Reason, r.Op)
}
