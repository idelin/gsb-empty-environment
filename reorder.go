// Package reorder applies batched paragraph operations (delete, move,
// modify) to a document modelled as a sequence of nodes with stable IDs.
//
// Semantics of a batch:
//   - Deletes remove nodes by ID.
//   - Moves relocate a node so that it lands immediately after a target
//     predecessor ID (an empty predecessor means the front of the
//     document). If the predecessor was deleted in the same batch, the
//     nearest surviving predecessor along the OLD order is used instead;
//     if none survives, the node moves to the front.
//   - Modifies replace node content and are merged into the result
//     regardless of whether the node was also moved.
//   - Move constraints must be acyclic; a cycle (including a move that
//     resolves onto the moved node itself) is reported as ErrCyclicMove.
package reorder

import "errors"

// Node is a paragraph with a stable ID.
type Node struct {
	ID      string
	Content string
}

// Move relocates node ID so it lands immediately after the node After.
// An empty After moves the node to the front of the document.
type Move struct {
	ID    string
	After string
}

// Modify replaces the content of node ID.
type Modify struct {
	ID      string
	Content string
}

// Batch is a set of operations applied atomically to a node sequence.
type Batch struct {
	Deletes  []string
	Moves    []Move
	Modifies []Modify
}

var (
	// ErrDuplicateID is returned when the base sequence contains the
	// same ID twice.
	ErrDuplicateID = errors.New("reorder: duplicate node id")
	// ErrUnknownNode is returned when an operation references a node ID
	// that does not exist in the base sequence.
	ErrUnknownNode = errors.New("reorder: unknown node id")
	// ErrUnknownAnchor is returned when a move predecessor ID does not
	// exist in the base sequence.
	ErrUnknownAnchor = errors.New("reorder: unknown move predecessor id")
	// ErrConflict is returned when the same node is targeted by
	// incompatible operations (e.g. deleted and modified) or by the
	// same kind of operation twice.
	ErrConflict = errors.New("reorder: conflicting operations on node")
	// ErrSelfMove is returned when a move names its own node as the
	// predecessor.
	ErrSelfMove = errors.New("reorder: node cannot be moved after itself")
	// ErrCyclicMove is returned when move constraints form a cycle.
	ErrCyclicMove = errors.New("reorder: cyclic move constraints")
)

// Apply applies b to base and returns the resulting node sequence.
// The input is not mutated.
func Apply(base []Node, b Batch) ([]Node, error) {
	pos := make(map[string]int, len(base))
	for i, n := range base {
		if _, dup := pos[n.ID]; dup {
			return nil, ErrDuplicateID
		}
		pos[n.ID] = i
	}

	deleted := make(map[string]bool, len(b.Deletes))
	for _, id := range b.Deletes {
		if _, ok := pos[id]; !ok {
			return nil, ErrUnknownNode
		}
		if deleted[id] {
			return nil, ErrConflict
		}
		deleted[id] = true
	}

	moves := make(map[string]string, len(b.Moves))
	for _, m := range b.Moves {
		if _, ok := pos[m.ID]; !ok {
			return nil, ErrUnknownNode
		}
		if deleted[m.ID] {
			return nil, ErrConflict
		}
		if _, dup := moves[m.ID]; dup {
			return nil, ErrConflict
		}
		if m.After == m.ID {
			return nil, ErrSelfMove
		}
		if m.After != "" {
			if _, ok := pos[m.After]; !ok {
				return nil, ErrUnknownAnchor
			}
		}
		moves[m.ID] = m.After
	}

	modified := make(map[string]string, len(b.Modifies))
	for _, m := range b.Modifies {
		if _, ok := pos[m.ID]; !ok {
			return nil, ErrUnknownNode
		}
		if deleted[m.ID] {
			return nil, ErrConflict
		}
		if _, dup := modified[m.ID]; dup {
			return nil, ErrConflict
		}
		modified[m.ID] = m.Content
	}

	// Resolve every move anchor to a surviving node ID ("" = front).
	// A deleted predecessor is replaced by the nearest surviving
	// predecessor along the old order.
	anchor := make(map[string]string, len(moves))
	for id, after := range moves {
		anchor[id] = resolveAnchor(base, pos, deleted, id, after)
	}

	// Detect cycles among moved nodes: an edge id -> anchor[id] exists
	// when the anchor is itself a moved node.
	if err := checkCycles(anchor, moves); err != nil {
		return nil, err
	}

	// Group moved nodes by their anchor, preserving old relative order
	// within each group.
	children := make(map[string][]string)
	for _, n := range base {
		if _, moved := moves[n.ID]; !moved {
			continue
		}
		a := anchor[n.ID]
		children[a] = append(children[a], n.ID)
	}

	content := make(map[string]string, len(base))
	for _, n := range base {
		content[n.ID] = n.Content
	}
	for id, c := range modified {
		content[id] = c
	}

	var out []Node
	var emit func(a string)
	emit = func(a string) {
		for _, id := range children[a] {
			out = append(out, Node{ID: id, Content: content[id]})
			emit(id)
		}
	}
	emit("")
	for _, n := range base {
		if deleted[n.ID] {
			continue
		}
		if _, moved := moves[n.ID]; moved {
			continue
		}
		out = append(out, Node{ID: n.ID, Content: content[n.ID]})
		emit(n.ID)
	}
	return out, nil
}

// resolveAnchor maps a move predecessor to the nearest surviving node
// walking backwards along the old order, or "" (front) if none survives.
// The moved node itself is skipped: its old position is being vacated,
// so it cannot serve as its own predecessor.
func resolveAnchor(base []Node, pos map[string]int, deleted map[string]bool, movedID, after string) string {
	if after == "" {
		return ""
	}
	for i := pos[after]; i >= 0; i-- {
		id := base[i].ID
		if id == movedID || deleted[id] {
			continue
		}
		return id
	}
	return ""
}

// checkCycles verifies that the moved-node dependency graph is acyclic.
func checkCycles(anchor map[string]string, moves map[string]string) error {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS path
		black = 2 // fully explored
	)
	color := make(map[string]int, len(moves))
	var visit func(id string) error
	visit = func(id string) error {
		color[id] = gray
		if a := anchor[id]; a != "" {
			if _, moved := moves[a]; moved {
				switch color[a] {
				case gray:
					return ErrCyclicMove
				case white:
					if err := visit(a); err != nil {
						return err
					}
				}
			}
		}
		color[id] = black
		return nil
	}
	for id := range moves {
		if color[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}
