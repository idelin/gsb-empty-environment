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

const (
	bucketListMax  = 8
	initialBuckets = 16
	growLoadFactor = 8
)

// ErrKeyTooLong is returned when a key exceeds MaxKeyLen bytes.
var ErrKeyTooLong = errors.New("netsec: key exceeds 256 bytes")

type entry struct {
	key   []byte
	value []byte
}

// bucket holds either a short list (up to bucketListMax entries) or,
// once it overflows, an AVL tree ordered by raw key bytes.
type bucket struct {
	tree *avlTree
	list []entry
}

func (b *bucket) get(key []byte, cmp *atomic.Uint64) ([]byte, bool) {
	if b.tree != nil {
		return b.tree.get(key)
	}
	for i := range b.list {
		cmp.Add(1)
		if bytes.Equal(b.list[i].key, key) {
			return b.list[i].value, true
		}
	}
	return nil, false
}

func (b *bucket) insert(key, value []byte, cmp *atomic.Uint64) bool {
	if b.tree != nil {
		return b.tree.insert(key, value)
	}
	for i := range b.list {
		cmp.Add(1)
		if bytes.Equal(b.list[i].key, key) {
			b.list[i].value = value
			return false
		}
	}
	b.list = append(b.list, entry{key: key, value: value})
	if len(b.list) > bucketListMax {
		b.convertToTree(cmp)
	}
	return true
}

func (b *bucket) delete(key []byte, cmp *atomic.Uint64) bool {
	if b.tree != nil {
		return b.tree.delete(key)
	}
	for i := range b.list {
		cmp.Add(1)
		if bytes.Equal(b.list[i].key, key) {
			b.list = append(b.list[:i], b.list[i+1:]...)
			return true
		}
	}
	return false
}

func (b *bucket) convertToTree(cmp *atomic.Uint64) {
	tree := &avlTree{cmp: cmp}
	for _, e := range b.list {
		tree.insert(e.key, e.value)
	}
	b.tree = tree
	b.list = nil
}

// Table is a hash table hardened against hash-collision denial of
// service. Each instance is keyed with an independent random secret,
// and buckets that grow past bucketListMax entries are converted to
// AVL trees ordered by raw key bytes, giving O(log n) worst-case
// operations even under total hash collision.
//
// All methods are safe for concurrent use; a single RWMutex makes
// every operation (including growth) linearizable. The secret is
// never exposed through any accessor or string form.
type Table struct {
	mu      sync.RWMutex
	hashFn  func(key []byte) uint64
	buckets []bucket
	size    int
	cmp     atomic.Uint64
}

// NewTable creates a table, drawing its per-instance secret from
// randSource. If randSource is nil, fails, or yields fewer than 16
// bytes, table creation is refused and an error is returned.
func NewTable(randSource io.Reader) (*Table, error) {
	if randSource == nil {
		return nil, errors.New("netsec: a random source is required")
	}
	var secret [16]byte
	if _, err := io.ReadFull(randSource, secret[:]); err != nil {
		return nil, fmt.Errorf("netsec: cannot read table secret: %w", err)
	}
	k0 := binary.LittleEndian.Uint64(secret[:8])
	k1 := binary.LittleEndian.Uint64(secret[8:])
	return newTable(func(key []byte) uint64 {
		return siphash(k0, k1, key)
	}), nil
}

func newTable(hashFn func(key []byte) uint64) *Table {
	return &Table{
		hashFn:  hashFn,
		buckets: make([]bucket, initialBuckets),
	}
}

// newTableWithHash builds a table with a caller-supplied hash function.
// It is unexported and exists only so in-package tests can inject a
// constant hash to exercise the worst-case collision path; the public
// API never lets callers choose the hash function.
func newTableWithHash(hashFn func(key []byte) uint64) *Table {
	return newTable(hashFn)
}

func (t *Table) bucketIndex(hash uint64) int {
	return int(hash & uint64(len(t.buckets)-1))
}

// Insert stores value under key, replacing any existing value.
// Keys longer than MaxKeyLen are rejected with ErrKeyTooLong.
func (t *Table) Insert(key, value []byte) error {
	if len(key) > MaxKeyLen {
		return ErrKeyTooLong
	}
	k := append([]byte(nil), key...)
	v := append([]byte(nil), value...)
	t.mu.Lock()
	defer t.mu.Unlock()
	idx := t.bucketIndex(t.hashFn(k))
	if t.buckets[idx].insert(k, v, &t.cmp) {
		t.size++
		if t.size > len(t.buckets)*growLoadFactor {
			t.grow()
		}
	}
	return nil
}

// Lookup returns the value stored under key.
func (t *Table) Lookup(key []byte) ([]byte, bool) {
	if len(key) > MaxKeyLen {
		return nil, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	idx := t.bucketIndex(t.hashFn(key))
	v, ok := t.buckets[idx].get(key, &t.cmp)
	if !ok {
		return nil, false
	}
	return append([]byte(nil), v...), true
}

// Delete removes key, returning true if it was present.
func (t *Table) Delete(key []byte) bool {
	if len(key) > MaxKeyLen {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	idx := t.bucketIndex(t.hashFn(key))
	if t.buckets[idx].delete(key, &t.cmp) {
		t.size--
		return true
	}
	return false
}

// Len returns the number of stored entries.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.size
}

// CompareCount returns the total number of key comparisons performed
// so far. It exposes work done, never key material or the secret.
func (t *Table) CompareCount() uint64 {
	return t.cmp.Load()
}

// ResetCompareCount zeroes the comparison counter.
func (t *Table) ResetCompareCount() {
	t.cmp.Store(0)
}

// grow doubles the bucket array and rehashes every entry. It runs
// under the write lock, so it is linearizable with respect to
// concurrent lookups and deletes.
func (t *Table) grow() {
	newBuckets := make([]bucket, len(t.buckets)*2)
	mask := uint64(len(newBuckets) - 1)
	move := func(key, value []byte) {
		idx := int(t.hashFn(key) & mask)
		newBuckets[idx].insert(key, value, &t.cmp)
	}
	for i := range t.buckets {
		old := &t.buckets[i]
		if old.tree != nil {
			old.tree.forEach(move)
		} else {
			for _, e := range old.list {
				move(e.key, e.value)
			}
		}
	}
	t.buckets = newBuckets
}
