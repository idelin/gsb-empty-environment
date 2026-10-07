package streamagg

import (
	"encoding/json"
	"io"
	"sort"
)

// Checkpoint is a durable snapshot of the aggregator: the global watermark
// plus every partition's watermark and idle flag.
type Checkpoint struct {
	Global     int64                     `json:"global"`
	HasGlobal  bool                      `json:"hasGlobal"`
	Partitions map[string]PartitionState `json:"partitions"`
}

// PartitionState captures one partition's watermark and idle flag.
type PartitionState struct {
	Watermark    int64 `json:"watermark"`
	HasWatermark bool  `json:"hasWatermark"`
	Idle         bool  `json:"idle"`
}

// Snapshot captures the aggregator's current state as a Checkpoint.
func (a *Aggregator) Snapshot() Checkpoint {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := Checkpoint{
		Global:     a.global,
		HasGlobal:  a.hasGlobal,
		Partitions: make(map[string]PartitionState, len(a.partitions)),
	}
	for id, p := range a.partitions {
		cp.Partitions[id] = PartitionState{
			Watermark:    p.watermark,
			HasWatermark: p.hasWatermark,
			Idle:         p.idle,
		}
	}
	return cp
}

// Restore replaces the aggregator's state with the checkpoint's. The
// restored global watermark is preserved verbatim; subsequent events only
// advance it forward.
func (a *Aggregator) Restore(cp Checkpoint) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.global = cp.Global
	a.hasGlobal = cp.HasGlobal
	a.partitions = make(map[string]*partitionState, len(cp.Partitions))
	for id, ps := range cp.Partitions {
		a.partitions[id] = &partitionState{
			watermark:    ps.Watermark,
			hasWatermark: ps.HasWatermark,
			idle:         ps.Idle,
		}
	}
}

// SaveCheckpoint serializes a snapshot of the aggregator to w as JSON.
// Partition keys are emitted in sorted order for deterministic output.
func (a *Aggregator) SaveCheckpoint(w io.Writer) error {
	cp := a.Snapshot()
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(toSortedCheckpoint(cp))
}

// LoadCheckpoint reads a checkpoint from r and restores it into the
// aggregator.
func (a *Aggregator) LoadCheckpoint(r io.Reader) error {
	var cp Checkpoint
	if err := json.NewDecoder(r).Decode(&cp); err != nil {
		return err
	}
	a.Restore(cp)
	return nil
}

// sortedCheckpoint is a serialization helper producing stable key order.
type sortedCheckpoint struct {
	Global     int64                  `json:"global"`
	HasGlobal  bool                   `json:"hasGlobal"`
	Partitions []sortedPartitionEntry `json:"partitions"`
}

type sortedPartitionEntry struct {
	ID    string         `json:"id"`
	State PartitionState `json:"state"`
}

func toSortedCheckpoint(cp Checkpoint) sortedCheckpoint {
	ids := make([]string, 0, len(cp.Partitions))
	for id := range cp.Partitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sc := sortedCheckpoint{
		Global:     cp.Global,
		HasGlobal:  cp.HasGlobal,
		Partitions: make([]sortedPartitionEntry, 0, len(ids)),
	}
	for _, id := range ids {
		sc.Partitions = append(sc.Partitions, sortedPartitionEntry{ID: id, State: cp.Partitions[id]})
	}
	return sc
}

// UnmarshalJSON accepts both the sorted-entry form produced by
// SaveCheckpoint and the plain map form of Checkpoint.
func (cp *Checkpoint) UnmarshalJSON(data []byte) error {
	var sc sortedCheckpoint
	if err := json.Unmarshal(data, &sc); err == nil && sc.Partitions != nil {
		cp.Global = sc.Global
		cp.HasGlobal = sc.HasGlobal
		cp.Partitions = make(map[string]PartitionState, len(sc.Partitions))
		for _, e := range sc.Partitions {
			cp.Partitions[e.ID] = e.State
		}
		return nil
	}
	type plain Checkpoint
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*cp = Checkpoint(p)
	return nil
}
