// Package zoneinterp is a small, self-contained full-zone-tree interpreter.
//
// It intentionally does NOT import the authenticated patch component: it is
// the reference oracle for how a completely materialized zone answers
// authorization queries. Cross-package tests feed equivalent trees to both
// implementations and require identical conclusions. All name ancestry here
// is structural over whole labels, never string-prefix based.
package zoneinterp

import "sort"

// Name is an apex-first sequence of labels; the empty name is the apex.
type Name []string

// Record is a resource record. NS records on a node mark a delegation cut.
type Record struct {
	Type  string
	Value string
}

// Node is one full-tree node. A node with Delegate == true is a cut: its
// subtree is out of zone and the interpreter stops there.
type Node struct {
	Delegate bool
	Records  []Record
	path     Name
	children map[string]*Node
}

// Tree is the materialized zone, indexed by name key.
type Tree struct {
	zone  string
	nodes map[string]*Node
}

func nameKey(n Name) string {
	key := ""
	for i, label := range n {
		if i > 0 {
			key += "\x00"
		}
		key += label
	}
	return key
}

// NewTree returns a tree containing only an empty apex node.
func NewTree(zone string) *Tree {
	t := &Tree{zone: zone, nodes: map[string]*Node{}}
	t.nodes[""] = &Node{children: map[string]*Node{}}
	return t
}

// Zone returns the zone name.
func (t *Tree) Zone() string { return t.zone }

func (t *Tree) get(n Name) *Node { return t.nodes[nameKey(n)] }

// ensure walks or creates the path, refusing to cross a delegation cut.
func (t *Tree) ensure(path Name) (*Node, error) {
	cur := t.nodes[""]
	for depth := 1; depth <= len(path); depth++ {
		if cur.Delegate {
			return nil, ErrCutCrossed
		}
		label := path[depth-1]
		child := cur.children[label]
		if child == nil {
			childPath := append(Name{}, path[:depth]...)
			child = &Node{path: childPath, children: map[string]*Node{}}
			cur.children[label] = child
			t.nodes[nameKey(path[:depth])] = child
		}
		cur = child
	}
	return cur, nil
}

// PutRecords adds records to name, creating intermediate regular nodes.
func (t *Tree) PutRecords(name Name, recs ...Record) error {
	node, err := t.ensure(name)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		present := false
		for _, existing := range node.Records {
			if existing == rec {
				present = true
				break
			}
		}
		if !present {
			node.Records = append(node.Records, rec)
		}
	}
	return nil
}

// AddDelegation marks name as a cut carrying the NS records and removes any
// out-of-zone material that had been provisioned beneath it.
func (t *Tree) AddDelegation(name Name, ns []Record) error {
	node, err := t.ensure(name)
	if err != nil {
		return err
	}
	node.Delegate = true
	node.Records = append([]Record(nil), ns...)
	node.children = map[string]*Node{}
	for key, candidate := range t.nodes {
		if candidate != node && hasPrefix(candidate.path, name) {
			delete(t.nodes, key)
		}
	}
	return nil
}

// RemoveDelegation lifts the cut at name, leaving a regular empty node.
func (t *Tree) RemoveDelegation(name Name) error {
	node := t.get(name)
	if node == nil {
		return ErrNotFound
	}
	if !node.Delegate {
		return ErrNotDelegation
	}
	node.Delegate = false
	node.Records = nil
	return nil
}

// SetWildcard installs, replaces or removes (nil recs) the "*" template
// beneath parent.
func (t *Tree) SetWildcard(parent Name, recs []Record) error {
	anchor, err := t.ensure(parent)
	if err != nil {
		return err
	}
	wildName := append(append(Name{}, parent...), "*")
	if len(recs) == 0 {
		if child := anchor.children["*"]; child != nil {
			delete(anchor.children, "*")
			delete(t.nodes, nameKey(wildName))
		}
		return nil
	}
	wild := anchor.children["*"]
	if wild == nil {
		wild = &Node{children: map[string]*Node{}}
		anchor.children["*"] = wild
		t.nodes[nameKey(wildName)] = wild
	}
	wild.Records = append([]Record(nil), recs...)
	return nil
}

// DeleteSubtree removes name and all descendants.
func (t *Tree) DeleteSubtree(name Name) error {
	if len(name) == 0 {
		return ErrApexProtected
	}
	node := t.get(name)
	if node == nil {
		return ErrNotFound
	}
	parent := t.get(name[:len(name)-1])
	delete(parent.children, name[len(name)-1])
	for key, candidate := range t.nodes {
		if hasPrefix(candidate.path, name) {
			delete(t.nodes, key)
		}
	}
	return nil
}

func splitKey(key string) Name {
	if key == "" {
		return Name{}
	}
	out := Name{}
	start := 0
	for i := 0; i <= len(key); i++ {
		if i == len(key) || key[i] == 0 {
			out = append(out, key[start:i])
			start = i + 1
		}
	}
	return out
}

func hasPrefix(name, ancestor Name) bool {
	if len(ancestor) > len(name) {
		return false
	}
	for i := range ancestor {
		if name[i] != ancestor[i] {
			return false
		}
	}
	return true
}

// SortedChildLabels is a test/inspection helper.
func (t *Tree) SortedChildLabels(name Name) []string {
	node := t.get(name)
	if node == nil {
		return nil
	}
	labels := make([]string, 0, len(node.children))
	for label := range node.children {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}
