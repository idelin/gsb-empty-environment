package namecrypt

import (
	"sync"
)

// InodeRef fully identifies an inode: its id and its generation.
type InodeRef struct {
	ID         InodeID
	Generation uint64
}

// Record is the on-disk directory entry. The raw name never appears here; it
// is sealed in Ciphertext. Canonical is kept in the clear so bucket conflict
// checks work without decrypting every candidate, and its SHA-256 is bound
// into the AEAD associated data so tampering makes decryption fail.
type Record struct {
	EntryNonce    []byte
	Ciphertext    []byte
	Canonical     string
	Inode         InodeRef
	KeyGeneration uint64
}

// LookupKey is the 20-byte on-disk bucket key.
type LookupKey [LookupKeySize]byte

// Tx is a serializable set of bucket mutations. Either every applied change
// commits or, on Abort, none do. A Tx is single-use.
type Tx interface {
	// Put inserts or replaces the entry for the given record pointer.
	Put(bucket LookupKey, rec *Record)
	// Delete removes the exact entry previously read from the store.
	Delete(bucket LookupKey, rec *Record)
	// Commit atomically publishes all mutations.
	Commit() error
	// Abort discards every mutation performed on this transaction.
	Abort() error
}

// Store is the persistence boundary a filesystem host must provide.
type Store interface {
	// GetBucket returns every record stored under the lookup key. A digest
	// collision causes multiple records to share one bucket.
	GetBucket(bucket LookupKey) ([]*Record, error)
	// Begin starts a transaction used to atomically swap entries.
	Begin() (Tx, error)
}

// MemoryStore is an in-memory Store useful for hosts and tests. Records are
// immutable once stored; transactions operate on their pointers.
type MemoryStore struct {
	mu      sync.Mutex
	buckets map[LookupKey][]*Record
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{buckets: make(map[LookupKey][]*Record)}
}

// GetBucket implements Store.
func (m *MemoryStore) GetBucket(bucket LookupKey) ([]*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	records := m.buckets[bucket]
	out := make([]*Record, len(records))
	copy(out, records)
	return out, nil
}

// AllRecords implements RecordEnumerator for key rotation.
func (m *MemoryStore) AllRecords() []*Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Record
	for _, records := range m.buckets {
		out = append(out, records...)
	}
	return out
}

// Begin implements Store.
func (m *MemoryStore) Begin() (Tx, error) {
	m.mu.Lock()
	snapshot := make(map[LookupKey][]*Record, len(m.buckets))
	for k, v := range m.buckets {
		snapshot[k] = append([]*Record(nil), v...)
	}
	return &memoryTx{store: m, snapshot: snapshot}, nil
}

type memoryOp struct {
	put    bool
	bucket LookupKey
	rec    *Record
}

type memoryTx struct {
	store    *MemoryStore
	snapshot map[LookupKey][]*Record
	ops      []memoryOp
	finished bool
}

func (t *memoryTx) Put(bucket LookupKey, rec *Record) {
	t.ops = append(t.ops, memoryOp{put: true, bucket: bucket, rec: rec})
}

func (t *memoryTx) Delete(bucket LookupKey, rec *Record) {
	t.ops = append(t.ops, memoryOp{put: false, bucket: bucket, rec: rec})
}

func (t *memoryTx) Commit() error {
	if t.finished {
		return ErrBadRecord
	}
	t.finished = true
	defer t.store.mu.Unlock()

	working := make(map[LookupKey][]*Record, len(t.snapshot))
	for k, v := range t.snapshot {
		working[k] = append([]*Record(nil), v...)
	}
	for _, op := range t.ops {
		list := working[op.bucket]
		if op.put {
			list = append(list, op.rec)
		} else {
			found := -1
			for i, existing := range list {
				if existing == op.rec {
					found = i
					break
				}
			}
			if found < 0 {
				return ErrNotFound
			}
			list = append(list[:found], list[found+1:]...)
		}
		if len(list) == 0 {
			delete(working, op.bucket)
		} else {
			working[op.bucket] = list
		}
	}
	t.store.buckets = working
	return nil
}

func (t *memoryTx) Abort() error {
	if t.finished {
		return ErrBadRecord
	}
	t.finished = true
	t.store.mu.Unlock()
	return nil
}
