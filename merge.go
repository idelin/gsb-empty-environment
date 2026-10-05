// Package treemerge implements a deterministic merger for an offline file
// tree. Concurrent edits (create/move/rename/delete) from multiple replicas
// are merged by taking the union of all operations and replaying them in a
// total order of (logical time, operation ID). Every operation is preserved:
// moves that are invalid at replay time (missing parent, self/descendant
// target, immovable root, ...) are recorded in a rejection list with a
// reason instead of being dropped silently. The displayed tree is therefore
// always acyclic.
//
// The semantics are deliberately total-order replay: no incremental
// rollback machinery is used to hide late-arriving early operations. A
// checkpoint simply stores the operation log, so recovery from a checkpoint
// plus subsequent operations replays the same union and yields the same
// parent map and rejection list regardless of batching or arrival order.
package treemerge

import (
	"fmt"
	"sort"
	"strings"
)

// RootID is the stable ID of the immovable root node.
const RootID = "root"

// OpKind enumerates the supported operations.
type OpKind string

const (
	OpCreate OpKind = "create"
	OpMove   OpKind = "move"
	OpRename OpKind = "rename"
	OpDelete OpKind = "delete"
)

// Op is a single tree operation. Node IDs are stable: renames only change
// the display name. Delete is an independent visibility marker; the node
// and its subtree keep existing and can still be moved or renamed.
type Op struct {
	ID     string // unique operation ID
	Kind   OpKind
	Node   string // target node ID
	Parent string // create/move: new parent ID
	Name   string // create/rename: display name
	Time   int64  // logical (Lamport) timestamp
}

// key is a canonical serialization covering every field; used for exact
// duplicate detection and deterministic conflict resolution.
func (o Op) key() string {
	return fmt.Sprintf("%d|%s|%s|%s|%s|%s", o.Time, o.ID, o.Kind, o.Node, o.Parent, o.Name)
}

// RejectReason explains why an operation was rejected during replay.
type RejectReason string

const (
	ReasonDuplicateOpID    RejectReason = "duplicate_op_id"
	ReasonUnknownNode      RejectReason = "unknown_node"
	ReasonNodeExists       RejectReason = "node_exists"
	ReasonReservedRoot     RejectReason = "reserved_root"
	ReasonImmovableRoot    RejectReason = "immovable_root"
	ReasonMissingParent    RejectReason = "missing_parent"
	ReasonSelfOrDescendant RejectReason = "self_or_descendant"
)

// Rejection pairs an operation with the reason it did not apply.
type Rejection struct {
	Op     Op
	Reason RejectReason
}

// Merger accumulates operations. It is safe to Apply the same operation
// more than once and to Apply operations in any order and any batching:
// Result always replays the deterministic union.
type Merger struct {
	ops []Op // full submitted log, including duplicates
}

// New returns an empty Merger.
func New() *Merger { return &Merger{} }

// Apply adds operations to the merge set.
func (m *Merger) Apply(ops ...Op) { m.ops = append(m.ops, ops...) }

// Result replays the union of all applied operations in total order.
func (m *Merger) Result() *Result { return replay(m.ops) }

// Checkpoint captures everything needed to resume merging later.
type Checkpoint struct {
	Ops []Op
}

// Checkpoint snapshots the operation log.
func (m *Merger) Checkpoint() Checkpoint {
	cp := Checkpoint{Ops: make([]Op, len(m.ops))}
	copy(cp.Ops, m.ops)
	return cp
}

// FromCheckpoint restores a Merger from a checkpoint. Applying subsequent
// operations and calling Result yields exactly the same parent map and
// rejection list as replaying the full union from scratch, because the
// checkpoint carries the operation log and replay is total-order.
func FromCheckpoint(cp Checkpoint) *Merger {
	m := &Merger{ops: make([]Op, len(cp.Ops))}
	copy(m.ops, cp.Ops)
	return m
}

// Result is the outcome of a deterministic replay.
type Result struct {
	parents  map[string]string // node -> parent; root -> ""
	names    map[string]string
	deleted  map[string]bool
	rejected []Rejection
}

// Parents returns the final parent map (root maps to "").
func (r *Result) Parents() map[string]string {
	out := make(map[string]string, len(r.parents))
	for k, v := range r.parents {
		out[k] = v
	}
	return out
}

// Names returns the final display-name map.
func (r *Result) Names() map[string]string {
	out := make(map[string]string, len(r.names))
	for k, v := range r.names {
		out[k] = v
	}
	return out
}

// Deleted reports whether the node itself carries the delete marker.
func (r *Result) Deleted(id string) bool { return r.deleted[id] }

// Rejected returns the rejection list in deterministic order.
func (r *Result) Rejected() []Rejection {
	out := make([]Rejection, len(r.rejected))
	copy(out, r.rejected)
	return out
}

// Visible reports whether a node is shown: it must exist, not be deleted,
// and have no deleted ancestor.
func (r *Result) Visible(id string) bool {
	cur := id
	seen := map[string]bool{}
	for {
		if _, ok := r.names[cur]; !ok {
			return false
		}
		if r.deleted[cur] {
			return false
		}
		if cur == RootID {
			return true
		}
		if seen[cur] {
			return false // defensive: replay never builds cycles
		}
		seen[cur] = true
		cur = r.parents[cur]
	}
}

// Path returns the slash-separated path with stable IDs, e.g.
// "/root#root/documents#d1/file#f2". Same-name siblings stay distinct
// because every segment carries its node ID. The second return value is
// false if the node does not exist.
func (r *Result) Path(id string) (string, bool) {
	if _, ok := r.names[id]; !ok {
		return "", false
	}
	var segs []string
	seen := map[string]bool{}
	cur := id
	for {
		if seen[cur] {
			return "", false // defensive: replay never builds cycles
		}
		seen[cur] = true
		segs = append([]string{r.names[cur] + "#" + cur}, segs...)
		if cur == RootID {
			break
		}
		cur = r.parents[cur]
	}
	return "/" + strings.Join(segs, "/"), true
}

// Tree renders the visible tree, children ordered by (name, ID).
func (r *Result) Tree() string {
	children := map[string][]string{}
	for id, p := range r.parents {
		if id != RootID {
			children[p] = append(children[p], id)
		}
	}
	for _, kids := range children {
		sort.Slice(kids, func(i, j int) bool {
			a, b := kids[i], kids[j]
			if r.names[a] != r.names[b] {
				return r.names[a] < r.names[b]
			}
			return a < b
		})
	}
	var b strings.Builder
	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		if !r.Visible(id) {
			return
		}
		fmt.Fprintf(&b, "%s%s#%s\n", strings.Repeat("  ", depth), r.names[id], id)
		for _, k := range children[id] {
			walk(k, depth+1)
		}
	}
	walk(RootID, 0)
	return b.String()
}

// replay normalizes the operation set (exact-duplicate removal and
// deterministic duplicate-ID conflict resolution), sorts the winners by
// (Time, ID), and applies them one by one.
func replay(all []Op) *Result {
	winners := map[string]Op{}
	var losers []Op
	for _, op := range all {
		w, ok := winners[op.ID]
		if !ok {
			winners[op.ID] = op
			continue
		}
		if op.key() == w.key() {
			continue // exact retransmission
		}
		// Conflicting operations sharing an ID: keep the canonically
		// smaller one so the outcome is arrival-order independent.
		if op.key() < w.key() {
			losers = append(losers, w)
			winners[op.ID] = op
		} else {
			losers = append(losers, op)
		}
	}

	ordered := make([]Op, 0, len(winners))
	for _, op := range winners {
		ordered = append(ordered, op)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Time != ordered[j].Time {
			return ordered[i].Time < ordered[j].Time
		}
		return ordered[i].ID < ordered[j].ID
	})

	st := newReplayState()
	for _, op := range ordered {
		st.apply(op)
	}

	sort.Slice(losers, func(i, j int) bool {
		if losers[i].Time != losers[j].Time {
			return losers[i].Time < losers[j].Time
		}
		if losers[i].ID != losers[j].ID {
			return losers[i].ID < losers[j].ID
		}
		return losers[i].key() < losers[j].key()
	})
	for _, op := range losers {
		st.rejected = append(st.rejected, Rejection{Op: op, Reason: ReasonDuplicateOpID})
	}
	return st.Result()
}

// replayState is the mutable state threaded through the ordered replay.
type replayState struct {
	parents  map[string]string
	names    map[string]string
	deleted  map[string]bool
	rejected []Rejection
}

func newReplayState() *replayState {
	return &replayState{
		parents: map[string]string{RootID: ""},
		names:   map[string]string{RootID: "root"},
		deleted: map[string]bool{},
	}
}

func (s *replayState) Result() *Result {
	return &Result{
		parents:  s.parents,
		names:    s.names,
		deleted:  s.deleted,
		rejected: s.rejected,
	}
}

func (s *replayState) exists(id string) bool {
	_, ok := s.names[id]
	return ok
}

func (s *replayState) reject(op Op, reason RejectReason) {
	s.rejected = append(s.rejected, Rejection{Op: op, Reason: reason})
}

// isDescendant reports whether candidate is node itself or lies in node's
// subtree according to the current parent map.
func (s *replayState) isDescendant(candidate, node string) bool {
	cur := candidate
	seen := map[string]bool{}
	for cur != "" {
		if cur == node {
			return true
		}
		if seen[cur] {
			return false
		}
		seen[cur] = true
		cur = s.parents[cur]
	}
	return false
}

func (s *replayState) apply(op Op) {
	switch op.Kind {
	case OpCreate:
		if op.Node == RootID {
			s.reject(op, ReasonReservedRoot)
			return
		}
		if s.exists(op.Node) {
			s.reject(op, ReasonNodeExists)
			return
		}
		if !s.exists(op.Parent) {
			s.reject(op, ReasonMissingParent)
			return
		}
		s.parents[op.Node] = op.Parent
		s.names[op.Node] = op.Name
	case OpMove:
		if op.Node == RootID {
			s.reject(op, ReasonImmovableRoot)
			return
		}
		if !s.exists(op.Node) {
			s.reject(op, ReasonUnknownNode)
			return
		}
		if !s.exists(op.Parent) {
			s.reject(op, ReasonMissingParent)
			return
		}
		// Deleted parents still exist: moving into a hidden subtree is
		// legal, the moved node simply becomes hidden too.
		if s.isDescendant(op.Parent, op.Node) {
			s.reject(op, ReasonSelfOrDescendant)
			return
		}
		s.parents[op.Node] = op.Parent
	case OpRename:
		if !s.exists(op.Node) {
			s.reject(op, ReasonUnknownNode)
			return
		}
		s.names[op.Node] = op.Name
	case OpDelete:
		if op.Node == RootID {
			s.reject(op, ReasonImmovableRoot)
			return
		}
		if !s.exists(op.Node) {
			s.reject(op, ReasonUnknownNode)
			return
		}
		s.deleted[op.Node] = true
	default:
		s.reject(op, ReasonUnknownNode)
	}
}
