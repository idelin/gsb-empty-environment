package reuseauth

// ChangeSet is the minimal navigation-visible mutation for one request in one
// epoch. It never contains a DownloadID or evidence hashes for ranges that
// were not shared with that navigation.
type ChangeSet struct {
	NavigationID NavID         `json:"navigation_id"`
	RequestID    RequestID     `json:"request_id"`
	Epoch        Epoch         `json:"epoch"`
	Added        []RangeDigest `json:"added,omitempty"`
	Removed      []ByteRange   `json:"removed,omitempty"`
	Length       *int64        `json:"length,omitempty"`
	Completed    bool          `json:"completed,omitempty"`
	Failed       bool          `json:"failed,omitempty"`
	Code         string        `json:"code,omitempty"`
	Reset        bool          `json:"reset,omitempty"`
}

// Empty reports whether the change set has no visible mutation.
func (c ChangeSet) Empty() bool {
	return len(c.Added) == 0 && len(c.Removed) == 0 && c.Length == nil && !c.Completed && !c.Failed && !c.Reset
}

// VisibleState is the per-navigation, per-request state exposed by Broker.
type VisibleState struct {
	Epoch       Epoch         `json:"epoch"`
	Ranges      []RangeDigest `json:"ranges,omitempty"`
	Length      *int64        `json:"length,omitempty"`
	Completed   bool          `json:"completed,omitempty"`
	Failed      bool          `json:"failed,omitempty"`
	FailureCode string        `json:"failure_code,omitempty"`
}

func rangeKey(rd RangeDigest) string { return rd.Range.String() + ":" + rd.Digest.String() }

// DiffRanges returns exact digest-additions and minimal coalesced removals.
func DiffRanges(before, after []RangeDigest) (added []RangeDigest, removed []ByteRange) {
	beforeByKey := make(map[string]RangeDigest, len(before))
	for _, rd := range before {
		beforeByKey[rangeKey(rd)] = rd
	}
	afterByKey := make(map[string]RangeDigest, len(after))
	for _, rd := range after {
		afterByKey[rangeKey(rd)] = rd
		if _, existed := beforeByKey[rangeKey(rd)]; !existed {
			added = append(added, rd)
		}
	}
	for _, rd := range before {
		if _, stillPresent := afterByKey[rangeKey(rd)]; !stillPresent {
			removed = append(removed, rd.Range)
		}
	}
	added = canonicalRangeDigests(added)
	removed = mergeRanges(removed)
	return added, removed
}

// DiffState computes the minimal change from before to after.
func DiffState(navID NavID, requestID RequestID, epoch Epoch, before, after VisibleState) ChangeSet {
	added, removed := DiffRanges(before.Ranges, after.Ranges)
	cs := ChangeSet{
		NavigationID: navID,
		RequestID:    requestID,
		Epoch:        epoch,
		Added:        added,
		Removed:      removed,
		Completed:    after.Completed && !before.Completed,
		Failed:       after.Failed && !before.Failed,
	}
	if after.Length != nil && (before.Length == nil || *before.Length != *after.Length) {
		length := *after.Length
		cs.Length = &length
	}
	return cs
}

func unionRangeDigests(existing, additions []RangeDigest) []RangeDigest {
	byKey := make(map[string]RangeDigest)
	for _, rd := range append(append([]RangeDigest(nil), existing...), additions...) {
		byKey[rangeKey(rd)] = rd
	}
	out := make([]RangeDigest, 0, len(byKey))
	for _, rd := range byKey {
		out = append(out, rd)
	}
	return canonicalRangeDigests(out)
}

func cloneVisibleState(in VisibleState) VisibleState {
	out := in
	out.Ranges = append([]RangeDigest(nil), in.Ranges...)
	if in.Length != nil {
		length := *in.Length
		out.Length = &length
	}
	return out
}

func cloneChangeSet(in ChangeSet) ChangeSet {
	out := in
	out.Added = append([]RangeDigest(nil), in.Added...)
	out.Removed = append([]ByteRange(nil), in.Removed...)
	if in.Length != nil {
		length := *in.Length
		out.Length = &length
	}
	return out
}
