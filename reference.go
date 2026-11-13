package netsec

import "sort"

// ReferenceRefresh independently recomputes a partition by iterating the
// small source table row by row: each row is first filtered through the RLS
// predicate and only then projected. It shares no state with the cache and is
// intended as an oracle for tests and audits.
func (h *Host) ReferenceRefresh(viewName string, subject Subject) ([]ResultRow, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	view, ok := h.views[viewName]
	if !ok {
		return nil, ErrNoEntry
	}
	src, ok := h.sourceByID[view.SourceID]
	if !ok || src.superseded {
		return nil, ErrSourceRebuilt
	}
	var predicate Predicate
	var closure []string
	if view.Mode == PerPolicyPartition {
		policy, ok := h.policies[src.id]
		if !ok {
			return nil, ErrStalePartition
		}
		predicate = policy.Predicate
		var err error
		closure, err = h.roles.Closure(subject.Role)
		if err != nil {
			return nil, err
		}
	}

	kinds := view.aggregateKinds()
	groups := map[GroupKey]*groupAccum{}
	keys := make([]string, 0, len(src.rows))
	for k := range src.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, rowKey := range keys {
		row := src.rows[rowKey]
		if predicate != nil && !predicate(row, subject, closure) {
			continue
		}
		gk, err := makeGroupKey(view.Projection.GroupBy, row)
		if err != nil {
			return nil, err
		}
		g, ok := groups[gk]
		if !ok {
			g = newGroupAccum(kinds)
			groups[gk] = g
		}
		if err := g.add(view, row); err != nil {
			return nil, err
		}
	}

	rows := make([]ResultRow, 0, len(groups))
	for _, gk := range sortedGroupKeys(groups) {
		r, err := groups[gk].finalize(view, gk)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// VerifyAgainstReference runs the row-by-row reference and compares it with
// the live cached partition. It returns the mismatches (empty means the cache
// agrees with the independent oracle).
func (h *Host) VerifyAgainstReference(viewName string, subject Subject) ([]KeyDiff, error) {
	expected, err := h.ReferenceRefresh(viewName, subject)
	if err != nil {
		return nil, err
	}
	actual, _, err := h.Query(viewName, subject)
	if err != nil {
		return nil, err
	}

	view, _ := h.View(viewName)
	key, err := h.keyFor(view, subject)
	if err != nil {
		return nil, err
	}
	exp := indexRows(expected)
	act := indexRows(actual)
	var diffs []KeyDiff
	for gk, e := range exp {
		a, ok := act[gk]
		if !ok {
			er := e
			diffs = append(diffs, KeyDiff{Partition: key, Kind: DiffMissing, Group: gk, Live: &er})
		} else if !equalValues(e.Values, a.Values) {
			er, ar := e, a
			diffs = append(diffs, KeyDiff{Partition: key, Kind: DiffValue, Group: gk, Live: &er, Pending: &ar})
		}
	}
	for gk, a := range act {
		if _, ok := exp[gk]; !ok {
			ar := a
			diffs = append(diffs, KeyDiff{Partition: key, Kind: DiffExtra, Group: gk, Pending: &ar})
		}
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Group < diffs[j].Group })
	return diffs, nil
}

// KeyFor exposes the partition key a subject currently resolves to, for tests
// and differential search.
func (h *Host) KeyFor(viewName string, subject Subject) (PartitionKey, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	view, ok := h.views[viewName]
	if !ok {
		return PartitionKey{}, ErrNoEntry
	}
	return h.keyFor(view, subject)
}

func (h *Host) keyFor(view *ViewDef, subject Subject) (PartitionKey, error) {
	if view.Mode == OwnerSnapshot {
		return ownerKey(view.DefinitionDigest), nil
	}
	src, ok := h.sourceByID[view.SourceID]
	if !ok || src.superseded {
		return PartitionKey{}, ErrSourceRebuilt
	}
	policy, ok := h.policies[src.id]
	if !ok {
		return PartitionKey{}, ErrStalePartition
	}
	class, err := h.roles.classDigest(subject.Role)
	if err != nil {
		return PartitionKey{}, err
	}
	return partitionKey(view.DefinitionDigest, policy.Digest(), class), nil
}
