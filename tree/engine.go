package tree

import (
	"fmt"
	"sort"
	"strings"
)

// Engine holds the operation log and the replayed tree state. Every Merge
// re-replays the full operation log from scratch, so results depend only on
// the operation set, never on arrival order or batching.
type Engine struct {
	log []Op // every op ever merged, including conflicting duplicates

	names    map[string]string // node ID -> current name
	parent   map[string]string // node ID -> parent ID (root has none)
	deleted  map[string]bool   // independent visibility markers
	rejected []Rejection       // deterministic order: (Time, ID, Reason)
}

// NewEngine returns an engine containing only the root node.
func NewEngine() *Engine {
	e := &Engine{}
	e.reset()
	return e
}

func (e *Engine) reset() {
	e.names = map[string]string{RootID: "root"}
	e.parent = map[string]string{}
	e.deleted = map[string]bool{}
	e.rejected = nil
}

// Merge adds operations and re-replays the whole log deterministically.
func (e *Engine) Merge(ops ...Op) {
	e.log = append(e.log, ops...)
	e.replay()
}

// lessOp is the canonical total order used to resolve duplicate op IDs.
func lessOp(a, b Op) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Node != b.Node {
		return a.Node < b.Node
	}
	if a.Parent != b.Parent {
		return a.Parent < b.Parent
	}
	return a.Name < b.Name
}

func (e *Engine) replay() {
	e.reset()

	// Deduplicate by op ID. Identical payloads collapse silently; conflicting
	// payloads resolve to the canonically smaller one, the loser is rejected.
	winner := map[string]Op{}
	var rejected []Rejection
	for _, op := range e.log {
		cur, ok := winner[op.ID]
		if !ok {
			winner[op.ID] = op
			continue
		}
		if cur == op {
			continue
		}
		if lessOp(op, cur) {
			rejected = append(rejected, Rejection{Op: cur, Reason: ReasonDuplicateOpID})
			winner[op.ID] = op
		} else {
			rejected = append(rejected, Rejection{Op: op, Reason: ReasonDuplicateOpID})
		}
	}

	ops := make([]Op, 0, len(winner))
	for _, op := range winner {
		ops = append(ops, op)
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Time != ops[j].Time {
			return ops[i].Time < ops[j].Time
		}
		return ops[i].ID < ops[j].ID
	})

	for _, op := range ops {
		if r, ok := e.apply(op); !ok {
			rejected = append(rejected, Rejection{Op: op, Reason: r})
		}
	}

	sort.Slice(rejected, func(i, j int) bool {
		a, b := rejected[i], rejected[j]
		if a.Op.Time != b.Op.Time {
			return a.Op.Time < b.Op.Time
		}
		if a.Op.ID != b.Op.ID {
			return a.Op.ID < b.Op.ID
		}
		return a.Reason < b.Reason
	})
	e.rejected = rejected
}

// apply tries one operation against the current replayed state.
func (e *Engine) apply(op Op) (RejectReason, bool) {
	switch op.Kind {
	case OpCreate:
		if op.Node == RootID || e.exists(op.Node) {
			return ReasonNodeExists, false
		}
		if !e.exists(op.Parent) {
			return ReasonParentMissing, false
		}
		if e.hidden(op.Parent) {
			return ReasonParentHidden, false
		}
		e.names[op.Node] = op.Name
		e.parent[op.Node] = op.Parent
		return "", true

	case OpMove:
		if op.Node == RootID {
			return ReasonRootImmovable, false
		}
		if !e.exists(op.Node) {
			return ReasonNodeMissing, false
		}
		if op.Parent == op.Node {
			return ReasonTargetIsSelf, false
		}
		if !e.exists(op.Parent) {
			return ReasonParentMissing, false
		}
		if e.hidden(op.Parent) {
			return ReasonParentHidden, false
		}
		if e.isDescendant(op.Parent, op.Node) {
			return ReasonTargetIsDesc, false
		}
		e.parent[op.Node] = op.Parent
		return "", true

	case OpRename:
		if !e.exists(op.Node) {
			return ReasonNodeMissing, false
		}
		e.names[op.Node] = op.Name
		return "", true

	case OpDelete:
		if op.Node == RootID {
			return ReasonRootUndeletable, false
		}
		if !e.exists(op.Node) {
			return ReasonNodeMissing, false
		}
		e.deleted[op.Node] = true
		return "", true
	}
	return ReasonNodeMissing, false
}

func (e *Engine) exists(id string) bool {
	_, ok := e.names[id]
	return ok
}

// isDescendant reports whether id is in the subtree rooted at ancestor.
// The parent map is always a forest rooted at RootID, so the walk terminates.
func (e *Engine) isDescendant(id, ancestor string) bool {
	for cur := id; ; {
		if cur == ancestor {
			return true
		}
		p, ok := e.parent[cur]
		if !ok {
			return false
		}
		cur = p
	}
}

// hidden reports whether id is invisible: itself deleted or any ancestor
// deleted. Visibility is derived solely from the delete markers.
func (e *Engine) hidden(id string) bool {
	for cur := id; ; {
		if e.deleted[cur] {
			return true
		}
		p, ok := e.parent[cur]
		if !ok {
			return false
		}
		cur = p
	}
}

// Parent returns the current parent of id.
func (e *Engine) Parent(id string) (string, bool) {
	p, ok := e.parent[id]
	return p, ok
}

// Visible reports whether id exists and is not hidden.
func (e *Engine) Visible(id string) bool {
	return e.exists(id) && !e.hidden(id)
}

// VisibleIDs returns all visible node IDs in sorted order.
func (e *Engine) VisibleIDs() []string {
	var ids []string
	for id := range e.names {
		if !e.hidden(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Path returns the ID-carrying path of id, e.g. /root(root)/docs(d1).
// Same-name siblings are unambiguous because every segment carries its ID.
func (e *Engine) Path(id string) (string, bool) {
	if !e.exists(id) {
		return "", false
	}
	var parts []string
	for cur := id; ; {
		parts = append(parts, fmt.Sprintf("%s(%s)", e.names[cur], cur))
		p, ok := e.parent[cur]
		if !ok {
			break
		}
		cur = p
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/"), true
}

// Tree renders the visible subtree as indented name(id) lines. Children are
// ordered by (name, ID) so the rendering is deterministic.
func (e *Engine) Tree() string {
	children := map[string][]string{}
	for id := range e.names {
		if p, ok := e.parent[id]; ok {
			children[p] = append(children[p], id)
		}
	}
	for _, ids := range children {
		sort.Slice(ids, func(i, j int) bool {
			a, b := ids[i], ids[j]
			if e.names[a] != e.names[b] {
				return e.names[a] < e.names[b]
			}
			return a < b
		})
	}
	var sb strings.Builder
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		if e.hidden(id) {
			return
		}
		fmt.Fprintf(&sb, "%s%s(%s)\n", strings.Repeat("  ", depth), e.names[id], id)
		for _, c := range children[id] {
			walk(c, depth+1)
		}
	}
	walk(RootID, 0)
	return sb.String()
}

// Rejections returns the rejected operations with reasons, in deterministic
// (Time, ID, Reason) order.
func (e *Engine) Rejections() []Rejection {
	return append([]Rejection(nil), e.rejected...)
}

// Snapshot is a deep, comparable copy of the replayed state.
type Snapshot struct {
	Names    map[string]string
	Parents  map[string]string
	Deleted  map[string]bool
	Rejected []Rejection
}

// Snapshot returns a deep copy of the current state.
func (e *Engine) Snapshot() Snapshot {
	s := Snapshot{
		Names:    map[string]string{},
		Parents:  map[string]string{},
		Deleted:  map[string]bool{},
		Rejected: append([]Rejection(nil), e.rejected...),
	}
	for k, v := range e.names {
		s.Names[k] = v
	}
	for k, v := range e.parent {
		s.Parents[k] = v
	}
	for k, v := range e.deleted {
		s.Deleted[k] = v
	}
	return s
}
