// Package maglev implements Google's Maglev consistent-hashing lookup table.
//
// The algorithm builds a fixed-size lookup table (a prime number of slots,
// e.g. 65537) from a set of backend nodes. Each backend generates a
// globally-unique permutation of the slot indices (its "preference list")
// from two independent hashes of its name. Slots are then filled in
// round-robin order: each backend claims the first still-empty slot in its
// permutation. The result is an O(1) lookup table with near-perfectly even
// distribution and minimal disruption when backends are added or removed.
package maglev

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"sort"
)

// DefaultTableSize is the table size recommended by the Maglev paper
// (a prime number close to 2^16).
const DefaultTableSize = 65537

// Table is a Maglev lookup table. Lookup is a single array index: O(1).
type Table struct {
	size    int
	entries []int // entries[slot] = index into nodes
	nodes   []string
}

// hash1 and hash2 are two independent hash functions used to derive each
// backend's permutation offset and skip. The Maglev paper uses Karmarkar's
// hashes; any two independent hashes work. We use MD5 and SHA-1.
func hash1(name string) uint64 {
	sum := md5.Sum([]byte(name))
	return binary.BigEndian.Uint64(sum[:8])
}

func hash2(name string) uint64 {
	sum := sha1.Sum([]byte(name))
	return binary.BigEndian.Uint64(sum[:8])
}

// permutation generates the preference list of backend `name`:
// perm[j] = (offset + j*skip) % size, which visits every slot exactly once
// because size is prime and 0 < skip < size.
func permutation(name string, size int) []int {
	offset := int(hash1(name) % uint64(size))
	skip := int(hash2(name)%uint64(size-1)) + 1
	perm := make([]int, size)
	pos := offset
	for j := 0; j < size; j++ {
		perm[j] = pos
		pos += skip
		if pos >= size {
			pos -= size
		}
	}
	return perm
}

// Build constructs a Maglev lookup table for the given backend node names
// with the given (prime) table size.
func Build(nodes []string, size int) (*Table, error) {
	if size < 3 {
		return nil, errors.New("maglev: table size must be >= 3")
	}
	if len(nodes) == 0 {
		return nil, errors.New("maglev: need at least one node")
	}
	// Sort for deterministic output regardless of input order.
	sorted := make([]string, len(nodes))
	copy(sorted, nodes)
	sort.Strings(sorted)

	n := len(sorted)
	perms := make([][]int, n)
	for i, name := range sorted {
		perms[i] = permutation(name, size)
	}

	entries := make([]int, size)
	for i := range entries {
		entries[i] = -1
	}
	next := make([]int, n) // per-backend cursor into its permutation
	filled := 0
	for filled < size {
		for i := 0; i < n && filled < size; i++ {
			// Advance to the next empty slot in this backend's permutation.
			for entries[perms[i][next[i]]] != -1 {
				next[i]++
			}
			entries[perms[i][next[i]]] = i
			next[i]++
			filled++
		}
	}
	return &Table{size: size, entries: entries, nodes: sorted}, nil
}

// Lookup returns the backend node responsible for the given key in O(1).
func (t *Table) Lookup(key string) string {
	return t.nodes[t.entries[int(hash1(key)%uint64(t.size))]]
}

// Nodes returns the sorted backend node names in the table.
func (t *Table) Nodes() []string {
	out := make([]string, len(t.nodes))
	copy(out, t.nodes)
	return out
}

// Size returns the number of slots in the table.
func (t *Table) Size() int {
	return t.size
}

// Distribution returns how many slots each node owns.
func (t *Table) Distribution() map[string]int {
	dist := make(map[string]int, len(t.nodes))
	for _, idx := range t.entries {
		dist[t.nodes[idx]]++
	}
	return dist
}

// Entry returns the node index owning the given slot (used by tests).
func (t *Table) Entry(slot int) int {
	return t.entries[slot]
}
