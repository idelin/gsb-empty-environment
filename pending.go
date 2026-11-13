package netsec

import "sort"

// DiffKind classifies a single cache-key difference.
type DiffKind string

const (
	// DiffMissing means the pending entry lacks a group the live entry holds.
	DiffMissing DiffKind = "missing"
	// DiffExtra means the pending entry adds a group absent from the live entry.
	DiffExtra DiffKind = "extra"
	// DiffValue means the same group key materialized different values.
	DiffValue DiffKind = "value"
)

// KeyDiff is one difference found by the pending-review differential search.
type KeyDiff struct {
	Partition PartitionKey
	Kind      DiffKind
	Group     GroupKey
	Live      *ResultRow
	Pending   *ResultRow
}

// PendingCache holds refresh candidates awaiting review. Candidates are never
// queryable until approved, and approval re-runs all binding checks against
// the current policy/role snapshot.
type PendingCache struct {
	entries map[PartitionKey]*Entry
}

func newPendingCache() *PendingCache {
	return &PendingCache{entries: map[PartitionKey]*Entry{}}
}

func (p *PendingCache) stage(key PartitionKey, entry *Entry) {
	p.entries[key] = entry
}

// PendingEntry returns a staged candidate without publishing it.
func (h *Host) PendingEntry(key PartitionKey) (*Entry, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	e, ok := h.pending.entries[key]
	return e, ok
}

// DiscardPending drops a staged candidate.
func (h *Host) DiscardPending(key PartitionKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.pending.entries, key)
}

// SearchPendingKeyDiffs performs a set-difference search over the group keys
// of the staged candidate and the live entry, reporting missing groups, extra
// groups and value mismatches.
func (h *Host) SearchPendingKeyDiffs(key PartitionKey) ([]KeyDiff, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	candidate, ok := h.pending.entries[key]
	if !ok {
		return nil, ErrNoEntry
	}
	live := h.cache.entries[key]

	pendingRows := indexRows(candidate.Rows)
	var liveRows map[GroupKey]ResultRow
	if live != nil {
		liveRows = indexRows(live.Rows)
	} else {
		liveRows = map[GroupKey]ResultRow{}
	}

	var diffs []KeyDiff
	for gk, pRow := range pendingRows {
		lRow, ok := liveRows[gk]
		if !ok {
			pr := pRow
			diffs = append(diffs, KeyDiff{Partition: key, Kind: DiffExtra, Group: gk, Pending: &pr})
			continue
		}
		if !equalValues(lRow.Values, pRow.Values) {
			lr, pr := lRow, pRow
			diffs = append(diffs, KeyDiff{Partition: key, Kind: DiffValue, Group: gk, Live: &lr, Pending: &pr})
		}
	}
	for gk, lRow := range liveRows {
		if _, ok := pendingRows[gk]; !ok {
			lr := lRow
			diffs = append(diffs, KeyDiff{Partition: key, Kind: DiffMissing, Group: gk, Live: &lr})
		}
	}
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Group != diffs[j].Group {
			return diffs[i].Group < diffs[j].Group
		}
		return diffs[i].Kind < diffs[j].Kind
	})
	return diffs, nil
}

// ApprovePending promotes a reviewed candidate to the live cache only when it
// still binds to the current source version and policy epoch. A stale
// candidate is rejected rather than silently served.
func (h *Host) ApprovePending(key PartitionKey) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	candidate, ok := h.pending.entries[key]
	if !ok {
		return ErrNoEntry
	}
	view, ok := h.viewByDigest[key.ViewDigest]
	if !ok {
		return ErrNoEntry
	}
	src, ok := h.sourceByID[view.SourceID]
	if !ok || src.superseded {
		return ErrSourceRebuilt
	}
	if candidate.SourceVersion != src.version {
		return ErrStalePartition
	}
	if view.Mode == PerPolicyPartition {
		policy, ok := h.policies[src.id]
		if !ok || policy.Digest() != key.PolicyDigest || candidate.PolicyEpoch != policy.epoch {
			return ErrStalePartition
		}
		class, err := h.roles.classDigest(candidate.SubjectRole)
		if err != nil || class != key.SubjectClass {
			return ErrStalePartition
		}
	}
	candidate.Complete = true
	h.cache.swap(candidate)
	delete(h.pending.entries, key)
	return nil
}

func indexRows(rows []ResultRow) map[GroupKey]ResultRow {
	m := make(map[GroupKey]ResultRow, len(rows))
	for _, r := range rows {
		m[r.Group] = r
	}
	return m
}

func equalValues(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
