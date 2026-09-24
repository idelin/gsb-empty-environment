package pathindex

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrNotFound is returned when an ID (or parent ID) is unknown.
	ErrNotFound = errors.New("pathindex: node not found")
	// ErrExists is returned when creating an ID that already exists.
	ErrExists = errors.New("pathindex: id already exists")
	// ErrCycle is returned when a move would make a node its own
	// ancestor, e.g. moving A into its own subdirectory B.
	ErrCycle = errors.New("pathindex: move would create a cycle")
	// ErrConflict is returned when two siblings would share a name.
	ErrConflict = errors.New("pathindex: sibling name conflict")
	// ErrInvalidName is returned for empty names or names
	// containing a path separator.
	ErrInvalidName = errors.New("pathindex: invalid name")
	// ErrInvalidID is returned for empty IDs or the reserved root ID.
	ErrInvalidID = errors.New("pathindex: invalid id")
)

// MoveOp describes one subtree move inside a MoveBatch. NewName may be
// empty to keep the current name (a pure relocation); setting NewName
// while keeping the parent is a rename. The object's ID never changes.
type MoveOp struct {
	ID        string
	NewParent string
	NewName   string
}

// Create inserts a new object id under parent with the given name.
// The parent must exist and must not already have a child with that
// name.
func (s *Store) Create(id, parent, name string) error {
	if id == rootID {
		return ErrInvalidID
	}
	if _, ok := s.nodes[id]; ok {
		return fmt.Errorf("%v: %q", ErrExists, id)
	}
	if _, ok := s.nodes[parent]; !ok {
		return fmt.Errorf("%v: parent %q", ErrNotFound, parent)
	}
	if err := checkName(name); err != nil {
		return err
	}
	for _, n := range s.nodes {
		if n.Parent == parent && n.Name == name {
			return fmt.Errorf("%v: %q already exists under parent %q", ErrConflict, name, parent)
		}
	}
	next := s.snapshot()
	next.Nodes[id] = node{Parent: parent, Name: name}
	next.Version = s.version + 1
	return s.commitLocked(next)
}

// Move relocates the subtree rooted at id under newParent, keeping its
// current name. Equivalent to MoveBatch with a single op.
func (s *Store) Move(id, newParent string) error {
	return s.MoveBatch([]MoveOp{{ID: id, NewParent: newParent}})
}

// Rename changes the name of id without moving it. The ID is stable:
// every reference to the object keeps working after a rename.
func (s *Store) Rename(id, newName string) error {
	n, ok := s.nodes[id]
	if !ok || id == rootID {
		return fmt.Errorf("%v: %q", ErrNotFound, id)
	}
	return s.MoveBatch([]MoveOp{{ID: id, NewParent: n.Parent, NewName: newName}})
}

// MoveBatch atomically applies a batch of subtree moves. Either every
// op is committed (single durable state write, version +1) or none is
// and all paths stay exactly as before.
//
// Validation runs against the final state of the whole batch, not
// against intermediate states, so:
//   - cycles introduced by the combination of ops are rejected
//     (including moving A under its own descendant B);
//   - sibling name conflicts are detected regardless of op order:
//     two ops landing on the same (parent, name) slot always fail,
//     and an op moving into a slot freed by another op in the same
//     batch succeeds.
func (s *Store) MoveBatch(ops []MoveOp) error {
	if len(ops) == 0 {
		return nil
	}
	next := s.snapshot()
	for _, op := range ops {
		if op.ID == rootID {
			return fmt.Errorf("%v: cannot move root", ErrInvalidID)
		}
		n, ok := next.Nodes[op.ID]
		if !ok {
			return fmt.Errorf("%v: %q", ErrNotFound, op.ID)
		}
		if _, ok := next.Nodes[op.NewParent]; !ok {
			return fmt.Errorf("%v: parent %q", ErrNotFound, op.NewParent)
		}
		name := op.NewName
		if name == "" {
			name = n.Name
		}
		if err := checkName(name); err != nil {
			return err
		}
		n.Parent = op.NewParent
		n.Name = name
		next.Nodes[op.ID] = n
	}
	if err := checkAcyclic(next.Nodes, ops); err != nil {
		return err
	}
	if err := checkSiblingConflicts(next.Nodes); err != nil {
		return err
	}
	next.Version = s.version + 1
	return s.commitLocked(next)
}

// checkAcyclic walks the ancestor chain of every moved node in the
// final state; any repeated node means the batch created a cycle.
func checkAcyclic(nodes map[string]node, ops []MoveOp) error {
	for _, op := range ops {
		seen := map[string]bool{}
		for cur := op.ID; cur != rootID; {
			if seen[cur] {
				return fmt.Errorf("%v: involving %q", ErrCycle, op.ID)
			}
			seen[cur] = true
			cur = nodes[cur].Parent
		}
	}
	return nil
}

// checkSiblingConflicts builds the full (parent, name) -> id map of
// the final state. Because it inspects the end state rather than
// replaying ops one by one, conflicts can never be missed or falsely
// reported due to batch ordering.
func checkSiblingConflicts(nodes map[string]node) error {
	type slot struct {
		parent string
		name   string
	}
	taken := make(map[slot]string, len(nodes))
	for id, n := range nodes {
		if id == rootID {
			continue
		}
		k := slot{parent: n.Parent, name: n.Name}
		if other, ok := taken[k]; ok {
			return fmt.Errorf("%v: %q and %q both named %q under the same parent",
				ErrConflict, other, id, n.Name)
		}
		taken[k] = id
	}
	return nil
}

// Path returns the absolute path of id, e.g. "/a/b/c". Results are
// cached and the cache is keyed by the store version: any committed
// mutation bumps the version and invalidates every cached entry, so a
// stale path is never served after a move or rename.
func (s *Store) Path(id string) (string, error) {
	return s.path(id)
}

// Children lists the IDs of the direct children of id, sorted for
// deterministic output.
func (s *Store) Children(id string) ([]string, error) {
	if _, ok := s.nodes[id]; !ok {
		return nil, fmt.Errorf("%v: %q", ErrNotFound, id)
	}
	var out []string
	for cid, n := range s.nodes {
		if cid != rootID && n.Parent == id {
			out = append(out, cid)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Name returns the current name of id.
func (s *Store) Name(id string) (string, error) {
	n, ok := s.nodes[id]
	if !ok {
		return "", fmt.Errorf("%v: %q", ErrNotFound, id)
	}
	return n.Name, nil
}

func checkName(name string) error {
	if name == "" || strings.Contains(name, "/") || name == "." || name == ".." {
		return fmt.Errorf("%v: %q", ErrInvalidName, name)
	}
	return nil
}
