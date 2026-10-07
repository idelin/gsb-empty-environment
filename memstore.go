package importer

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// ErrReceiptLost is returned by MemStore when a commit was applied but its
// receipt is dropped, simulating a crash between target commit and
// checkpoint save.
var ErrReceiptLost = errors.New("memstore: commit applied but receipt lost")

// MemStore is an in-memory Store with idempotent, batch-numbered commits.
// It can drop receipts to simulate losing a commit acknowledgement.
type MemStore struct {
	mu sync.Mutex
	// batches[i] is batch number i+1.
	batches []committedBatch
	// DropReceipts lists batch numbers whose commit is applied but reported
	// as failed with ErrReceiptLost (each dropped at most once).
	DropReceipts map[uint64]bool
}

type committedBatch struct {
	digest  string
	records [][]byte
}

var _ Store = (*MemStore)(nil)

func (s *MemStore) LastBatch() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return uint64(len(s.batches)), nil
}

func (s *MemStore) CommitBatch(batch uint64, records [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	digest := digestRecords(records)
	if batch <= uint64(len(s.batches)) {
		// Redelivery of an already-committed batch: deduplicate if the
		// content matches, reject conflicting content.
		if s.batches[batch-1].digest == digest {
			return nil
		}
		return fmt.Errorf("memstore: batch %d already committed with different records", batch)
	}
	if batch != uint64(len(s.batches))+1 {
		return fmt.Errorf("memstore: out-of-order batch %d, last committed is %d", batch, len(s.batches))
	}
	cp := make([][]byte, len(records))
	for i, r := range records {
		cp[i] = append([]byte(nil), r...)
	}
	s.batches = append(s.batches, committedBatch{digest: digest, records: cp})
	if s.DropReceipts[batch] {
		delete(s.DropReceipts, batch)
		return ErrReceiptLost
	}
	return nil
}

// Records returns all committed records in batch order.
func (s *MemStore) Records() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]byte
	for _, b := range s.batches {
		out = append(out, b.records...)
	}
	return out
}

func digestRecords(records [][]byte) string {
	h := sha256.New()
	var size [8]byte
	for _, r := range records {
		binary.BigEndian.PutUint64(size[:], uint64(len(r)))
		h.Write(size[:])
		h.Write(r)
	}
	return hex.EncodeToString(h.Sum(nil))
}
