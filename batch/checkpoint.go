package batch

import (
	"errors"
	"fmt"
	"sync"
)

// Status is the lifecycle state of one manifest item inside a batch.
type Status string

const (
	// StatusPending means the item still needs processing in this batch.
	StatusPending Status = "pending"
	// StatusDone means the item's output is confirmed in the checkpoint,
	// bound to the input digest frozen at batch start.
	StatusDone Status = "done"
	// StatusIsolated means the item (and its successors) were cut off from
	// this batch because its source version changed or disappeared, or a
	// dependency was isolated. Isolated items are never processed and
	// their results are never reused by other batches.
	StatusIsolated Status = "isolated"
)

// ItemCheckpoint is the per-item record persisted at every cut point.
type ItemCheckpoint struct {
	Path      string
	DependsOn []string

	// InputDigest is the content digest frozen at batch start. Done
	// results stay bound to this digest even if the source file is
	// later replaced.
	InputDigest string
	// DepDigests records the frozen input digest of every dependency,
	// so a resumed batch can prove it is not mixing input versions.
	DepDigests map[string]string

	Status Status
	Reason string // isolation reason, when Status == StatusIsolated

	// Output and OutputDigest form the output confirmation: on resume
	// the stored output is re-hashed and must match OutputDigest.
	Output       []byte
	OutputDigest string
}

// Checkpoint is the full recoverable state of one batch.
type Checkpoint struct {
	BatchID string
	// Order is the frozen topological processing order.
	Order []string
	Items map[string]*ItemCheckpoint
}

func (c *Checkpoint) clone() *Checkpoint {
	out := &Checkpoint{
		BatchID: c.BatchID,
		Order:   append([]string(nil), c.Order...),
		Items:   make(map[string]*ItemCheckpoint, len(c.Items)),
	}
	for path, ic := range c.Items {
		dup := *ic
		dup.DependsOn = append([]string(nil), ic.DependsOn...)
		dup.DepDigests = make(map[string]string, len(ic.DepDigests))
		for dep, digest := range ic.DepDigests {
			dup.DepDigests[dep] = digest
		}
		dup.Output = append([]byte(nil), ic.Output...)
		out.Items[path] = &dup
	}
	return out
}

// Sentinel errors returned by Store and Runner.
var (
	ErrBatchNotFound     = errors.New("batch: unknown batch ID")
	ErrBatchExists       = errors.New("batch: batch ID already exists")
	ErrCheckpointCorrupt = errors.New("batch: checkpoint output confirmation failed")
)

// Store persists checkpoints. Save is called at every cut point: after
// freezing, after each item state change, and after each output
// confirmation.
type Store interface {
	Save(cp *Checkpoint) error
	Load(batchID string) (*Checkpoint, error)
}

// MemStore is an in-memory Store. Tests wrap it to inject save failures.
type MemStore struct {
	mu      sync.Mutex
	batches map[string]*Checkpoint
}

func NewMemStore() *MemStore {
	return &MemStore{batches: make(map[string]*Checkpoint)}
}

func (s *MemStore) Save(cp *Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches[cp.BatchID] = cp.clone()
	return nil
}

func (s *MemStore) Load(batchID string) (*Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, ok := s.batches[batchID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrBatchNotFound, batchID)
	}
	return cp.clone(), nil
}
