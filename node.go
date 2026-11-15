package netsec

// Node is an internal error in the authentication service's error graph.
//
// Nodes may form an arbitrary directed graph: cause cycles and shared
// (diamond) substructures are explicitly permitted and must be tolerated
// by the sanitizer. The message and any unregistered parameters are
// private and must never reach external output.
type Node struct {
	code   string
	msg    string
	params map[string]any
	causes []*Node
}

// NewNode builds an internal error node. params may be nil; causes may
// be empty. The graph rooted at the returned node may contain cycles.
func NewNode(code, msg string, params map[string]any, causes ...*Node) *Node {
	return &Node{code: code, msg: msg, params: params, causes: causes}
}

// AddCause appends a cause edge. It may be used to construct cycles.
func (n *Node) AddCause(causes ...*Node) {
	n.causes = append(n.causes, causes...)
}

// Code returns the node's public error code. The code is not secret, but
// it is only externally visible if the registry allows it.
func (n *Node) Code() string { return n.code }
