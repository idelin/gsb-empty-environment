package sim

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
)

// CheckpointVersion is the only checkpoint format this build can restore.
const CheckpointVersion uint32 = 1

// ErrUnknownCheckpointVersion is returned when a checkpoint's version does
// not match CheckpointVersion.
var ErrUnknownCheckpointVersion = errors.New("sim: unknown checkpoint version")

// Checkpoint is a complete, serializable snapshot of an Engine.
type Checkpoint struct {
	Version  uint32
	Seed     uint64
	NextSeq  uint64
	Entities []entitySnapshot
}

// Checkpoint captures the engine state, including every entity's RNG state
// and draw count.
func (e *Engine) Checkpoint() *Checkpoint {
	ids := e.sortedIDs()
	cp := &Checkpoint{
		Version:  CheckpointVersion,
		Seed:     e.seed,
		NextSeq:  e.nextSeq,
		Entities: make([]entitySnapshot, 0, len(ids)),
	}
	for _, id := range ids {
		cp.Entities = append(cp.Entities, e.entities[id].snapshot())
	}
	return cp
}

// Restore rebuilds an engine from a checkpoint. Checkpoints written by an
// unknown (newer or foreign) version are rejected.
func Restore(cp *Checkpoint) (*Engine, error) {
	if cp.Version != CheckpointVersion {
		return nil, fmt.Errorf("%v: got %d, want %d", ErrUnknownCheckpointVersion, cp.Version, CheckpointVersion)
	}
	e := &Engine{
		seed:     cp.Seed,
		nextSeq:  cp.NextSeq,
		entities: make(map[string]*Entity, len(cp.Entities)),
	}
	for _, s := range cp.Entities {
		e.entities[s.ID] = entityFromSnapshot(s)
	}
	return e, nil
}

// Fork returns an independent copy of the engine via a checkpoint
// round-trip. Both branches continue from identical state.
func (e *Engine) Fork() (*Engine, error) {
	return Restore(e.Checkpoint())
}

// MarshalCheckpoint serializes a checkpoint with a version header.
func MarshalCheckpoint(cp *Checkpoint) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(cp); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalCheckpoint deserializes a checkpoint, rejecting unknown versions
// before any state is touched.
func UnmarshalCheckpoint(data []byte) (*Checkpoint, error) {
	var cp Checkpoint
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&cp); err != nil {
		return nil, err
	}
	if cp.Version != CheckpointVersion {
		return nil, fmt.Errorf("%v: got %d, want %d", ErrUnknownCheckpointVersion, cp.Version, CheckpointVersion)
	}
	return &cp, nil
}
