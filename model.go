// Package treeplan plans how a tree viewer expands and collapses groups.
//
// Problem 1 (SolveExpandSet): pick the cheapest ancestor-closed set of
// groups to expand so that every target leaf is visible; expanded groups
// cost ExpandCost once (shared by all targets below them) and every
// non-target leaf exposed by the expansion adds its Penalty.
//
// Problem 2 (SolveSequence): plan an expand/collapse/serve action sequence
// that serves every target under a peak-memory budget at minimum action
// cost. Expanding a group loads its own data chunk plus the chunks of the
// leaves it exposes; serving a target opens a persistent view that keeps a
// reference to the target's leaf chunk, so collapsing an ancestor cannot
// free chunks still referenced by other views.
package treeplan

import (
	"errors"
	"fmt"
)

// Leaf is a terminal data item shown directly under its group.
type Leaf struct {
	Name      string `json:"name"`
	Penalty   int64  `json:"penalty,omitempty"`   // problem 1: cost of exposing a non-target leaf
	Memory    int64  `json:"memory,omitempty"`    // problem 2: bytes of the leaf's data chunk
	ServeCost int64  `json:"serveCost,omitempty"` // problem 2: action cost of serving the leaf
}

// Group is a collapsible tree node.
type Group struct {
	Name         string   `json:"name"`
	ExpandCost   int64    `json:"expandCost,omitempty"`
	CollapseCost int64    `json:"collapseCost,omitempty"`
	Memory       int64    `json:"memory,omitempty"` // problem 2: bytes of the group's own data chunk
	Groups       []*Group `json:"groups,omitempty"`
	Leaves       []Leaf   `json:"leaves,omitempty"`
}

// Spec is the JSON input document.
type Spec struct {
	Root    *Group   `json:"root"`
	Targets []string `json:"targets"`
	Budget  int64    `json:"budget,omitempty"` // problem 2: peak memory budget in bytes
}

// Tree is the flattened, indexed form of a Spec. Groups are numbered in
// preorder, so a parent always has a smaller index than its children.
type Tree struct {
	Groups      []*Group
	Parent      []int
	Children    [][]int
	GroupLeaves [][]int // leaf indices directly under each group

	Leaves      []Leaf
	LeafGroup   []int
	LeafAncMask []uint32 // ancestor groups of the leaf, its own group included

	GroupIndex map[string]int
	LeafIndex  map[string]int

	IsTarget  []bool
	Targets   []int    // leaf indices of targets
	TargetAnc []uint32 // per target, groups that must be expanded to serve it
}

const maxGroups = 32 // bitmask width

// BuildTree validates a Spec and flattens it into a Tree.
func BuildTree(spec *Spec) (*Tree, error) {
	if spec == nil || spec.Root == nil {
		return nil, errors.New("spec.root is required")
	}
	t := &Tree{
		GroupIndex: map[string]int{},
		LeafIndex:  map[string]int{},
	}
	var walk func(g *Group, parent int, anc uint32) error
	walk = func(g *Group, parent int, anc uint32) error {
		if g.Name == "" {
			return errors.New("group with empty name")
		}
		if _, dup := t.GroupIndex[g.Name]; dup {
			return fmt.Errorf("duplicate group name %q", g.Name)
		}
		if g.ExpandCost < 0 || g.CollapseCost < 0 || g.Memory < 0 {
			return fmt.Errorf("group %q: costs and memory must be non-negative", g.Name)
		}
		idx := len(t.Groups)
		if idx >= maxGroups {
			return fmt.Errorf("at most %d groups supported", maxGroups)
		}
		t.GroupIndex[g.Name] = idx
		t.Groups = append(t.Groups, g)
		t.Parent = append(t.Parent, parent)
		t.Children = append(t.Children, nil)
		t.GroupLeaves = append(t.GroupLeaves, nil)
		if parent >= 0 {
			t.Children[parent] = append(t.Children[parent], idx)
		}
		selfAnc := anc | (uint32(1) << uint(idx))
		for _, lf := range g.Leaves {
			if lf.Name == "" {
				return fmt.Errorf("group %q: leaf with empty name", g.Name)
			}
			if _, dup := t.LeafIndex[lf.Name]; dup {
				return fmt.Errorf("duplicate leaf name %q", lf.Name)
			}
			if lf.Penalty < 0 || lf.Memory < 0 || lf.ServeCost < 0 {
				return fmt.Errorf("leaf %q: penalty, memory and serveCost must be non-negative", lf.Name)
			}
			li := len(t.Leaves)
			t.LeafIndex[lf.Name] = li
			t.Leaves = append(t.Leaves, lf)
			t.LeafGroup = append(t.LeafGroup, idx)
			t.LeafAncMask = append(t.LeafAncMask, selfAnc)
			t.GroupLeaves[idx] = append(t.GroupLeaves[idx], li)
		}
		for _, c := range g.Groups {
			if err := walk(c, idx, selfAnc); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(spec.Root, -1, 0); err != nil {
		return nil, err
	}
	t.IsTarget = make([]bool, len(t.Leaves))
	for _, name := range spec.Targets {
		li, ok := t.LeafIndex[name]
		if !ok {
			return nil, fmt.Errorf("unknown target leaf %q", name)
		}
		if t.IsTarget[li] {
			continue
		}
		t.IsTarget[li] = true
		t.Targets = append(t.Targets, li)
		t.TargetAnc = append(t.TargetAnc, t.LeafAncMask[li])
	}
	if len(t.Targets) == 0 {
		return nil, errors.New("at least one target leaf is required")
	}
	return t, nil
}
