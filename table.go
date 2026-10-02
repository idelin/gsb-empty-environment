// Package checksumsync implements a real-time consistency inspector between
// a source and a target table using per-bucket rolling XOR checksums
// (a Merkle-tree-like two-level digest scheme).
//
// Every row is hashed into exactly one bucket. Each bucket keeps a rolling
// digest (XOR of row hashes + row count) that is updated in O(1) on every
// insert/update/delete, so the digest stays correct while changes keep
// flowing. An inspection cycle only compares NumBuckets digests, and drills
// down into a bucket (comparing row hashes) only when its digest mismatches,
// which pinpoints the exact primary keys that diverged.
//
// Each bucket carries its own lock, so a heavy write stream on hot
// partitions never stalls the inspection of the remaining partitions.
package checksumsync

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
)

// NumBuckets is the number of checksum partitions (Merkle leaf groups).
const NumBuckets = 1024

// Digest is the rolling checksum of one bucket.
type Digest struct {
	Xor   uint64 // XOR of all row hashes in the bucket
	Count int64  // number of rows in the bucket
}

func (d Digest) String() string {
	return fmt.Sprintf("xor=%016x count=%d", d.Xor, d.Count)
}

// bucket is one partition: its rows plus its rolling checksum. The checksum
// fields are updated under the bucket lock but read lock-free via atomics,
// so a patrol scan never contends with the write stream.
type bucket struct {
	xor   uint64 // atomic
	count int64  // atomic
	mu   sync.RWMutex
	rows map[uint64]string
}

// Table is an in-memory "database" keyed by a uint64 primary key, partitioned
// into NumBuckets independently locked checksum buckets.
type Table struct {
	buckets [NumBuckets]bucket
}

func NewTable() *Table {
	t := &Table{}
	for i := range t.buckets {
		t.buckets[i].rows = make(map[uint64]string)
	}
	return t
}

// bucketOf maps a primary key to its checksum bucket.
func bucketOf(pk uint64) uint64 {
	// Mix the key so sequential IDs spread evenly across buckets.
	x := pk
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	return x % NumBuckets
}

// hashRow hashes one row (primary key + payload) into a 64-bit fingerprint.
func hashRow(pk uint64, value string) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], pk)
	h.Write(buf[:])
	h.Write([]byte{0})
	h.Write([]byte(value))
	return h.Sum64()
}

// Upsert inserts or replaces a row, updating the rolling checksum in O(1).
func (t *Table) Upsert(pk uint64, value string) {
	b := &t.buckets[bucketOf(pk)]
	b.mu.Lock()
	defer b.mu.Unlock()
	newXor := atomic.LoadUint64(&b.xor)
	if old, ok := b.rows[pk]; ok {
		newXor ^= hashRow(pk, old) // roll the old image out
	} else {
		atomic.AddInt64(&b.count, 1)
	}
	newXor ^= hashRow(pk, value) // roll the new image in
	atomic.StoreUint64(&b.xor, newXor)
	b.rows[pk] = value
}

// Delete removes a row, updating the rolling checksum in O(1).
func (t *Table) Delete(pk uint64) {
	b := &t.buckets[bucketOf(pk)]
	b.mu.Lock()
	defer b.mu.Unlock()
	old, ok := b.rows[pk]
	if !ok {
		return
	}
	atomic.AddInt64(&b.count, -1)
	atomic.StoreUint64(&b.xor, atomic.LoadUint64(&b.xor)^hashRow(pk, old))
	delete(b.rows, pk)
}

// Get returns the row payload.
func (t *Table) Get(pk uint64) (string, bool) {
	b := &t.buckets[bucketOf(pk)]
	b.mu.RLock()
	defer b.mu.RUnlock()
	v, ok := b.rows[pk]
	return v, ok
}

// Len returns the number of rows.
func (t *Table) Len() int {
	var n int64
	for i := range t.buckets {
		n += atomic.LoadInt64(&t.buckets[i].count)
	}
	return int(n)
}

// Digest returns the rolling checksum of one bucket. O(1).
func (t *Table) Digest(idx int) Digest {
	b := &t.buckets[idx]
	return Digest{
		Xor:   atomic.LoadUint64(&b.xor),
		Count: atomic.LoadInt64(&b.count),
	}
}

// SnapshotDigests returns a copy of all bucket digests. O(NumBuckets); each
// bucket is read under its own lock, so writers on other buckets never stall
// the scan.
func (t *Table) SnapshotDigests() [NumBuckets]Digest {
	var out [NumBuckets]Digest
	for i := range t.buckets {
		out[i] = t.Digest(i)
	}
	return out
}

// rowsInBucket returns a copy of all rows in bucket idx. O(bucket size).
func (t *Table) rowsInBucket(idx int) map[uint64]string {
	b := &t.buckets[idx]
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make(map[uint64]string, len(b.rows))
	for pk, v := range b.rows {
		out[pk] = v
	}
	return out
}
