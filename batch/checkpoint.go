package batch

import (
	"encoding/json"
	"fmt"
	"os"
)

// ItemStatus is the lifecycle state of one manifest entry inside a batch.
type ItemStatus string

const (
	// StatusPending means the item has not been processed in this batch.
	StatusPending ItemStatus = "pending"
	// StatusDone means the item was processed and its output confirmed.
	StatusDone ItemStatus = "done"
	// StatusIsolated means the item (or one of its dependencies) no longer
	// matches the frozen manifest, so it is excluded from this batch.
	StatusIsolated ItemStatus = "isolated"
)

// ItemRecord is the per-item checkpoint record. For done items it binds the
// output to the exact input digest and dependency digests it was computed
// from, so a later restart can confirm the result is still valid.
type ItemRecord struct {
	Status ItemStatus `json:"status"`
	// InputDigest is the frozen digest the result was computed from.
	InputDigest string `json:"input_digest,omitempty"`
	// OutputDigest confirms the materialized output for this item.
	OutputDigest string `json:"output_digest,omitempty"`
	// DepDigests records the input digests of the dependencies whose
	// same-batch results were consumed.
	DepDigests map[string]string `json:"dep_digests,omitempty"`
	// Reason explains why an item is isolated.
	Reason string `json:"reason,omitempty"`
}

// Checkpoint is the durable state of one batch.
type Checkpoint struct {
	BatchID  int                    `json:"batch_id"`
	SrcDir   string                 `json:"src_dir"`
	Manifest *Manifest              `json:"manifest"`
	Items    map[string]*ItemRecord `json:"items"`
	// Seq counts persisted state transitions; used by fault-injection hooks.
	Seq int `json:"seq"`
}

const checkpointFile = "checkpoint.json"

// saveCheckpoint atomically persists the checkpoint (tmp + rename via FS).
func saveCheckpoint(fs FS, dir string, cp *Checkpoint) error {
	cp.Seq++
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	return fs.WriteFile(joinPath(dir, checkpointFile), data)
}

// loadCheckpoint loads the checkpoint, returning (nil, nil) when none exists.
func loadCheckpoint(fs FS, dir string) (*Checkpoint, error) {
	data, err := fs.ReadFile(joinPath(dir, checkpointFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("batch: read checkpoint: %v", err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("batch: corrupt checkpoint: %v", err)
	}
	if cp.Manifest == nil || cp.Items == nil {
		return nil, fmt.Errorf("batch: incomplete checkpoint")
	}
	return &cp, nil
}
