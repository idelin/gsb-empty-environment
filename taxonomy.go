package netsec

import (
	"errors"
	"fmt"
)

// MaxQuasiIdentifiers is the maximum number of quasi-identifier columns an
// export may declare.
const MaxQuasiIdentifiers = 6

// MaxTreeHeight is the maximum number of parent edges between a leaf value
// and the root of a taxonomy (depth of the deepest node).
const MaxTreeHeight = 5

// Taxonomy is a rooted category tree for one quasi-identifier column.
//
// Nodes is the set of node identifiers (leaf values and internal categories).
// Parent maps every non-root node to its parent; it contains no entry for the
// unique root. Root is the unique node without a parent. Node depth counts
// edges toward the root, so the root has depth 0 and the deepest permitted
// node has depth MaxTreeHeight.
//
// Generalization level 0 keeps a value unchanged; level h replaces each value
// by its ancestor at depth (nodeDepth - h). Global recoding picks one level
// per column, so a column's admissible levels run from 0 to its tree height.
type Taxonomy struct {
	Name   string
	Nodes  []string
	Root   string
	Parent map[string]string

	depth     map[string]int
	height    int
	ancestor  map[string]map[int]string
	leafLevel map[string]int
}

// NewTaxonomy validates the supplied tree and precomputes the ancestor tables
// needed for generalization.
func NewTaxonomy(name string, nodes []string, root string, parent map[string]string) (*Taxonomy, error) {
	if len(nodes) == 0 {
		return nil, errors.New("netsec: taxonomy has no nodes")
	}
	index := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node == "" {
			return nil, errors.New("netsec: taxonomy contains an empty node id")
		}
		if index[node] {
			return nil, fmt.Errorf("netsec: taxonomy %q contains duplicate node %q", name, node)
		}
		index[node] = true
	}
	if !index[root] {
		return nil, fmt.Errorf("netsec: taxonomy %q root %q is not a node", name, root)
	}

	depth := make(map[string]int, len(nodes))
	height := 0
	for _, node := range nodes {
		chain := map[string]bool{node: true}
		cursor := node
		nodeDepth := 0
		for {
			up, hasParent := parent[cursor]
			if !hasParent {
				if cursor != root {
					return nil, fmt.Errorf("netsec: taxonomy %q node %q reaches orphan root %q", name, node, cursor)
				}
				break
			}
			if !index[up] {
				return nil, fmt.Errorf("netsec: taxonomy %q node %q references unknown parent %q", name, node, up)
			}
			nodeDepth++
			if nodeDepth > MaxTreeHeight {
				return nil, fmt.Errorf("netsec: taxonomy %q exceeds maximum height %d", name, MaxTreeHeight)
			}
			if chain[up] {
				return nil, fmt.Errorf("netsec: taxonomy %q contains a cycle through %q", name, up)
			}
			chain[up] = true
			cursor = up
		}
		depth[node] = nodeDepth
		if nodeDepth > height {
			height = nodeDepth
		}
	}

	ancestor := make(map[string]map[int]string, len(nodes))
	for _, node := range nodes {
		levels := map[int]string{0: node}
		cursor := node
		for level := 1; level <= depth[node]; level++ {
			cursor = parent[cursor]
			levels[level] = cursor
		}
		ancestor[node] = levels
	}

	return &Taxonomy{
		Name:      name,
		Nodes:     append([]string(nil), nodes...),
		Root:      root,
		Parent:    cloneParent(parent),
		depth:     depth,
		height:    height,
		ancestor:  ancestor,
		leafLevel: cloneDepth(depth),
	}, nil
}

// Height returns the maximum node depth in the tree, i.e. the highest
// generalization level the column admits.
func (t *Taxonomy) Height() int {
	return t.height
}

// generalizeAt maps a leaf value to the node identifier obtained by climbing
// level parent edges.
func (t *Taxonomy) generalizeAt(value string, level int) (string, error) {
	levels, ok := t.ancestor[value]
	if !ok {
		return "", fmt.Errorf("netsec: value %q is not a node of taxonomy %q", value, t.Name)
	}
	ancestor, ok := levels[level]
	if !ok {
		return "", fmt.Errorf("netsec: level %d is not admissible for value %q in taxonomy %q", level, value, t.Name)
	}
	return ancestor, nil
}

func cloneParent(parent map[string]string) map[string]string {
	clone := make(map[string]string, len(parent))
	for node, up := range parent {
		clone[node] = up
	}
	return clone
}

func cloneDepth(depth map[string]int) map[string]int {
	clone := make(map[string]int, len(depth))
	for node, d := range depth {
		clone[node] = d
	}
	return clone
}
