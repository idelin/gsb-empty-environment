package checkpoint

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrMissingParent is returned when a commit references a parent
	// snapshot that does not exist. Because parents must already exist,
	// the DAG is acyclic by construction.
	ErrMissingParent = errors.New("checkpoint: missing parent snapshot")
	// ErrDuplicateSnapshot is returned when a snapshot ID is reused.
	ErrDuplicateSnapshot = errors.New("checkpoint: duplicate snapshot ID")
	// ErrUnknownSnapshot is returned when expanding an unknown snapshot.
	ErrUnknownSnapshot = errors.New("checkpoint: unknown snapshot")
	// ErrUnknownChunk is returned when an add references a chunk that
	// was never stored.
	ErrUnknownChunk = errors.New("checkpoint: unknown chunk")
	// ErrUnknownRef is returned when a remove targets a name that is not
	// present in the merged parent state.
	ErrUnknownRef = errors.New("checkpoint: remove of unknown reference")
	// ErrSelfParent is returned when a snapshot lists itself as parent.
	ErrSelfParent = errors.New("checkpoint: snapshot cannot be its own parent")
)

// MergeConflictError reports references that both merge sides changed
// in incompatible ways relative to their merge base.
type MergeConflictError struct {
	Names []string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf("checkpoint: merge conflict on %v", e.Names)
}

// Store keeps chunks and the snapshot DAG.
type Store struct {
	chunks   map[Hash][]byte
	snaps    map[string]*Snapshot
	expanded map[string]map[string]Hash // memoized Expand results
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		chunks:   make(map[Hash][]byte),
		snaps:    make(map[string]*Snapshot),
		expanded: make(map[string]map[string]Hash),
	}
}

// PutChunk stores chunk data and returns its content address.
// Storing the same content twice is a no-op (chunks are shared).
func (s *Store) PutChunk(data []byte) Hash {
	h := HashBytes(data)
	if _, ok := s.chunks[h]; !ok {
		cp := make([]byte, len(data))
		copy(cp, data)
		s.chunks[h] = cp
	}
	return h
}

// Chunk returns the stored data for h.
func (s *Store) Chunk(h Hash) ([]byte, bool) {
	b, ok := s.chunks[h]
	return b, ok
}

// Snapshot returns the snapshot with the given ID.
func (s *Store) Snapshot(id string) (*Snapshot, bool) {
	sn, ok := s.snaps[id]
	return sn, ok
}

// Commit creates an immutable incremental snapshot.
//
// parents must be 0, 1 or 2 existing snapshot IDs. With two parents the
// commit is a merge: both sides are reconciled against their merge base
// and a *MergeConflictError is returned if they disagree. ops are
// validated against the merged parent state and applied in order.
func (s *Store) Commit(id string, parents []string, ops []Op) (*Snapshot, error) {
	if id == "" {
		return nil, errors.New("checkpoint: empty snapshot ID")
	}
	if _, ok := s.snaps[id]; ok {
		return nil, ErrDuplicateSnapshot
	}
	if len(parents) > 2 {
		return nil, errors.New("checkpoint: at most two parents supported")
	}
	parentDigests := make([]Hash, len(parents))
	for i, p := range parents {
		if p == id {
			return nil, ErrSelfParent
		}
		pn, ok := s.snaps[p]
		if !ok {
			return nil, fmt.Errorf("%v: %q", ErrMissingParent, p)
		}
		parentDigests[i] = pn.Digest
	}

	// Merged parent state the ops apply to.
	var base map[string]Hash
	var err error
	switch len(parents) {
	case 0:
		base = make(map[string]Hash)
	case 1:
		base, err = s.Expand(parents[0])
	default:
		base, err = s.mergeParents(parents[0], parents[1])
	}
	if err != nil {
		return nil, err
	}

	// Validate ops against the parent state.
	state := copyState(base)
	for _, op := range ops {
		switch op.Kind {
		case OpAdd:
			if _, ok := s.chunks[op.Chunk]; !ok {
				return nil, fmt.Errorf("%v: %s", ErrUnknownChunk, op.Chunk)
			}
			state[op.Name] = op.Chunk
		case OpRemove:
			if _, ok := state[op.Name]; !ok {
				return nil, fmt.Errorf("%v: %q", ErrUnknownRef, op.Name)
			}
			delete(state, op.Name)
		default:
			return nil, fmt.Errorf("checkpoint: invalid op kind %d", op.Kind)
		}
	}

	sn := &Snapshot{
		ID:      id,
		Parents: append([]string(nil), parents...),
		Ops:     append([]Op(nil), ops...),
		Digest:  computeDigest(parentDigests, ops),
	}
	s.snaps[id] = sn
	s.expanded[id] = state
	return sn, nil
}

// Expand materializes the full reference set of a snapshot by replaying
// the DAG: parent states are merged, then the snapshot's add/remove ops
// are applied in order. Results are memoized.
func (s *Store) Expand(id string) (map[string]Hash, error) {
	if st, ok := s.expanded[id]; ok {
		return copyState(st), nil
	}
	sn, ok := s.snaps[id]
	if !ok {
		return nil, fmt.Errorf("%v: %q", ErrUnknownSnapshot, id)
	}
	var state map[string]Hash
	switch len(sn.Parents) {
	case 0:
		state = make(map[string]Hash)
	case 1:
		p, err := s.Expand(sn.Parents[0])
		if err != nil {
			return nil, err
		}
		state = p
	default:
		m, err := s.mergeParents(sn.Parents[0], sn.Parents[1])
		if err != nil {
			return nil, err
		}
		state = m
	}
	for _, op := range sn.Ops {
		if op.Kind == OpAdd {
			state[op.Name] = op.Chunk
		} else {
			delete(state, op.Name)
		}
	}
	s.expanded[id] = state
	return copyState(state), nil
}

// ReachableChunks returns the set of chunks referenced by any of the
// given heads (each head stands for its whole branch history).
func (s *Store) ReachableChunks(heads []string) (map[Hash]bool, error) {
	live := make(map[Hash]bool)
	for _, h := range heads {
		st, err := s.Expand(h)
		if err != nil {
			return nil, err
		}
		for _, c := range st {
			live[c] = true
		}
	}
	return live, nil
}

// GC deletes every chunk not reachable from any of the given heads.
// A chunk dropped by one branch survives as long as another branch head
// still references it. It returns the deleted chunk hashes, sorted.
func (s *Store) GC(heads []string) ([]Hash, error) {
	live, err := s.ReachableChunks(heads)
	if err != nil {
		return nil, err
	}
	var removed []Hash
	for h := range s.chunks {
		if !live[h] {
			delete(s.chunks, h)
			removed = append(removed, h)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return lessHash(removed[i], removed[j]) })
	return removed, nil
}

// ValidateDAG checks structural integrity: every parent exists and the
// graph is acyclic. Parents must pre-exist at commit time, so a valid
// store is acyclic by construction; this is a defensive audit.
func (s *Store) ValidateDAG() error {
	const (
		white = 0 // unvisited
		gray  = 1 // on stack
		black = 2 // done
	)
	color := make(map[string]int, len(s.snaps))
	var visit func(id string) error
	visit = func(id string) error {
		switch color[id] {
		case gray:
			return fmt.Errorf("checkpoint: cycle through %q", id)
		case black:
			return nil
		}
		color[id] = gray
		sn := s.snaps[id]
		for _, p := range sn.Parents {
			if _, ok := s.snaps[p]; !ok {
				return fmt.Errorf("%v: %q (from %q)", ErrMissingParent, p, id)
			}
			if err := visit(p); err != nil {
				return err
			}
		}
		color[id] = black
		return nil
	}
	for id := range s.snaps {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func copyState(in map[string]Hash) map[string]Hash {
	out := make(map[string]Hash, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func lessHash(a, b Hash) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
