// Package errsan provides error sanitization for the auth service error
// chain.
//
// Internal errors form a graph that may contain cycles and shared nodes.
// Each node carries a public error code, a private message, optional
// parameters and a cause list. Sanitize folds the internal graph into a
// single PublicError that only exposes registry-allowed codes and typed
// parameters declared per code; everything else maps to a unified failure.
package errsan

// Node is an internal error graph node. The private message and the graph
// structure are never visible in external output.
type Node struct {
	code   string
	msg    string // private message, never leaves the process
	params map[string]any
	causes []*Node
}

// NewNode builds an internal error node. msg is private; params are
// candidate public parameters: only whitelisted keys with strictly
// matching types survive sanitization.
func NewNode(code, msg string, params map[string]any, causes ...*Node) *Node {
	n := &Node{code: code, msg: msg, causes: causes}
	if len(params) > 0 {
		n.params = make(map[string]any, len(params))
		for k, v := range params {
			n.params[k] = v
		}
	}
	return n
}

// AddCause appends cause nodes; cycles and shared subgraphs are allowed.
func (n *Node) AddCause(causes ...*Node) {
	n.causes = append(n.causes, causes...)
}

// Code returns the node error code (possibly unknown to the registry).
func (n *Node) Code() string { return n.code }

// Message returns the private message, for internal logs only.
func (n *Node) Message() string { return n.msg }

// Params returns a copy of the candidate parameters.
func (n *Node) Params() map[string]any {
	if len(n.params) == 0 {
		return nil
	}
	out := make(map[string]any, len(n.params))
	for k, v := range n.params {
		out[k] = v
	}
	return out
}

// Causes returns a copy of the cause list.
func (n *Node) Causes() []*Node {
	if len(n.causes) == 0 {
		return nil
	}
	out := make([]*Node, len(n.causes))
	copy(out, n.causes)
	return out
}
