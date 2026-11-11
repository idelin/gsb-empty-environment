package shortcircuit

import "fmt"

// validateTree rejects malformed trees so that evaluation and replay share the
// same structural assumptions. A node reachable twice is a shared node and is
// allowed; cycles are rejected.
func validateTree(root Node) error {
	if root == nil {
		return fmt.Errorf("%w: nil root", ErrInvalidTree)
	}
	state := map[Node]int{}  // 0 unseen, 1 on stack, 2 done
	ids := map[string]Node{} // id -> node claiming it; a shared id must be the same pointer
	var walk func(Node) error
	walk = func(n Node) error {
		if n == nil {
			return fmt.Errorf("%w: nil node", ErrInvalidTree)
		}
		if n.nodeID() == "" {
			return fmt.Errorf("%w: node has empty id", ErrInvalidTree)
		}
		if owner, ok := ids[n.nodeID()]; ok && owner != n {
			return fmt.Errorf("%w: distinct nodes share id %q; reuse one node pointer for shared subtrees",
				ErrInvalidTree, n.nodeID())
		}
		ids[n.nodeID()] = n
		switch state[n] {
		case 1:
			return fmt.Errorf("%w: cycle at node %q", ErrInvalidTree, n.nodeID())
		case 2:
			return nil // shared node
		}
		state[n] = 1

		switch t := n.(type) {
		case *CheckNode:
			if t.Fn == nil {
				return fmt.Errorf("%w: check node %q has nil function", ErrInvalidTree, t.ID)
			}
			if t.Rule.Name == "" || t.Rule.Version == "" {
				return fmt.Errorf("%w: check node %q has incomplete rule version", ErrInvalidTree, t.ID)
			}
		case *AndNode:
			if len(t.Children) == 0 {
				return fmt.Errorf("%w: node %q has no children", ErrInvalidTree, n.nodeID())
			}
			for _, child := range t.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
		case *OrNode:
			if len(t.Children) == 0 {
				return fmt.Errorf("%w: node %q has no children", ErrInvalidTree, n.nodeID())
			}
			for _, child := range t.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
		case *NotNode:
			if t.Child == nil {
				return fmt.Errorf("%w: not node %q has nil child", ErrInvalidTree, t.ID)
			}
			if err := walk(t.Child); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unknown node type %T", ErrInvalidTree, n)
		}

		state[n] = 2
		return nil
	}
	return walk(root)
}
