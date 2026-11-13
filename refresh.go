package netsec

import "sort"

// FullRefresh materializes one partition from scratch under a fixed policy
// snapshot and publishes it atomically. For OwnerSnapshot the subject and
// policy arguments are ignored (no RLS is applied).
func (h *Host) FullRefresh(viewName string, subject Subject) (RefreshStats, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	view, src, snap, key, err := h.planRefresh(viewName, subject)
	if err != nil {
		return RefreshStats{}, err
	}

	entry, groups, err := h.buildFull(view, src, snap, key)
	if err != nil {
		return RefreshStats{}, err
	}
	if err := h.publish(view, key, entry, groups); err != nil {
		return RefreshStats{}, err
	}
	return RefreshStats{Key: key, LSN: entry.RefreshLSN, SourceVersion: entry.SourceVersion}, nil
}

// planRefresh resolves all security inputs for a refresh while the host lock
// is held, fixing the policy snapshot used for the whole build.
func (h *Host) planRefresh(viewName string, subject Subject) (*ViewDef, *Source, PolicySnapshot, PartitionKey, error) {
	view, ok := h.views[viewName]
	if !ok {
		return nil, nil, PolicySnapshot{}, PartitionKey{}, ErrNoEntry
	}
	src, ok := h.sourceByID[view.SourceID]
	if !ok || src.superseded {
		return nil, nil, PolicySnapshot{}, PartitionKey{}, ErrSourceRebuilt
	}

	if view.Mode == OwnerSnapshot {
		key := ownerKey(view.DefinitionDigest)
		return view, src, PolicySnapshot{}, key, nil
	}

	policy, ok := h.policies[src.id]
	if !ok {
		return nil, nil, PolicySnapshot{}, PartitionKey{}, ErrStalePartition
	}
	class, err := h.roles.classDigest(subject.Role)
	if err != nil {
		return nil, nil, PolicySnapshot{}, PartitionKey{}, err
	}
	closure, err := h.roles.Closure(subject.Role)
	if err != nil {
		return nil, nil, PolicySnapshot{}, PartitionKey{}, err
	}
	key := partitionKey(view.DefinitionDigest, policy.Digest(), class)
	snap := PolicySnapshot{
		SourceID:     src.id,
		PolicyDigest: policy.Digest(),
		Epoch:        policy.epoch,
		Predicate:    policy.Predicate,
		Subject:      subject,
		Roles:        closure,
	}
	return view, src, snap, key, nil
}

// buildFull scans every source row, applies RLS first and then the projection,
// accumulating the monoid state per group.
func (h *Host) buildFull(view *ViewDef, src *Source, snap PolicySnapshot, key PartitionKey) (*Entry, map[GroupKey]*groupAccum, error) {
	kinds := view.aggregateKinds()
	groups := map[GroupKey]*groupAccum{}

	keys := make([]string, 0, len(src.rows))
	for k := range src.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, rowKey := range keys {
		row := src.rows[rowKey]
		// RLS is evaluated on each original row before projection.
		if view.Mode == PerPolicyPartition && !snap.visible(row) {
			continue
		}
		gk, err := makeGroupKey(view.Projection.GroupBy, row)
		if err != nil {
			return nil, nil, err
		}
		g, ok := groups[gk]
		if !ok {
			g = newGroupAccum(kinds)
			groups[gk] = g
		}
		if err := g.add(view, row); err != nil {
			return nil, nil, err
		}
	}

	entry, err := h.renderEntry(view, src, snap, key, groups)
	if err != nil {
		return nil, nil, err
	}
	return entry, groups, nil
}

// visible applies the captured RLS predicate under the fixed subject.
func (s PolicySnapshot) visible(row Row) bool {
	if s.Predicate == nil {
		return true
	}
	return s.Predicate(row, s.Subject, s.Roles)
}

func (h *Host) renderEntry(view *ViewDef, src *Source, snap PolicySnapshot, key PartitionKey, groups map[GroupKey]*groupAccum) (*Entry, error) {
	rows := make([]ResultRow, 0, len(groups))
	for _, gk := range sortedGroupKeys(groups) {
		g := groups[gk]
		if g.rowCount == 0 {
			continue
		}
		r, err := g.finalize(view, gk)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	entry := &Entry{
		Key:           key,
		SourceID:      src.id,
		SourceVersion: src.version,
		SubjectClass:  key.SubjectClass,
		SubjectRole:   snap.Subject.Role,
		PolicyDigest:  key.PolicyDigest,
		PolicyEpoch:   snap.Epoch,
		RefreshLSN:    h.allocLSN(),
		Rows:          rows,
		Complete:      true,
	}
	return entry, nil
}

// publish runs the interruption hook and then performs the single atomic
// publication. On failure the previous entry stays in place (and remains
// unreadable if its epoch has advanced).
func (h *Host) publish(view *ViewDef, key PartitionKey, entry *Entry, groups map[GroupKey]*groupAccum) error {
	if h.refreshHook != nil {
		staging := cloneEntry(entry)
		staging.Complete = false
		h.pending.stage(key, staging)
		if err := h.refreshHook(view.Name); err != nil {
			return ErrRefreshFailed
		}
	}
	entry.Complete = true
	h.cache.swap(entry)
	h.accums[key] = groups
	return nil
}

func cloneEntry(e *Entry) *Entry {
	rows := make([]ResultRow, len(e.Rows))
	copy(rows, e.Rows)
	return &Entry{
		Key:           e.Key,
		SourceID:      e.SourceID,
		SourceVersion: e.SourceVersion,
		PolicyEpoch:   e.PolicyEpoch,
		SubjectClass:  e.SubjectClass,
		SubjectRole:   e.SubjectRole,
		PolicyDigest:  e.PolicyDigest,
		RefreshLSN:    e.RefreshLSN,
		Rows:          rows,
		Complete:      e.Complete,
	}
}
