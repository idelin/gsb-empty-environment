package checkpoint

import (
	"fmt"
	"sort"
	"strings"
)

// MergeConflictError reports paths that both branches changed differently and
// that the merge operations did not resolve.
type MergeConflictError struct {
	Paths []string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf("%s: %s", ErrMergeConflict, strings.Join(e.Paths, ", "))
}

// Is reports merge conflicts for errors.Is.
func (e *MergeConflictError) Is(target error) bool { return target == ErrMergeConflict }

// delta describes how one path changed relative to the merge base.
type delta struct {
	block   Digest
	deleted bool
}

// Merge creates a two-parent snapshot. Changes that only one branch made
// (relative to the merge base) are auto-resolved into AutoOps. Paths both
// branches changed differently must be resolved explicitly by ops, otherwise
// a MergeConflictError is returned.
func (s *Store) Merge(id, left, right string, ops []Operation) (*Snapshot, error) {
	if left == right {
		return nil, fmt.Errorf("checkpoint: cannot merge %q with itself", left)
	}
	for _, p := range []string{left, right} {
		if _, ok := s.snapshots[p]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrMissingParent, p)
		}
	}
	baseID, err := s.mergeBase(left, right)
	if err != nil {
		return nil, err
	}
	memo := make(map[string]RefSet)
	baseSet, err := s.expand(baseID, memo)
	if err != nil {
		return nil, err
	}
	leftSet, err := s.expand(left, memo)
	if err != nil {
		return nil, err
	}
	rightSet, err := s.expand(right, memo)
	if err != nil {
		return nil, err
	}
	autoOps, err := resolveMerge(baseSet, leftSet, rightSet, ops)
	if err != nil {
		return nil, err
	}
	return s.commit(id, []string{left, right}, ops, autoOps, baseID)
}

// resolveMerge computes the auto-resolution ops for a three-way merge of full
// sets, validating that user ops resolve every conflicting path.
func resolveMerge(base, left, right RefSet, ops []Operation) ([]Operation, error) {
	leftDelta := diffSets(base, left)
	rightDelta := diffSets(base, right)

	conflicts := make(map[string]bool)
	for p, ld := range leftDelta {
		if rd, ok := rightDelta[p]; ok && rd != ld {
			conflicts[p] = true
		}
	}
	if len(conflicts) > 0 {
		resolved := make(map[string]bool)
		for _, op := range ops {
			for _, ref := range op.Refs {
				resolved[ref.Path] = true
			}
		}
		var missing []string
		for p := range conflicts {
			if !resolved[p] {
				missing = append(missing, p)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return nil, &MergeConflictError{Paths: missing}
		}
	}
	return autoOpsFromDeltas(leftDelta, rightDelta, conflicts), nil
}

// diffSets returns the changes that turn base into cur, per path.
func diffSets(base, cur RefSet) map[string]delta {
	d := make(map[string]delta)
	for p, b := range cur {
		if bb, ok := base[p]; !ok || bb != b {
			d[p] = delta{block: b}
		}
	}
	for p := range base {
		if _, ok := cur[p]; !ok {
			d[p] = delta{deleted: true}
		}
	}
	return d
}

// autoOpsFromDeltas builds deterministic ops (removes first, then adds, paths
// sorted) applying the union of both branches' non-conflicting changes.
func autoOpsFromDeltas(left, right map[string]delta, conflicts map[string]bool) []Operation {
	merged := make(map[string]delta, len(left)+len(right))
	for p, d := range left {
		if !conflicts[p] {
			merged[p] = d
		}
	}
	for p, d := range right {
		if !conflicts[p] {
			merged[p] = d
		}
	}
	var removes, adds []string
	for p, d := range merged {
		if d.deleted {
			removes = append(removes, p)
		} else {
			adds = append(adds, p)
		}
	}
	sort.Strings(removes)
	sort.Strings(adds)
	var ops []Operation
	if len(removes) > 0 {
		op := Operation{Kind: OpRemove}
		for _, p := range removes {
			op.Refs = append(op.Refs, Ref{Path: p})
		}
		ops = append(ops, op)
	}
	if len(adds) > 0 {
		op := Operation{Kind: OpAdd}
		for _, p := range adds {
			op.Refs = append(op.Refs, Ref{Path: p, Block: merged[p].block})
		}
		ops = append(ops, op)
	}
	return ops
}

// mergeBase finds a common ancestor of two snapshots via BFS. Histories are
// DAGs, so a common ancestor always exists for related branches.
func (s *Store) mergeBase(a, b string) (string, error) {
	ancestors := make(map[string]bool)
	queue := []string{a}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if ancestors[n] {
			continue
		}
		ancestors[n] = true
		if snap, ok := s.snapshots[n]; ok {
			queue = append(queue, snap.Parents...)
		}
	}
	visited := make(map[string]bool)
	queue = []string{b}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if visited[n] {
			continue
		}
		visited[n] = true
		if ancestors[n] {
			return n, nil
		}
		if snap, ok := s.snapshots[n]; ok {
			queue = append(queue, snap.Parents...)
		}
	}
	return "", ErrNoCommonAncestor
}
