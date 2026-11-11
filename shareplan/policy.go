package shareplan

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrEmptyPolicy is returned for a nil policy root.
	ErrEmptyPolicy = errors.New("shareplan: policy tree is empty")
	// ErrInvalidThreshold is returned when a node threshold is outside
	// [1, number of children].
	ErrInvalidThreshold = errors.New("shareplan: threshold must be in [1, child count]")
	// ErrMalformedNode is returned when a node mixes leaf and internal
	// fields or names an empty participant.
	ErrMalformedNode = errors.New("shareplan: malformed policy node")
	// ErrTooManyParticipants is returned when a policy names more than
	// MaxParticipants distinct participants.
	ErrTooManyParticipants = fmt.Errorf("shareplan: more than %d participants", MaxParticipants)
	// ErrPolicyTooLarge is returned when a policy has too many nodes or is
	// too deeply nested.
	ErrPolicyTooLarge = errors.New("shareplan: policy tree too large or too deep")
)

const maxDepth = 32

// Node is one node of a nested-threshold policy.
//
// A leaf node sets Participant and leaves Children empty. An internal node
// sets Threshold and Children; Threshold of its children must be satisfied.
type Node struct {
	// Threshold is t for a t-of-n internal node. Ignored for leaves.
	Threshold int
	// Children are the n children of an internal node.
	Children []*Node
	// Participant names the leaf holder. Empty means internal node.
	Participant string
}

// IsLeaf reports whether the node is a leaf.
func (n *Node) IsLeaf() bool {
	return len(n.Children) == 0
}

type leafInfo struct {
	participant string
	nodeID      string
}

type nodeMeta struct {
	node      *Node
	parentID  string
	threshold int
	childIDs  []string
	leaf      *leafInfo
}

// Policy is a validated, depth-first index of a policy tree. It is returned
// by Validate and consumed internally by Split.
type Policy struct {
	nodes        map[string]*nodeMeta
	leaves       []string // leaf node IDs in DFS order
	participants []string
}

// Validate checks the structural rules of a policy tree and returns its
// depth-first index. Every internal node must satisfy
// 1 <= Threshold <= len(Children); leaves must name a participant and hold
// no children. Distinct participants must not exceed MaxParticipants.
func Validate(root *Node) (*Policy, error) {
	if root == nil {
		return nil, ErrEmptyPolicy
	}
	idx := &Policy{nodes: make(map[string]*nodeMeta)}
	participants := make(map[string]struct{})
	nodeCount := 0

	var walk func(node *Node, parentID, nodeID string, depth int) error
	walk = func(node *Node, parentID, nodeID string, depth int) error {
		if node == nil {
			return ErrEmptyPolicy
		}
		nodeCount++
		if nodeCount > maxNodes || depth > maxDepth {
			return ErrPolicyTooLarge
		}

		meta := &nodeMeta{node: node, parentID: parentID}
		if node.IsLeaf() {
			if node.Participant == "" {
				return ErrMalformedNode
			}
			meta.leaf = &leafInfo{participant: node.Participant, nodeID: nodeID}
			participants[node.Participant] = struct{}{}
			if len(participants) > MaxParticipants {
				return ErrTooManyParticipants
			}
			idx.leaves = append(idx.leaves, nodeID)
		} else {
			if node.Participant != "" {
				return ErrMalformedNode
			}
			if node.Threshold < 1 || node.Threshold > len(node.Children) {
				return ErrInvalidThreshold
			}
			meta.threshold = node.Threshold
			for i, child := range node.Children {
				childID := fmt.Sprintf("%s/c%d", nodeID, i)
				meta.childIDs = append(meta.childIDs, childID)
				if err := walk(child, nodeID, childID, depth+1); err != nil {
					return err
				}
			}
		}
		idx.nodes[nodeID] = meta
		return nil
	}

	if err := walk(root, "", "root", 0); err != nil {
		return nil, err
	}

	idx.participants = make([]string, 0, len(participants))
	for name := range participants {
		idx.participants = append(idx.participants, name)
	}
	sort.Strings(idx.participants)
	return idx, nil
}

// Participants returns the distinct, sorted participant names of the policy.
func (idx *Policy) Participants() []string {
	out := make([]string, len(idx.participants))
	copy(out, idx.participants)
	return out
}

// leafShareIDs maps every leaf node ID to the independently identified
// share handed to that leaf occurrence.
func (idx *Policy) leafShareIDs() map[string]string {
	out := make(map[string]string, len(idx.leaves))
	for i, nodeID := range idx.leaves {
		out[nodeID] = fmt.Sprintf("leaf-%d", i)
	}
	return out
}
