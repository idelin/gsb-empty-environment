package netsec

// Merge combines independently collected shards into one selector.
//
// The returned selector carries the union of every accepted event and every
// withdrawal tombstone. Duplicate EventIDs are reconciled exactly as on a
// single host: identical copies collapse into one (idempotent retransmission),
// while copies whose content differs cause Merge to fail with ErrConflict.
// All shards must share the same HMAC key and caps; otherwise Merge returns
// ErrInvalidConfig.
//
// Because each shard retains its full accepted-event state rather than a
// pre-trimmed view, Merge(shards...).Trim() is bit-for-bit equivalent to
// ingesting the whole collection into one selector and trimming it.
func Merge(shards ...*Selector) (*Selector, error) {
	if len(shards) == 0 {
		return nil, ErrInvalidConfig
	}

	first := shards[0]
	first.mu.Lock()
	key := append([]byte(nil), first.key...)
	localCap := first.localCap
	globalCap := first.globalCap
	first.mu.Unlock()

	for _, shard := range shards[1:] {
		shard.mu.Lock()
		sameKey := string(shard.key) == string(key)
		sameCaps := shard.localCap == localCap && shard.globalCap == globalCap
		shard.mu.Unlock()
		if !sameKey || !sameCaps {
			return nil, ErrInvalidConfig
		}
	}

	merged, err := NewSelector(key, localCap, globalCap)
	if err != nil {
		return nil, err
	}

	for _, shard := range shards {
		shard.mu.Lock()

		// Collect every withdrawal signal (both active tombstones and orphan
		// requests for unseen events) into one pending set.
		for id := range shard.tombstoneOnly {
			merged.tombstoneOnly[id] = struct{}{}
		}
		for id := range shard.withdrawn {
			merged.tombstoneOnly[id] = struct{}{}
		}

		for id, e := range shard.entries {
			if existing, ok := merged.entries[id]; ok {
				if !recordsEqual(existing.record, e.record) {
					shard.mu.Unlock()
					return nil, ErrConflict
				}
				continue
			}

			merged.entries[id] = e

			gk := groupKey{
				scope: userWindow{userID: e.record.UserID, window: e.record.Window},
				group: e.record.Group,
			}
			groupMembers, ok := merged.groups[gk]
			if !ok {
				groupMembers = make(map[string]entry)
				merged.groups[gk] = groupMembers
			}
			groupMembers[id] = e

			uwMembers, ok := merged.userWindow[gk.scope]
			if !ok {
				uwMembers = make(map[string]entry)
				merged.userWindow[gk.scope] = uwMembers
			}
			uwMembers[id] = e
		}

		// Promote pending tombstones whose events are now known.
		for id := range merged.tombstoneOnly {
			if _, known := merged.entries[id]; known {
				delete(merged.tombstoneOnly, id)
				merged.withdrawn[id] = struct{}{}
			}
		}
		shard.mu.Unlock()
	}

	return merged, nil
}
