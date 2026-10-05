package tree

import "fmt"

// Validate independently checks tree invariants on a snapshot. It shares no
// code with the replay engine: it rebuilds the parent chains from scratch
// and verifies the displayed tree can never contain a cycle.
//
// Invariants:
//   - the root exists, has no parent, and is never deleted;
//   - every non-root node has exactly one parent that exists;
//   - every parent chain reaches the root within len(Names) steps
//     (no cycles, no orphans, no disconnected components);
//   - delete markers reference existing nodes only;
//   - visible ID-carrying paths are unique.
func Validate(s Snapshot) error {
	if _, ok := s.Names[RootID]; !ok {
		return fmt.Errorf("root %q missing", RootID)
	}
	if _, ok := s.Parents[RootID]; ok {
		return fmt.Errorf("root %q must not have a parent", RootID)
	}
	if s.Deleted[RootID] {
		return fmt.Errorf("root %q must not be deleted", RootID)
	}

	for id := range s.Names {
		if id == RootID {
			continue
		}
		p, ok := s.Parents[id]
		if !ok {
			return fmt.Errorf("node %q has no parent", id)
		}
		if _, ok := s.Names[p]; !ok {
			return fmt.Errorf("node %q has missing parent %q", id, p)
		}
	}

	// Cycle and connectivity check: walking parents from any node must hit
	// the root in at most len(Names) steps.
	for id := range s.Names {
		cur := id
		for steps := 0; ; steps++ {
			if cur == RootID {
				break
			}
			if steps >= len(s.Names) {
				return fmt.Errorf("cycle or disconnected subtree detected at node %q", id)
			}
			cur = s.Parents[cur]
		}
	}

	for id := range s.Deleted {
		if _, ok := s.Names[id]; !ok {
			return fmt.Errorf("delete marker references unknown node %q", id)
		}
	}

	// Visible paths must be unique. Segments carry IDs, so this holds unless
	// the state is corrupt; check it independently anyway.
	seen := map[string]string{} // path -> node ID
	for id := range s.Names {
		if hiddenIn(s, id) {
			continue
		}
		p := pathIn(s, id)
		if other, dup := seen[p]; dup {
			return fmt.Errorf("duplicate visible path %q for nodes %q and %q", p, other, id)
		}
		seen[p] = id
	}
	return nil
}

func hiddenIn(s Snapshot, id string) bool {
	for cur := id; ; {
		if s.Deleted[cur] {
			return true
		}
		p, ok := s.Parents[cur]
		if !ok {
			return false
		}
		cur = p
	}
}

func pathIn(s Snapshot, id string) string {
	path := ""
	for cur := id; ; {
		path = "/" + s.Names[cur] + "(" + cur + ")" + path
		p, ok := s.Parents[cur]
		if !ok {
			return path
		}
		cur = p
	}
}
