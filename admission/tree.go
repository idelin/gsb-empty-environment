package admission

import "sort"

// Default input limits mandated by the admission contract.
const (
	MaxOpsPerBatch   = 80
	MaxPathsPerBatch = 200
)

type nodeKind uint8

const (
	nodeTable nodeKind = iota
	nodeLiteral
	nodeRef
)

// node is the mutable internal tree representation. Only table nodes have
// children; literal and reference nodes are terminals.
type node struct {
	kind     nodeKind
	children map[string]*node
	lit      Literal
	source   Source
	target   Path
}

func newTableNode() *node {
	return &node{kind: nodeTable, children: map[string]*node{}}
}

func (n *node) clone() *node {
	cp := &node{kind: n.kind, lit: n.lit, source: n.source, target: clonePath(n.target)}
	if n.children != nil {
		cp.children = make(map[string]*node, len(n.children))
		for key, child := range n.children {
			cp.children[key] = child.clone()
		}
	}
	return cp
}

// lookup follows path from n and returns the node, or nil when absent.
func (n *node) lookup(path Path) *node {
	cur := n
	for _, comp := range path {
		if cur.kind != nodeTable || cur.children == nil {
			return nil
		}
		cur = cur.children[comp]
		if cur == nil {
			return nil
		}
	}
	return cur
}

// lookupParent returns the table node holding path's last component.
func (n *node) lookupParent(path Path) (*node, bool) {
	cur := n
	for _, comp := range path[:len(path)-1] {
		if cur.kind != nodeTable {
			return nil, false
		}
		cur = cur.children[comp]
		if cur == nil || cur.kind != nodeTable {
			return nil, false
		}
	}
	return cur, true
}

// Tree is an immutable configuration tree snapshot. Apply returns a new
// snapshot and never mutates the receiver, so a failed batch leaves the old
// tree fully intact.
type Tree struct {
	root *node
}

// NewTree returns an empty configuration tree.
func NewTree() *Tree {
	return &Tree{root: newTableNode()}
}

// Clone returns an independent deep copy of the tree.
func (t *Tree) Clone() *Tree {
	return &Tree{root: t.root.clone()}
}

// Apply admits a signed operation batch atomically. On success it returns
// a new tree; on failure the receiver is untouched and the error reports
// the offending operation index.
func (t *Tree) Apply(policy *Policy, ops []Op) (*Tree, error) {
	if len(ops) > MaxOpsPerBatch {
		return nil, newError(ErrLimitExceeded, -1, nil,
			"batch has %d operations, limit is %d", len(ops), MaxOpsPerBatch)
	}
	distinct := map[string]struct{}{}
	for i, op := range ops {
		if err := validateOp(op); err != nil {
			return nil, withOpIndex(err, i)
		}
		distinct[encodePath(op.Path)] = struct{}{}
		if op.Kind == OpLink {
			distinct[encodePath(op.Target)] = struct{}{}
		}
	}
	if len(distinct) > MaxPathsPerBatch {
		return nil, newError(ErrLimitExceeded, -1, nil,
			"batch touches %d distinct paths, limit is %d",
			len(distinct), MaxPathsPerBatch)
	}

	candidate := t.Clone()
	for i, op := range ops {
		if err := checkAffected(policy, candidate.root, op, i); err != nil {
			return nil, err
		}
		if err := applyOp(candidate.root, op); err != nil {
			return nil, withOpIndex(err, i)
		}
	}

	if _, err := materializeAll(candidate.root, policy); err != nil {
		return nil, err
	}
	return candidate, nil
}

// EffectiveLeaves returns every leaf of the resolved tree, ordered by
// vantage path, each with its terminal source and full reference closure.
// Trust violations, dangling references and cycles are reported as errors.
func (t *Tree) EffectiveLeaves(policy *Policy) ([]EffectiveLeaf, error) {
	return materializeAll(t.root, policy)
}

func validateOp(op Op) error {
	if len(op.Path) == 0 {
		return newError(ErrInvalidInput, -1, op.Path, "operation path must be non-empty")
	}
	if op.Principal == "" {
		return newError(ErrInvalidInput, -1, op.Path, "operation principal is required")
	}
	switch op.Kind {
	case OpSet:
		if op.Source.Principal == "" {
			return newError(ErrInvalidInput, -1, op.Path, "set operation requires a signed source")
		}
	case OpLink:
		if op.Source.Principal == "" {
			return newError(ErrInvalidInput, -1, op.Path, "link operation requires a signed source")
		}
	case OpReplace:
		if op.Table == nil {
			return newError(ErrInvalidInput, -1, op.Path, "replace operation requires a table")
		}
	case OpDelete:
	default:
		return newError(ErrInvalidInput, -1, op.Path, "unknown operation kind %d", op.Kind)
	}
	return nil
}

// checkAffected enforces write permission over every stored terminal whose
// stored location is ultimately touched by the operation, not merely over
// the command's own path. This covers ancestor replacement wiping protected
// descendants and reference retargeting changing which leaves appear.
func checkAffected(policy *Policy, root *node, op Op, opIndex int) error {
	affected := collectAffected(root, op)
	seen := map[string]bool{}
	for _, path := range affected {
		key := encodePath(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		if !policy.CanWrite(op.Principal, path) {
			return newError(ErrDenied, opIndex, path,
				"principal %q cannot write affected leaf %v",
				op.Principal, []string(path))
		}
	}
	return nil
}

// collectAffected returns the stored-terminal permission paths for one op:
//   - Set/Link: every terminal currently stored under op.Path plus, when a
//     table fragment is written there, every terminal inside it.
//   - Replace: every terminal currently stored under op.Path plus every
//     fragment terminal that will land under op.Path.
//   - Delete: every terminal currently stored under op.Path.
func collectAffected(root *node, op Op) []Path {
	var out []Path
	switch op.Kind {
	case OpSet, OpLink:
		out = append(out, terminalPathsUnder(root, op.Path)...)
		out = append(out, clonePath(op.Path))
	case OpReplace:
		out = append(out, terminalPathsUnder(root, op.Path)...)
		for _, term := range op.Table.terminalsInOrder() {
			out = append(out, joinPath(op.Path, term.path))
		}
	case OpDelete:
		out = append(out, terminalPathsUnder(root, op.Path)...)
	}
	return out
}

// terminalPathsUnder lists stored terminal paths (literals and references)
// at path itself or anywhere in its subtree. A missing node yields nothing.
func terminalPathsUnder(root *node, path Path) []Path {
	target := root.lookup(path)
	if target == nil {
		return nil
	}
	var out []Path
	var walk func(n *node, prefix Path)
	walk = func(n *node, prefix Path) {
		switch n.kind {
		case nodeLiteral, nodeRef:
			out = append(out, clonePath(prefix))
		case nodeTable:
			keys := make([]string, 0, len(n.children))
			for key := range n.children {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(n.children[key], append(clonePath(prefix), key))
			}
		}
	}
	walk(target, clonePath(path))
	return out
}

func applyOp(root *node, op Op) error {
	switch op.Kind {
	case OpSet:
		return writeTerminal(root, op.Path, &node{
			kind:   nodeLiteral,
			lit:    op.Literal,
			source: op.Source,
		})
	case OpLink:
		return writeTerminal(root, op.Path, &node{
			kind:   nodeRef,
			source: op.Source,
			target: clonePath(op.Target),
		})
	case OpReplace:
		return replaceTable(root, op)
	case OpDelete:
		return removePath(root, op.Path)
	}
	return newError(ErrInvalidInput, -1, op.Path, "unknown operation kind")
}

// writeTerminal stores a terminal at path, creating intermediate tables and
// replacing any table subtree that previously occupied the location.
func writeTerminal(root *node, path Path, terminal *node) error {
	parent, err := ensureParent(root, path)
	if err != nil {
		return err
	}
	parent.children[path[len(path)-1]] = terminal
	return nil
}

// replaceTable swaps the location for a fresh table built from the fragment.
func replaceTable(root *node, op Op) error {
	parent, err := ensureParent(root, op.Path)
	if err != nil {
		return err
	}
	table := newTableNode()
	for _, term := range op.Table.terminalsInOrder() {
		cur := table
		for _, comp := range term.path[:len(term.path)-1] {
			next := cur.children[comp]
			if next == nil {
				next = newTableNode()
				cur.children[comp] = next
			}
			cur = next
		}
		cur.children[term.path[len(term.path)-1]] = &node{
			kind:   nodeLiteral,
			lit:    term.lit,
			source: term.source,
		}
	}
	parent.children[op.Path[len(op.Path)-1]] = table
	return nil
}

// removePath deletes a node if present. Deleting a missing path is a
// no-op so retries stay idempotent.
func removePath(root *node, path Path) error {
	parent, ok := root.lookupParent(path)
	if !ok {
		return nil
	}
	delete(parent.children, path[len(path)-1])
	return nil
}

// ensureParent walks path's ancestors, creating missing table nodes, and
// returns the table holding the final component. An existing terminal
// ancestor makes the location unreachable and is rejected.
func ensureParent(root *node, path Path) (*node, error) {
	cur := root
	for _, comp := range path[:len(path)-1] {
		next := cur.children[comp]
		if next == nil {
			next = newTableNode()
			cur.children[comp] = next
		} else if next.kind != nodeTable {
			return nil, newError(ErrInvalidInput, -1, path,
				"an ancestor of %v is a terminal and cannot hold children",
				[]string(path))
		}
		cur = next
	}
	return cur, nil
}
