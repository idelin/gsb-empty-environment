package groupx

// snapshot is a point-in-time, tenant-private copy of all mutable state.
type snapshot struct {
	seen      map[string]struct{}
	groups    map[string]Aggregate
	cache     *lru
	watermark int64
	highSeq   uint64
}

func takeSnapshot(t *tenantState) *snapshot {
	seen := make(map[string]struct{}, len(t.seen))
	for id := range t.seen {
		seen[id] = struct{}{}
	}
	groups := make(map[string]Aggregate, len(t.groups))
	for g, agg := range t.groups {
		groups[g] = agg.clone()
	}
	return &snapshot{
		seen:      seen,
		groups:    groups,
		cache:     t.cache.clone(),
		watermark: t.watermark,
		highSeq:   t.highSeq,
	}
}

// restore deep-copies the snapshot back into live state so the stored
// snapshot remains immutable and can be restored repeatedly.
func (s *snapshot) restore() *tenantState {
	seen := make(map[string]struct{}, len(s.seen))
	for id := range s.seen {
		seen[id] = struct{}{}
	}
	groups := make(map[string]Aggregate, len(s.groups))
	for g, agg := range s.groups {
		groups[g] = agg.clone()
	}
	return &tenantState{
		seen:      seen,
		groups:    groups,
		cache:     s.cache.clone(),
		watermark: s.watermark,
		highSeq:   s.highSeq,
	}
}
