package main

import "fmt"

type TreeIndex struct {
	Nodes    map[string]*AccessNode
	Parent   map[string]*string
	Children map[string][]string
	FormRoot map[string]*string
	Roots    []string
}

func indexTree(nodes []AccessNode, formRoles map[string]bool) (*TreeIndex, error) {
	idx := &TreeIndex{
		Nodes:    map[string]*AccessNode{},
		Parent:   map[string]*string{},
		Children: map[string][]string{},
		FormRoot: map[string]*string{},
	}
	for i := range nodes {
		n := &nodes[i]
		if _, exists := idx.Nodes[n.ID]; exists {
			return nil, fmt.Errorf("duplicate node id: %s", n.ID)
		}
		idx.Nodes[n.ID] = n
		idx.Parent[n.ID] = n.ParentID
		idx.Children[n.ID] = []string{}
	}
	for _, n := range nodes {
		if n.ParentID == nil {
			idx.Roots = append(idx.Roots, n.ID)
			continue
		}
		if _, ok := idx.Nodes[*n.ParentID]; !ok {
			return nil, fmt.Errorf("node %s references unknown parent %s", n.ID, *n.ParentID)
		}
		pid := *n.ParentID
		idx.Children[pid] = append(idx.Children[pid], n.ID)
	}
	var walk func(id string, nearest *string)
	walk = func(id string, nearest *string) {
		n := idx.Nodes[id]
		current := nearest
		if formRoles[n.Role] {
			current = &n.ID
		}
		idx.FormRoot[id] = current
		for _, child := range idx.Children[id] {
			walk(child, current)
		}
	}
	for _, root := range idx.Roots {
		walk(root, nil)
	}
	return idx, nil
}

func sameStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sharesFormAncestor(idx *TreeIndex, labelID, controlID string) bool {
	return sameStringPtr(idx.FormRoot[labelID], idx.FormRoot[controlID])
}
