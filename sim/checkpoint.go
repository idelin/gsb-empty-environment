package sim

import (
	"encoding/json"
	"fmt"
)

// CheckpointVersion is the only checkpoint format this build accepts.
const CheckpointVersion = 1

type entitySnap struct {
	ID       string           `json:"id"`
	Attrs    map[string]int64 `json:"attrs"`
	RNGState uint64           `json:"rng_state"`
	RNGCalls uint64           `json:"rng_calls"`
}

type checkpoint struct {
	Version  int          `json:"version"`
	Seed     uint64       `json:"seed"`
	Now      int64        `json:"now"`
	Entities []entitySnap `json:"entities"`
	Queue    []Event      `json:"queue"`
}

// Save serializes the full engine state (clock, entities with their
// random-stream state and call counts, and the pending queue) into a
// versioned checkpoint blob.
func (eng *Engine) Save() ([]byte, error) {
	cp := checkpoint{
		Version: CheckpointVersion,
		Seed:    eng.seed,
		Now:     eng.now,
		Queue:   append([]Event(nil), eng.queue...),
	}
	for _, id := range eng.EntityIDs() {
		e := eng.entities[id]
		attrs := make(map[string]int64, len(e.Attrs))
		for k, v := range e.Attrs {
			attrs[k] = v
		}
		cp.Entities = append(cp.Entities, entitySnap{
			ID:       e.ID,
			Attrs:    attrs,
			RNGState: e.rng.state,
			RNGCalls: e.rng.calls,
		})
	}
	return json.Marshal(cp)
}

// Load restores an engine from a checkpoint blob. Checkpoints written
// by any other format version are rejected.
func Load(blob []byte, h Handler) (*Engine, error) {
	var cp checkpoint
	if err := json.Unmarshal(blob, &cp); err != nil {
		return nil, fmt.Errorf("invalid checkpoint: %v", err)
	}
	if cp.Version != CheckpointVersion {
		return nil, fmt.Errorf("unsupported checkpoint version %d (want %d)", cp.Version, CheckpointVersion)
	}
	eng := &Engine{
		seed:     cp.Seed,
		now:      cp.Now,
		entities: make(map[string]*Entity, len(cp.Entities)),
		queue:    append([]Event(nil), cp.Queue...),
		handler:  h,
	}
	for _, snap := range cp.Entities {
		if _, ok := eng.entities[snap.ID]; ok {
			return nil, fmt.Errorf("checkpoint lists entity %q twice", snap.ID)
		}
		attrs := make(map[string]int64, len(snap.Attrs))
		for k, v := range snap.Attrs {
			attrs[k] = v
		}
		eng.entities[snap.ID] = &Entity{
			ID:    snap.ID,
			Attrs: attrs,
			rng:   RNG{state: snap.RNGState, calls: snap.RNGCalls},
		}
	}
	return eng, nil
}
