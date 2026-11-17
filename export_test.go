package netsec

import "io"

// This file is compiled only into test binaries. It provides the test
// interface for injecting alternative hash functions (e.g. a constant
// hash to force worst-case collisions) and for reading operation
// counters. The production API (New) offers no way to choose the hash
// function.

// HashFunc is the hash function signature accepted by NewForTest.
type HashFunc func(key []byte) uint64

// NewForTest builds a table whose hash function is hf. The random
// source is still required and validated exactly as in New, so tests
// exercise the same secret-handling path.
func NewForTest(rand io.Reader, hf HashFunc) (*Table, error) {
	return newTable(rand, hashFunc(hf))
}

// Compares returns the number of key comparisons performed so far.
func (t *Table) Compares() int64 { return t.compares.Load() }

// ResetCompares zeroes the key-comparison counter.
func (t *Table) ResetCompares() { t.compares.Store(0) }

// BucketCount returns the current number of buckets.
func (t *Table) BucketCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.buckets)
}

// TreeBucketCount returns the number of buckets that have been
// converted to balanced trees.
func (t *Table) TreeBucketCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := 0
	for i := range t.buckets {
		if t.buckets[i].root != nil {
			n++
		}
	}
	return n
}

// HashOf exposes the table's hash of key, letting tests verify that
// distinct secrets produce distinct hashes. It never exposes the
// secret itself.
func (t *Table) HashOf(key []byte) uint64 { return t.hash(key) }
