// Package checkpoint implements an incremental, content-addressed snapshot
// store. Snapshots form a DAG: each snapshot records only the references it
// adds and removes relative to its parent(s), so many snapshots share the
// same underlying blocks. A snapshot digest binds its parent digests and its
// ordered operation list, making history tamper-evident.
package checkpoint

import (
	"errors"
	"fmt"
	"sort"
)

var (
	ErrMissingParent    = errors.New("checkpoint: missing parent snapshot")
	ErrSnapshotExists   = errors.New("checkpoint: snapshot already exists")
	ErrSnapshotMissing  = errors.New("checkpoint: snapshot not found")
	ErrSelfParent       = errors.New("checkpoint: snapshot cannot be its own parent")
	ErrDuplicateRef     = errors.New("checkpoint: duplicate path reference in operation")
	ErrUnknownBlock     = errors.New("checkpoint: operation references unknown block")
	ErrHasChildren      = errors.New("checkpoint: snapshot has descendants")
	ErrMergeConflict    = errors.New("checkpoint: unresolved merge conflict")
	ErrNoCommonAncestor = errors.New("checkpoint: snapshots have no common ancestor")
	ErrTooManyParents   = errors.New("checkpoint: use Merge for two-parent snapshots")
)

// OpKind is the kind of a reference operation.
type OpKind string

const (
	OpAdd    OpKind = "add"
	OpRemove OpKind = "remove"
)

// Ref binds a path to a block. For OpRemove only Path is significant.
type Ref struct {
	Path  string
	Block Digest
}

// Operation is an ordered batch of reference additions or removals.
type Operation struct {
	Kind OpKind
	Refs []Ref
}

// Snapshot is a node in the version DAG. BaseID and AutoOps are set only for
// merge snapshots: AutoOps replays the auto-resolved changes of both parents
// relative to the merge base, so expansion stays deterministic.
type Snapshot struct {
	ID      string
	Parents []string
	BaseID  string
	AutoOps []Operation
	Ops     []Operation
	Digest  Digest
}

// RefSet is a fully expanded view: path -> block.
type RefSet map[string]Digest

// Clone returns a deep copy of the set.
func (rs RefSet) Clone() RefSet {
	out := make(RefSet, len(rs))
	for p, b := range rs {
		out[p] = b
	}
	return out
}

// Store holds blocks and the snapshot DAG.
type Store struct {
	blocks    map[Digest][]byte
	snapshots map[string]*Snapshot
	children  map[string][]string
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		blocks:    make(map[Digest][]byte),
		snapshots: make(map[string]*Snapshot),
		children:  make(map[string][]string),
	}
}

// PutBlock stores data content-addressed and returns its digest. Storing the
// same content twice is a no-op, so blocks are shared across snapshots.
func (s *Store) PutBlock(data []byte) Digest {
	d := BlockDigest(data)
	if _, ok := s.blocks[d]; !ok {
		cp := make([]byte, len(data))
		copy(cp, data)
		s.blocks[d] = cp
	}
	return d
}

// Block returns the data for a digest.
func (s *Store) Block(d Digest) ([]byte, bool) {
	b, ok := s.blocks[d]
	return b, ok
}

// BlockCount reports how many distinct blocks are stored.
func (s *Store) BlockCount() int { return len(s.blocks) }

// Snapshot returns the snapshot with the given id.
func (s *Store) Snapshot(id string) (*Snapshot, bool) {
	snap, ok := s.snapshots[id]
	return snap, ok
}

// Heads returns the ids of snapshots without children (branch tips).
func (s *Store) Heads() []string {
	var heads []string
	for id := range s.snapshots {
		if len(s.children[id]) == 0 {
			heads = append(heads, id)
		}
	}
	sort.Strings(heads)
	return heads
}

// Commit creates a snapshot with zero or one parent. For two parents use Merge.
func (s *Store) Commit(id string, parents []string, ops []Operation) (*Snapshot, error) {
	if len(parents) > 1 {
		return nil, ErrTooManyParents
	}
	return s.commit(id, parents, ops, nil, "")
}

func (s *Store) commit(id string, parents []string, ops, autoOps []Operation, baseID string) (*Snapshot, error) {
	if id == "" {
		return nil, errors.New("checkpoint: empty snapshot id")
	}
	if _, ok := s.snapshots[id]; ok {
		return nil, fmt.Errorf("%w: %s", ErrSnapshotExists, id)
	}
	for _, p := range parents {
		if p == id {
			return nil, fmt.Errorf("%w: %s", ErrSelfParent, id)
		}
		if _, ok := s.snapshots[p]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrMissingParent, p)
		}
	}
	if err := s.validateOps(autoOps); err != nil {
		return nil, err
	}
	if err := s.validateOps(ops); err != nil {
		return nil, err
	}
	parentDigests := make([]Digest, len(parents))
	for i, p := range parents {
		parentDigests[i] = s.snapshots[p].Digest
	}
	snap := &Snapshot{
		ID:      id,
		Parents: append([]string(nil), parents...),
		BaseID:  baseID,
		AutoOps: autoOps,
		Ops:     ops,
		Digest:  computeDigest(parentDigests, autoOps, ops),
	}
	s.snapshots[id] = snap
	for _, p := range parents {
		s.children[p] = append(s.children[p], id)
	}
	return snap, nil
}

func (s *Store) validateOps(ops []Operation) error {
	for _, op := range ops {
		if op.Kind != OpAdd && op.Kind != OpRemove {
			return fmt.Errorf("checkpoint: unknown op kind %q", op.Kind)
		}
		seen := make(map[string]bool, len(op.Refs))
		for _, ref := range op.Refs {
			if ref.Path == "" {
				return errors.New("checkpoint: empty path in operation")
			}
			if seen[ref.Path] {
				return fmt.Errorf("%w: %s", ErrDuplicateRef, ref.Path)
			}
			seen[ref.Path] = true
			if op.Kind == OpAdd {
				if _, ok := s.blocks[ref.Block]; !ok {
					return fmt.Errorf("%w: %s", ErrUnknownBlock, ref.Block)
				}
			}
		}
	}
	return nil
}

// Expand materializes the full reference set of a snapshot by replaying the
// incremental operations along its ancestry.
func (s *Store) Expand(id string) (RefSet, error) {
	return s.expand(id, make(map[string]RefSet))
}

func (s *Store) expand(id string, memo map[string]RefSet) (RefSet, error) {
	if rs, ok := memo[id]; ok {
		return rs.Clone(), nil
	}
	snap, ok := s.snapshots[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSnapshotMissing, id)
	}
	var base RefSet
	var err error
	switch {
	case snap.BaseID != "":
		// Merge: replay from the merge base plus auto-resolved changes.
		base, err = s.expand(snap.BaseID, memo)
	case len(snap.Parents) > 0:
		base, err = s.expand(snap.Parents[0], memo)
	default:
		base = RefSet{}
	}
	if err != nil {
		return nil, err
	}
	out := base.Clone()
	applyOps(out, snap.AutoOps)
	applyOps(out, snap.Ops)
	memo[id] = out.Clone()
	return out, nil
}

func applyOps(set RefSet, ops []Operation) {
	for _, op := range ops {
		switch op.Kind {
		case OpAdd:
			for _, r := range op.Refs {
				set[r.Path] = r.Block
			}
		case OpRemove:
			for _, r := range op.Refs {
				delete(set, r.Path)
			}
		}
	}
}

// DownloadPlan returns the distinct blocks needed to materialize a snapshot,
// excluding blocks already present locally. A block referenced by many paths
// (or many snapshots) appears exactly once.
func (s *Store) DownloadPlan(id string, local []Digest) ([]Digest, error) {
	set, err := s.Expand(id)
	if err != nil {
		return nil, err
	}
	have := make(map[Digest]bool, len(local))
	for _, d := range local {
		have[d] = true
	}
	need := make(map[Digest]bool)
	for _, b := range set {
		if !have[b] {
			need[b] = true
		}
	}
	plan := make([]Digest, 0, len(need))
	for d := range need {
		plan = append(plan, d)
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i] < plan[j] })
	return plan, nil
}

// DeleteSnapshot removes a branch tip and garbage-collects blocks that no
// remaining snapshot references. Blocks still used by other branches are
// retained. Deleting a snapshot with children is rejected.
func (s *Store) DeleteSnapshot(id string) ([]Digest, error) {
	snap, ok := s.snapshots[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSnapshotMissing, id)
	}
	if len(s.children[id]) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrHasChildren, id)
	}
	delete(s.snapshots, id)
	delete(s.children, id)
	for _, p := range snap.Parents {
		s.children[p] = removeString(s.children[p], id)
	}
	return s.GC(), nil
}

// GC removes blocks not referenced by any snapshot's expanded set and returns
// the freed digests.
func (s *Store) GC() []Digest {
	used := make(map[Digest]bool)
	memo := make(map[string]RefSet)
	for id := range s.snapshots {
		set, err := s.expand(id, memo)
		if err != nil {
			continue
		}
		for _, b := range set {
			used[b] = true
		}
	}
	var freed []Digest
	for d := range s.blocks {
		if !used[d] {
			delete(s.blocks, d)
			freed = append(freed, d)
		}
	}
	sort.Slice(freed, func(i, j int) bool { return freed[i] < freed[j] })
	return freed
}

func removeString(in []string, v string) []string {
	out := in[:0]
	for _, x := range in {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
