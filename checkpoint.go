package ormap

import "encoding/json"

// Checkpoint support. The exported form is the full state: live dot store
// plus the compacted causal context. The context is the tombstone
// information: it is what prevents re-delivered old dots from
// resurrecting removed values after a restore, so it is always preserved
// in its compact (version vector + out-of-order dots) form.

type checkpointEntry struct {
	Replica string `json:"replica"`
	Counter uint64 `json:"counter"`
	Value   string `json:"value"`
}

type checkpointDot struct {
	Replica string `json:"replica"`
	Counter uint64 `json:"counter"`
}

type checkpoint struct {
	Store map[string][]checkpointEntry `json:"store"`
	Max   map[string]uint64            `json:"max"`
	Extra []checkpointDot              `json:"extra"`
}

// Export serializes the state into a self-contained checkpoint.
func (s *State) Export() ([]byte, error) {
	cp := checkpoint{
		Store: make(map[string][]checkpointEntry),
		Max:   make(map[string]uint64),
	}
	for key, dots := range s.Store {
		for dot, value := range dots {
			cp.Store[key] = append(cp.Store[key], checkpointEntry{
				Replica: dot.Replica,
				Counter: dot.Counter,
				Value:   value,
			})
		}
	}
	for replica, max := range s.Context.Max {
		cp.Max[replica] = max
	}
	for dot := range s.Context.Extra {
		cp.Extra = append(cp.Extra, checkpointDot{Replica: dot.Replica, Counter: dot.Counter})
	}
	return json.Marshal(cp)
}

// Import restores a state from a checkpoint produced by Export.
func Import(data []byte) (*State, error) {
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, err
	}
	state := NewState()
	for key, entries := range cp.Store {
		for _, entry := range entries {
			state.put(key, Dot{Replica: entry.Replica, Counter: entry.Counter}, entry.Value)
		}
	}
	for replica, max := range cp.Max {
		state.Context.Max[replica] = max
	}
	for _, dot := range cp.Extra {
		state.Context.Extra[Dot{Replica: dot.Replica, Counter: dot.Counter}] = struct{}{}
	}
	// Re-run compaction so the restored context is in canonical form.
	state.Context.compact()
	return state, nil
}
