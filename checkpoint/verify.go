package checkpoint

import (
	"fmt"
	"reflect"
	"sort"
)

// Verify cross-checks the store against a full-set version model: every
// version is recomputed as a complete reference set in topological order and
// compared with the incremental expansion. It reports structural problems
// (missing parents, cycles, dangling base links), digest tampering, duplicate
// path references, unknown blocks, and merge results inconsistent with the
// full-set model of both parents.
func (s *Store) Verify() []string {
	var issues []string

	// Structural checks: parent/base edges and per-op invariants.
	for _, id := range s.sortedIDs() {
		snap := s.snapshots[id]
		for _, p := range snap.Parents {
			if p == id {
				issues = append(issues, fmt.Sprintf("snapshot %s is its own parent", id))
				continue
			}
			if _, ok := s.snapshots[p]; !ok {
				issues = append(issues, fmt.Sprintf("snapshot %s has missing parent %s", id, p))
			}
		}
		if snap.BaseID != "" {
			if _, ok := s.snapshots[snap.BaseID]; !ok {
				issues = append(issues, fmt.Sprintf("snapshot %s has missing merge base %s", id, snap.BaseID))
			}
		}
		issues = append(issues, s.checkOps(id, snap.AutoOps)...)
		issues = append(issues, s.checkOps(id, snap.Ops)...)
	}

	// Acyclicity + topological order via Kahn's algorithm.
	order, cyclic := s.topoOrder()
	if cyclic {
		issues = append(issues, "snapshot graph contains a cycle")
	}

	// Digest integrity: recompute and compare.
	for _, id := range s.sortedIDs() {
		snap := s.snapshots[id]
		parentDigests := make([]Digest, 0, len(snap.Parents))
		ok := true
		for _, p := range snap.Parents {
			parent, exists := s.snapshots[p]
			if !exists {
				ok = false
				break
			}
			parentDigests = append(parentDigests, parent.Digest)
		}
		if !ok {
			continue
		}
		if want := computeDigest(parentDigests, snap.AutoOps, snap.Ops); want != snap.Digest {
			issues = append(issues, fmt.Sprintf("snapshot %s digest mismatch: stored %s, recomputed %s", id, snap.Digest, want))
		}
	}

	// Full-set model replay in topological order.
	model := make(map[string]RefSet, len(order))
	for _, id := range order {
		snap := s.snapshots[id]
		var base RefSet
		switch {
		case snap.BaseID != "":
			base = model[snap.BaseID]
		case len(snap.Parents) > 0:
			base = model[snap.Parents[0]]
		}
		if base == nil {
			base = RefSet{}
		}
		m := base.Clone()
		applyOps(m, snap.AutoOps)
		applyOps(m, snap.Ops)
		model[id] = m

		got, err := s.Expand(id)
		if err != nil {
			issues = append(issues, fmt.Sprintf("snapshot %s cannot expand: %v", id, err))
			continue
		}
		if !reflect.DeepEqual(got, m) {
			issues = append(issues, fmt.Sprintf("snapshot %s expansion disagrees with full-set model", id))
		}

		// Independent merge check: recompute the merge from the parents'
		// full sets and compare with the modeled result.
		if len(snap.Parents) == 2 && snap.BaseID != "" {
			left := model[snap.Parents[0]]
			right := model[snap.Parents[1]]
			baseSet := model[snap.BaseID]
			if left != nil && right != nil && baseSet != nil {
				autoOps, err := resolveMerge(baseSet, left, right, snap.Ops)
				if err != nil {
					issues = append(issues, fmt.Sprintf("snapshot %s merge no longer resolves: %v", id, err))
				} else {
					want := baseSet.Clone()
					applyOps(want, autoOps)
					applyOps(want, snap.Ops)
					if !reflect.DeepEqual(want, m) {
						issues = append(issues, fmt.Sprintf("snapshot %s merge result disagrees with full-set merge of parents", id))
					}
				}
			}
		}
	}
	return issues
}

func (s *Store) checkOps(id string, ops []Operation) []string {
	var issues []string
	for _, op := range ops {
		seen := make(map[string]bool, len(op.Refs))
		for _, ref := range op.Refs {
			if seen[ref.Path] {
				issues = append(issues, fmt.Sprintf("snapshot %s has duplicate reference to path %q in one operation", id, ref.Path))
			}
			seen[ref.Path] = true
			if op.Kind == OpAdd {
				if _, ok := s.blocks[ref.Block]; !ok {
					issues = append(issues, fmt.Sprintf("snapshot %s references unknown block %s for path %q", id, ref.Block, ref.Path))
				}
			}
		}
	}
	return issues
}

// topoOrder returns snapshot ids in dependency order (parents first). The
// second result is true if the graph contains a cycle.
func (s *Store) topoOrder() ([]string, bool) {
	children := make(map[string][]string, len(s.snapshots))
	indegree := make(map[string]int, len(s.snapshots))
	for id, snap := range s.snapshots {
		n := 0
		for _, p := range snap.Parents {
			if _, ok := s.snapshots[p]; ok {
				children[p] = append(children[p], id)
				n++
			}
		}
		indegree[id] = n
	}
	var queue []string
	for id, d := range indegree {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)
	var order []string
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		order = append(order, n)
		for _, c := range children[n] {
			indegree[c]--
			if indegree[c] == 0 {
				queue = append(queue, c)
			}
		}
	}
	return order, len(order) != len(s.snapshots)
}

func (s *Store) sortedIDs() []string {
	ids := make([]string, 0, len(s.snapshots))
	for id := range s.snapshots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
