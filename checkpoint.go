package streamagg

import "encoding/json"

// PartitionCheckpoint captures the persisted state of one partition: its
// watermark and its idle flag.
type PartitionCheckpoint struct {
	Watermark int64 `json:"watermark"`
	Set       bool  `json:"set"`
	Idle      bool  `json:"idle"`
}

// Checkpoint is a serialisable snapshot of the whole aggregator: the global
// watermark plus every partition's watermark and idle flag.
type Checkpoint struct {
	Global     int64                          `json:"global"`
	HasGlobal  bool                           `json:"hasGlobal"`
	Partitions map[string]PartitionCheckpoint `json:"partitions"`
}

// Snapshot captures the current aggregator state into a checkpoint.
func (a *Aggregator) Snapshot() Checkpoint {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := Checkpoint{
		Global:     a.global,
		HasGlobal:  a.hasGlobal,
		Partitions: make(map[string]PartitionCheckpoint, len(a.partitions)),
	}
	for name, p := range a.partitions {
		cp.Partitions[name] = PartitionCheckpoint{
			Watermark: p.watermark,
			Set:       p.set,
			Idle:      p.idle,
		}
	}
	return cp
}

// Restore rebuilds an aggregator from a checkpoint, preserving every
// partition's watermark and idle flag as well as the global watermark.
func Restore(cp Checkpoint) *Aggregator {
	a := NewAggregator()
	a.global = cp.Global
	a.hasGlobal = cp.HasGlobal
	for name, pc := range cp.Partitions {
		a.partitions[name] = &partitionState{
			watermark: pc.Watermark,
			set:       pc.Set,
			idle:      pc.Idle,
		}
	}
	return a
}

// Encode serialises a checkpoint to JSON for durable storage.
func EncodeCheckpoint(cp Checkpoint) ([]byte, error) {
	return json.Marshal(cp)
}

// DecodeCheckpoint deserialises a checkpoint produced by EncodeCheckpoint.
func DecodeCheckpoint(data []byte) (Checkpoint, error) {
	var cp Checkpoint
	err := json.Unmarshal(data, &cp)
	return cp, err
}
