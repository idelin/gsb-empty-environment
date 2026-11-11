// Package shortcircuit evaluates boolean security-gateway rule expressions
// with AND/OR/NOT short-circuit semantics and produces tamper-evident
// execution traces that an independent validator can replay without invoking
// any check.
//
// A trace binds, for every node that was actually visited, the node id, the
// visit order (sequence and occurrence for shared nodes), the returned value,
// the rule version and the evidence digest. Entries are linked with a hash
// chain; when the engine holds an Ed25519 key the trace is additionally sealed
// so that a re-chained forgery cannot be mistaken for a genuine execution.
package shortcircuit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// NodeKind identifies the concrete type of a node inside a trace entry.
type NodeKind string

const (
	KindCheck NodeKind = "check"
	KindAnd   NodeKind = "and"
	KindOr    NodeKind = "or"
	KindNot   NodeKind = "not"
)

// Status is the outcome recorded for a single node visit.
type Status string

const (
	StatusTrue      Status = "true"      // check evaluated to true
	StatusFalse     Status = "false"     // check evaluated to false
	StatusError     Status = "error"     // check returned an error or panicked
	StatusCanceled  Status = "cancel"    // evaluation stopped due to context cancellation
	StatusCompleted Status = "completed" // composite node finished normally
)

// RuleVersion pins the revision of the logic behind a check. It is bound into
// every check entry so that replaying an old trace against a changed rule is
// rejected.
type RuleVersion struct {
	Name    string
	Version string
}

// Evidence is the audit summary returned together with a check decision.
type Evidence struct {
	Summary string
	Data    string
}

func (e Evidence) digest() string {
	h := sha256.New()
	h.Write([]byte("evidence"))
	h.Write([]byte{0})
	h.Write([]byte(e.Summary))
	h.Write([]byte{0})
	h.Write([]byte(e.Data))
	return hex.EncodeToString(h.Sum(nil))
}

// CheckFunc executes one leaf check. A non-nil error aborts the whole
// evaluation and is recorded as an error (or cancellation) entry; no decision
// is produced for the run. Implementations should honor ctx cancellation.
type CheckFunc func(ctx context.Context) (decision bool, evidence Evidence, err error)

// Node is an element of the rule expression tree. The same node pointer may
// appear more than once in the tree (a shared node); each visit gets its own
// trace entry distinguished by occurrence number.
type Node interface {
	nodeID() string
}

// CheckNode is a leaf that runs a single cancellable check.
type CheckNode struct {
	ID   string
	Rule RuleVersion
	Fn   CheckFunc
}

// AndNode evaluates children left to right and short-circuits on the first
// false result.
type AndNode struct {
	ID       string
	Children []Node
}

// OrNode evaluates children left to right and short-circuits on the first
// true result.
type OrNode struct {
	ID       string
	Children []Node
}

// NotNode negates its single child.
type NotNode struct {
	ID    string
	Child Node
}

func (n *CheckNode) nodeID() string { return n.ID }
func (n *AndNode) nodeID() string   { return n.ID }
func (n *OrNode) nodeID() string    { return n.ID }
func (n *NotNode) nodeID() string   { return n.ID }

func kindOf(n Node) NodeKind {
	switch n.(type) {
	case *CheckNode:
		return KindCheck
	case *AndNode:
		return KindAnd
	case *OrNode:
		return KindOr
	case *NotNode:
		return KindNot
	default:
		return NodeKind("unknown")
	}
}
