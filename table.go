package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// MaxKeyLen is the maximum accepted key length in bytes.
const MaxKeyLen = 256

// ErrKeyTooLong is returned by operations on keys longer than MaxKeyLen.
var ErrKeyTooLong = errors.New("netsec: key exceeds 256 bytes")

const (
	initialBuckets      = 16
	bucketTreeThreshold = 8
	loadFactor          = 8
	secretLen           = 16
)

// hashFunc maps a raw key to a 64-bit hash. The production hash is
// SipHash-2-4 keyed with the table secret; tests may inject alternatives
// through hooks that are compiled only into test binaries.
type hashFunc func(key []byte) uint64

type entry struct {
	key   []byte
	value []byte
}

// bucket holds either a short list of entries or, once it grows beyond
// bucketTreeThreshold, an AVL tree ordered by raw key bytes.
type bucket struct {
	list []entry
	root *node
}

func (b *bucket) insert(k, v []byte, cmp func(a, b []byte) int) bool {
	if b.root != nil {
		var inserted bool
		b.root, inserted = avlInsert(b.root, k, v, cmp)
		return inserted
	}
	for i := range b.list {
		if cmp(b.list[i].key, k) == 0 {
			b.list[i].value = v
			return false
		}
	}
	b.list = append(b.list, entry{key: k, value: v})
	if len(b.list) > bucketTreeThreshold {
		b.treeify(cmp)
	}
	return true
}

func (b *bucket) get(k []byte, cmp func(a, b []byte) int) ([]byte, bool) {
	if b.root != nil {
		return avlGet(b.root, k, cmp)
	}
	for i := range b.list {
		if cmp(b.list[i].key, k) == 0 {
			return b.list[i].value, true
		}
	}
	return nil, false
}

func (b *bucket) delete(k []byte, cmp func(a, b []byte) int) bool {
	if b.root != nil {
		var deleted bool
		b.root, deleted = avlDelete(b.root, k, cmp)
		return deleted
	}
	for i := range b.list {
		if cmp(b.list[i].key, k) == 0 {
			b.list = append(b.list[:i], b.list[i+1:]...)
			return true
		}
	}
	return false
}

func (b *bucket) treeify(cmp func(a, b []byte) int) {
	var root *node
	for _, e := range b.list {
		root, _ = avlInsert(root, e.key, e.value, cmp)
	}
	b.root = root
	b.list = nil
}

func (b *bucket) forEach(fn func(k, v []byte)) {
	if b.root != nil {
		avlForEach(b.root, fn)
		return
	}
	for _, e := range b.list {
		fn(e.key, e.value)
	}
}

// Table is a hash table safe for attacker-controlled keys. The zero
// value is not usable; construct with New.
type Table struct {
	mu       sync.RWMutex
	buckets  []bucket
	count    int
	hash     hashFunc
	compares atomic.Int64
}

// New creates a table keyed with a fresh secret read from rand. The
// caller must inject a cryptographically secure random source (e.g.
// crypto/rand.Reader); if rand is nil or fails to provide enough bytes,
// New returns an error and no table is created. The hash function is
// fixed to keyed SipHash-2-4 and cannot be selected through this API.
func New(rand io.Reader) (*Table, error) {
	return newTable(rand, nil)
}

func newTable(rand io.Reader, hf hashFunc) (*Table, error) {
	if rand == nil {
		return nil, errors.New("netsec: a random source is required")
	}
	var secret [secretLen]byte
	if _, err := io.ReadFull(rand, secret[:]); err != nil {
		return nil, fmt.Errorf("netsec: reading table secret: %w", err)
	}
	if hf == nil {
		k0 := binary.LittleEndian.Uint64(secret[0:8])
		k1 := binary.LittleEndian.Uint64(secret[8:16])
		hf = func(key []byte) uint64 { return siphash24(k0, k1, key) }
	}
	return &Table{
		buckets: make([]bucket, initialBuckets),
		hash:    hf,
	}, nil
}

// cmp counts every key comparison so tests can produce operation-count
// evidence for worst-case workloads.
func (t *Table) cmp(a, b []byte) int {
	t.compares.Add(1)
	return bytes.Compare(a, b)
}

func (t *Table) bucketIndex(k []byte) uint64 {
	return t.hash(k) & uint64(len(t.buckets)-1)
}

// grow doubles the bucket array and rehashes all entries. Callers must
// hold t.mu for writing.
func (t *Table) grow() {
	old := t.buckets
	t.buckets = make([]bucket, len(old)*2)
	for i := range old {
		old[i].forEach(func(k, v []byte) {
			b := &t.buckets[t.bucketIndex(k)]
			b.insert(k, v, t.cmp)
		})
	}
}

// Insert adds or replaces the value for key. Keys longer than MaxKeyLen
// are rejected with ErrKeyTooLong. Key and value are copied.
func (t *Table) Insert(key, value []byte) error {
	if len(key) > MaxKeyLen {
		return ErrKeyTooLong
	}
	k := bytes.Clone(key)
	v := bytes.Clone(value)
	t.mu.Lock()
	defer t.mu.Unlock()
	b := &t.buckets[t.bucketIndex(k)]
	if b.insert(k, v, t.cmp) {
		t.count++
		if t.count > len(t.buckets)*loadFactor {
			t.grow()
		}
	}
	return nil
}

// Get returns a copy of the value stored for key.
func (t *Table) Get(key []byte) ([]byte, bool) {
	if len(key) > MaxKeyLen {
		return nil, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	b := &t.buckets[t.bucketIndex(key)]
	v, ok := b.get(key, t.cmp)
	if !ok {
		return nil, false
	}
	return bytes.Clone(v), true
}

// Delete removes key, reporting whether it was present.
func (t *Table) Delete(key []byte) bool {
	if len(key) > MaxKeyLen {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	b := &t.buckets[t.bucketIndex(key)]
	if b.delete(key, t.cmp) {
		t.count--
		return true
	}
	return false
}

// Len returns the number of stored entries.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.count
}

// String and GoString deliberately omit the table secret and key
// material so formatting a table cannot leak them.
func (t *Table) String() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return fmt.Sprintf("netsec.Table{entries:%d buckets:%d}", t.count, len(t.buckets))
}

func (t *Table) GoString() string { return t.String() }
