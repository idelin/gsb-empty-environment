package ormap

// Checkpoint is a portable snapshot of a replica's full state. It
// carries the compressed tombstones, so a restored replica keeps the
// causal information needed to reject replays of removed assignments,
// and the dot counter, so restored replicas never reissue a dead dot.
type Checkpoint struct {
	ReplicaID string                   `json:"replica"`
	Counter   uint64                   `json:"counter"`
	Clock     uint64                   `json:"clock"`
	Keys      map[string]KeyCheckpoint `json:"keys,omitempty"`
}

// KeyCheckpoint snapshots one key: live versions plus tombstones
// compressed into inclusive counter ranges per replica.
type KeyCheckpoint struct {
	Live       []Version              `json:"live,omitempty"`
	Tombstones map[string][][2]uint64 `json:"tombstones,omitempty"`
}

// Export snapshots the replica state. The result is deterministic:
// versions and tombstone ranges are sorted.
func (m *Map) Export() Checkpoint {
	cp := Checkpoint{
		ReplicaID: m.ReplicaID,
		Counter:   m.counter,
		Clock:     m.clock,
	}
	if len(m.keys) > 0 {
		cp.Keys = make(map[string]KeyCheckpoint, len(m.keys))
	}
	for key, ks := range m.keys {
		if ks.empty() {
			continue
		}
		kc := KeyCheckpoint{Live: m.Live(key)}
		if len(ks.tombs) > 0 {
			kc.Tombstones = make(map[string][][2]uint64, len(ks.tombs))
			for rep, rs := range ks.tombs {
				kc.Tombstones[rep] = append([][2]uint64(nil), rs...)
			}
		}
		cp.Keys[key] = kc
	}
	return cp
}

// Restore rebuilds a replica from a checkpoint. Live entries whose dot
// is covered by a tombstone are dropped (tombstones win); a dot listed
// twice with different content is rejected.
func Restore(cp Checkpoint) (*Map, error) {
	m := NewMap(cp.ReplicaID)
	m.counter = cp.Counter
	m.clock = cp.Clock
	for key, kc := range cp.Keys {
		ks := m.keyState(key)
		for rep, rs := range kc.Tombstones {
			ks.tombs[rep] = unionRanges(ks.tombs[rep], normalizeRanges(rs))
		}
		seen := make(map[Dot]Version, len(kc.Live))
		for _, v := range kc.Live {
			if existing, ok := seen[v.Dot]; ok && existing != v {
				return nil, &DotConflictError{Dot: v.Dot, Existing: existing, Incoming: v}
			}
			seen[v.Dot] = v
			if !ks.tombstoned(v.Dot) {
				ks.live[v.Dot] = v
			}
		}
		if ks.empty() {
			delete(m.keys, key)
		}
	}
	return m, nil
}

// SameData reports whether two checkpoints hold equivalent replicated
// data, ignoring local bookkeeping (replica ID, counter, clock).
func (cp Checkpoint) SameData(other Checkpoint) bool {
	if len(cp.Keys) != len(other.Keys) {
		return false
	}
	for key, kc := range cp.Keys {
		okc, ok := other.Keys[key]
		if !ok || !kc.same(okc) {
			return false
		}
	}
	return true
}

func (kc KeyCheckpoint) same(o KeyCheckpoint) bool {
	if len(kc.Live) != len(o.Live) || len(kc.Tombstones) != len(o.Tombstones) {
		return false
	}
	for i, v := range kc.Live {
		if v != o.Live[i] {
			return false
		}
	}
	for rep, rs := range kc.Tombstones {
		ors, ok := o.Tombstones[rep]
		if !ok || len(rs) != len(ors) {
			return false
		}
		for i, r := range rs {
			if r != ors[i] {
				return false
			}
		}
	}
	return true
}
