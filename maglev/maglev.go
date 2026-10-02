// Package maglev implements Google's Maglev consistent-hashing lookup table.
//
// Each backend produces a permutation of the table slots derived from two
// independent hashes of its name. Slots are then populated round-robin:
// every backend claims the next free slot in its own permutation until the
// table is full. The resulting table gives O(1) lookups and near-perfect
// balance, and removing a backend only remaps (roughly) the slots it owned.
package maglev

import (
	"errors"
	"hash/crc64"
	"hash/fnv"
)

// DefaultSize is the table size recommended by the Maglev paper: a prime
// slightly larger than 2^16.
const DefaultSize = 65537

// Table is an immutable Maglev lookup table. It is safe for concurrent use
// after construction (lock-free reads).
type Table struct {
	size     int
	backends []string
	lookup   []int32 // lookup[slot] = index into backends
}

// hash1 and hash2 are two independent hash functions used to derive each
// backend's permutation (offset and skip).
func hash1(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

var crc64Table = crc64.MakeTable(crc64.ECMA)

func hash2(s string) uint64 {
	return crc64.Checksum([]byte(s), crc64Table)
}

// IsPrime reports whether n is prime. Table sizes must be prime so that any
// skip in [1, size) generates a full permutation of the slots.
func IsPrime(n int) bool {
	if n < 2 {
		return false
	}
	for d := 2; d*d <= n; d++ {
		if n%d == 0 {
			return false
		}
	}
	return true
}

// Build constructs a Maglev lookup table of the given (prime) size for the
// provided backends. Backend names must be unique.
func Build(backends []string, size int) (*Table, error) {
	if len(backends) == 0 {
		return nil, errors.New("maglev: no backends")
	}
	if !IsPrime(size) {
		return nil, errors.New("maglev: table size must be prime")
	}
	seen := make(map[string]bool, len(backends))
	for _, b := range backends {
		if seen[b] {
			return nil, errors.New("maglev: duplicate backend " + b)
		}
		seen[b] = true
	}

	n := len(backends)
	offset := make([]uint64, n)
	skip := make([]uint64, n)
	for i, b := range backends {
		offset[i] = hash1(b) % uint64(size)
		skip[i] = hash2(b)%uint64(size-1) + 1
	}

	lookup := make([]int32, size)
	for i := range lookup {
		lookup[i] = -1
	}

	// next[i] tracks how far backend i has advanced in its permutation.
	// The permutation value for step j is (offset + j*skip) mod size, so we
	// never materialize the full N x M permutation matrix.
	next := make([]uint64, n)
	filled := 0
	for filled < size {
		for i := 0; i < n && filled < size; i++ {
			for {
				c := (offset[i] + next[i]*skip[i]) % uint64(size)
				next[i]++
				if lookup[c] == -1 {
					lookup[c] = int32(i)
					filled++
					break
				}
			}
		}
	}

	bs := make([]string, n)
	copy(bs, backends)
	return &Table{size: size, backends: bs, lookup: lookup}, nil
}

// Lookup maps an arbitrary key to a backend in O(1).
func (t *Table) Lookup(key string) string {
	slot := hash1(key) % uint64(t.size)
	return t.backends[t.lookup[slot]]
}

// LookupIndex is like Lookup but returns the backend's index.
func (t *Table) LookupIndex(key string) int {
	slot := hash1(key) % uint64(t.size)
	return int(t.lookup[slot])
}

// Size returns the number of slots in the table.
func (t *Table) Size() int { return t.size }

// OwnerAtSlot returns the name of the backend owning the given slot.
// It is primarily useful for inspecting and comparing tables in tests
// and tooling.
func (t *Table) OwnerAtSlot(slot int) string {
	return t.backends[t.lookup[slot]]
}

// Backends returns the backend list the table was built with.
func (t *Table) Backends() []string {
	out := make([]string, len(t.backends))
	copy(out, t.backends)
	return out
}

// Distribution returns, for each backend index, how many slots it owns.
func (t *Table) Distribution() []int {
	counts := make([]int, len(t.backends))
	for _, owner := range t.lookup {
		counts[owner]++
	}
	return counts
}

// ChangedSlots returns the fraction of slots whose owner differs between
// two tables. The tables must have the same size. Backend identity is
// compared by name, so the backend lists may be ordered differently.
func ChangedSlots(a, b *Table) float64 {
	if a.size != b.size {
		panic("maglev: cannot compare tables of different sizes")
	}
	changed := 0
	for slot := 0; slot < a.size; slot++ {
		if a.backends[a.lookup[slot]] != b.backends[b.lookup[slot]] {
			changed++
		}
	}
	return float64(changed) / float64(a.size)
}
