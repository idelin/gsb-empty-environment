package netsec

import (
	"errors"
	"sort"
)

// MaxGraphNodes is the maximum number of nodes accepted in one wire graph.
const MaxGraphNodes = 1024

var (
	ErrEmptyGraph             = errors.New("netsec: empty graph")
	ErrGraphTooLarge          = errors.New("netsec: graph exceeds node limit")
	ErrInvalidNodeKind        = errors.New("netsec: invalid node kind")
	ErrInvalidNodeReference   = errors.New("netsec: invalid node reference")
	ErrInvalidObjectReference = errors.New("netsec: invalid object reference")
	ErrMissingCapabilityToken = errors.New("netsec: empty capability token")
)

// NodeID addresses one node in a wire Graph. Node IDs are implicit slice
// indexes, which means aliases are explicit and cannot be forged by names.
type NodeID uint32

// Kind identifies a wire node type.
type Kind uint8

const (
	KindString Kind = iota + 1
	KindArray
	KindDict
	KindObjectRef
	KindCapabilityRef
)

// Aliases for callers that prefer node-prefixed names.
const (
	StringNode        = KindString
	ArrayNode         = KindArray
	DictNode          = KindDict
	ObjectRefNode     = KindObjectRef
	CapabilityRefNode = KindCapabilityRef
)

// Node is one node in the attacker-controlled wire graph.
//
// String uses String. Array uses Array. Dict uses Dict. ObjectRef uses Target,
// and may target a String, Array, or Dict after any chain of ObjectRefs.
// CapabilityRef uses Token. Fields not used by a node's kind are ignored.
type Node struct {
	Kind   Kind
	String string
	Array  []NodeID
	Dict   map[string]NodeID
	Target NodeID
	Token  string
}

// Graph is a bounded, index-addressed wire graph. Sharing and cycles are
// represented by repeated node IDs and are preserved in the decoded Value.
type Graph struct {
	Root  NodeID
	Nodes []Node
}

// Value is the decoded object graph. String, Array, and Dict nodes have a
// stable pointer per input object node. ObjectRef nodes resolve directly to
// their target, and repeated capability tokens resolve to one shared Value and
// one shared Capability.
//
// Capability values contain no bearer token. They are intentionally terminal:
// no other object is traversed through a capability.
type Value struct {
	Kind       Kind
	String     string
	Array      []*Value
	Dict       map[string]*Value
	Capability *Capability
}

func (g Graph) validate() error {
	if len(g.Nodes) == 0 {
		return ErrEmptyGraph
	}
	if len(g.Nodes) > MaxGraphNodes {
		return ErrGraphTooLarge
	}
	if !g.validID(g.Root) {
		return ErrInvalidNodeReference
	}

	for _, node := range g.Nodes {
		switch node.Kind {
		case KindString:
		case KindArray:
			for _, child := range node.Array {
				if !g.validID(child) {
					return ErrInvalidNodeReference
				}
			}
		case KindDict:
			for _, child := range node.Dict {
				if !g.validID(child) {
					return ErrInvalidNodeReference
				}
			}
		case KindObjectRef:
			if !g.validID(node.Target) {
				return ErrInvalidNodeReference
			}
			if err := g.validateObjectReference(node.Target); err != nil {
				return err
			}
		case KindCapabilityRef:
			if node.Token == "" {
				return ErrMissingCapabilityToken
			}
		default:
			return ErrInvalidNodeKind
		}
	}
	return nil
}

func (g Graph) validID(id NodeID) bool {
	return uint64(id) < uint64(len(g.Nodes))
}

func (g Graph) validateObjectReference(target NodeID) error {
	seen := make([]bool, len(g.Nodes))
	current := target
	for {
		if !g.validID(current) {
			return ErrInvalidNodeReference
		}
		if seen[current] {
			return ErrInvalidObjectReference
		}
		seen[current] = true

		switch g.Nodes[current].Kind {
		case KindString, KindArray, KindDict:
			return nil
		case KindObjectRef:
			current = g.Nodes[current].Target
		default:
			return ErrInvalidObjectReference
		}
	}
}

// reachableTokens returns the distinct capability tokens reachable from the
// root. The graph has already been validated. Unreachable nodes are validated
// but do not cause capabilities to be leased.
func (g Graph) reachableTokens() []string {
	seen := make([]bool, len(g.Nodes))
	pending := []NodeID{g.Root}
	tokenSet := make(map[string]struct{})

	for len(pending) > 0 {
		last := len(pending) - 1
		id := pending[last]
		pending = pending[:last]
		if seen[id] {
			continue
		}
		seen[id] = true

		node := g.Nodes[id]
		switch node.Kind {
		case KindArray:
			pending = append(pending, node.Array...)
		case KindDict:
			for _, child := range node.Dict {
				pending = append(pending, child)
			}
		case KindObjectRef:
			pending = append(pending, node.Target)
		case KindCapabilityRef:
			tokenSet[node.Token] = struct{}{}
		}
	}

	tokens := make([]string, 0, len(tokenSet))
	for token := range tokenSet {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}
