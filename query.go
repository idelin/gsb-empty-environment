package netsec

// Query serves a subject from the materialized cache. It never scans the
// source directly: it derives the subject's current role-closure class and
// policy digest, locates exactly that partition, and revalidates every binding
// (source incarnation/version, policy epoch, class and LSN) before returning.
func (h *Host) Query(viewName string, subject Subject) ([]ResultRow, *Entry, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	view, ok := h.views[viewName]
	if !ok {
		return nil, nil, ErrNoEntry
	}
	src, ok := h.sourceByID[view.SourceID]
	if !ok || src.superseded {
		return nil, nil, ErrSourceRebuilt
	}

	var key PartitionKey
	var expectedEpoch int64
	if view.Mode == OwnerSnapshot {
		key = ownerKey(view.DefinitionDigest)
	} else {
		policy, ok := h.policies[src.id]
		if !ok {
			return nil, nil, &StalePartitionError{Reason: "no RLS policy"}
		}
		class, err := h.roles.classDigest(subject.Role)
		if err != nil {
			return nil, nil, err
		}
		key = partitionKey(view.DefinitionDigest, policy.Digest(), class)
		expectedEpoch = policy.epoch
	}

	entry, ok := h.cache.lookup(key)
	if !ok {
		return nil, nil, ErrStalePartition
	}
	if !entry.Complete {
		return nil, nil, ErrStalePartition
	}
	if entry.SourceID != src.id {
		return nil, nil, &StalePartitionError{Reason: "source incarnation changed"}
	}
	if entry.SourceVersion != src.version {
		return nil, nil, &StalePartitionError{Reason: "source version changed"}
	}
	if view.Mode == PerPolicyPartition {
		if entry.PolicyDigest != key.PolicyDigest || entry.SubjectClass != key.SubjectClass {
			return nil, nil, &StalePartitionError{Reason: "policy or role changed"}
		}
		if entry.PolicyEpoch != expectedEpoch {
			return nil, nil, &StalePartitionError{Reason: "policy epoch advanced"}
		}
	}
	if entry.RefreshLSN <= 0 {
		return nil, nil, &StalePartitionError{Reason: "missing refresh LSN"}
	}

	out := make([]ResultRow, len(entry.Rows))
	for i, r := range entry.Rows {
		values := make(map[string]int64, len(r.Values))
		for k, v := range r.Values {
			values[k] = v
		}
		out[i] = ResultRow{Group: r.Group, Values: values}
	}
	return out, entry, nil
}
