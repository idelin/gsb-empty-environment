package treemerge

import "fmt"

// ValidateTree independently checks the structural invariants of a parent
// map produced by a merge: exactly one root, every node has exactly one
// existing parent, every node reaches the root, and there are no cycles.
// It deliberately shares no logic with the replay code so it can serve as
// an independent checker (e.g. in tests or as a runtime assertion).
func ValidateTree(parents map[string]string) error {
	rootParent, ok := parents[RootID]
	if !ok {
		return fmt.Errorf("tree has no root node %q", RootID)
	}
	if rootParent != "" {
		return fmt.Errorf("root %q must not have a parent, got %q", RootID, rootParent)
	}
	for id, parent := range parents {
		if id == RootID {
			continue
		}
		if parent == "" {
			return fmt.Errorf("node %q has no parent", id)
		}
		if parent == id {
			return fmt.Errorf("node %q is its own parent", id)
		}
		if _, ok := parents[parent]; !ok {
			return fmt.Errorf("node %q points at unknown parent %q", id, parent)
		}
	}
	for id := range parents {
		seen := map[string]bool{}
		cur := id
		for cur != RootID {
			if seen[cur] {
				return fmt.Errorf("cycle detected involving node %q", cur)
			}
			seen[cur] = true
			next, ok := parents[cur]
			if !ok || next == "" {
				return fmt.Errorf("node %q does not reach the root", id)
			}
			cur = next
		}
	}
	return nil
}

// VisibleSet independently derives the visible node set from a parent map
// and a delete-marker set: a node is visible iff neither it nor any of its
// ancestors carries the marker. Assumes ValidateTree passed.
func VisibleSet(parents map[string]string, deleted map[string]bool) map[string]bool {
	visible := map[string]bool{}
	for id := range parents {
		shown := true
		cur := id
		for {
			if deleted[cur] {
				shown = false
				break
			}
			if cur == RootID {
				break
			}
			cur = parents[cur]
		}
		visible[id] = shown
	}
	return visible
}
