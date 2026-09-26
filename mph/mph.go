// Package mph builds a minimal perfect hash index over a fixed set of
// unique string keys.
//
// The index is a real static structure, not a wrapped map: every key is
// assigned a collision-free slot in [0, n) via the CHD (hash, displace)
// scheme. Each bucket stores one uint16 displacement; each slot stores an
// 8-bit fingerprint plus the original key. Lookups for unknown keys are
// rejected in two stages: first the fingerprint, then a full comparison
// against the original key, so an unknown key can never be reported as a
// hit.
//
// Construction is reproducible: candidate seeds come from a deterministic
// splitmix64 sequence, and the number of seed attempts is bounded by
// MaxSeedTries. If no seed places every key, Build returns an
// *IncompleteError and no index at all -- keys are never silently dropped.
package mph

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// BucketSize is the target number of keys per displacement bucket.
	BucketSize = 4
	// MaxSeedTries bounds the reproducible seed search.
	MaxSeedTries = 4096
	// maxDisplace bounds the per-bucket displacement; displacements are
	// stored as uint16 in the encoded layout.
	maxDisplace = 1<<16 - 1
	// stepSeedXor derives the second hash function from the build seed.
	stepSeedXor = 0xD1B54A32D192ED03
)

// DuplicateError is returned by Build when the input contains repeated
// keys. The duplicates are diagnosed before any hashing work happens.
type DuplicateError struct {
	Keys   []string // each duplicated key, sorted, once
	Counts []int    // occurrence counts, parallel to Keys
}

func (e *DuplicateError) Error() string {
	parts := make([]string, len(e.Keys))
	for i, k := range e.Keys {
		parts[i] = fmt.Sprintf("%q x%d", k, e.Counts[i])
	}
	return fmt.Sprintf("mph: duplicate keys diagnosed before build: %s",
		strings.Join(parts, ", "))
}

// IncompleteError is returned when the bounded seed search cannot place
// every key. The build is reported as not complete; no partial index and
// no dropped keys.
type IncompleteError struct {
	Keys     int // number of keys that all had to be placed
	Attempts int // seed attempts actually consumed (the bound)
}

func (e *IncompleteError) Error() string {
	return fmt.Sprintf("mph: build incomplete: no seed placed all %d keys within %d bounded attempt(s); index not built, no keys dropped",
		e.Keys, e.Attempts)
}

// Index is a minimal perfect hash index over a fixed key set.
type Index struct {
	numKeys    uint32
	numBuckets uint32
	seed       uint64   // winning build seed (reproducible)
	attempts   uint32   // seed attempts consumed, 1-based count
	displ      []uint16 // one displacement per bucket
	fprints    []uint8  // one 8-bit fingerprint per slot
	keys       []string // original keys in slot order, for recheck
}

// Len returns the number of indexed keys.
func (ix *Index) Len() int { return int(ix.numKeys) }

// Seed returns the winning build seed.
func (ix *Index) Seed() uint64 { return ix.seed }

// Attempts returns how many seed attempts the build consumed.
// It is always in [1, MaxSeedTries] for a non-empty successful build.
func (ix *Index) Attempts() uint32 { return ix.attempts }

// NumBuckets returns the number of displacement buckets.
func (ix *Index) NumBuckets() int { return int(ix.numBuckets) }

// Build constructs an index over keys. The input is canonicalized (sorted)
// first, so the same key set always yields the same seed, attempt count and
// encoded bytes. Duplicate keys are diagnosed up front via *DuplicateError.
// If the bounded seed search fails, the error is *IncompleteError and the
// returned index is nil.
func Build(keys []string) (*Index, error) {
	return build(keys, MaxSeedTries)
}

func build(keys []string, seedBudget int) (*Index, error) {
	sorted := make([]string, len(keys))
	copy(sorted, keys)
	sort.Strings(sorted)
	if dup := diagnoseDuplicates(sorted); dup != nil {
		return nil, dup
	}
	n := len(sorted)
	ix := &Index{}
	if n == 0 {
		return ix, nil
	}
	nb := (n + BucketSize - 1) / BucketSize
	for attempt := 0; attempt < seedBudget; attempt++ {
		seed := seedForAttempt(uint64(attempt))
		if displ, ok := placeAll(sorted, seed, n, nb); ok {
			ix.numKeys = uint32(n)
			ix.numBuckets = uint32(nb)
			ix.seed = seed
			ix.attempts = uint32(attempt + 1)
			ix.displ = displ
			ix.fillSlots(sorted)
			return ix, nil
		}
	}
	return nil, &IncompleteError{Keys: n, Attempts: seedBudget}
}

// diagnoseDuplicates scans a sorted slice and reports every repeated key.
func diagnoseDuplicates(sorted []string) *DuplicateError {
	var dup *DuplicateError
	for i := 0; i < len(sorted); {
		j := i + 1
		for j < len(sorted) && sorted[j] == sorted[i] {
			j++
		}
		if j-i > 1 {
			if dup == nil {
				dup = &DuplicateError{}
			}
			dup.Keys = append(dup.Keys, sorted[i])
			dup.Counts = append(dup.Counts, j-i)
		}
		i = j
	}
	return dup
}

// placeAll tries to assign every key a free slot for the given seed.
// Buckets are processed largest-first; each bucket searches displacements
// d = 0,1,2,... until all of its keys land on free slots.
func placeAll(keys []string, seed uint64, n, nb int) ([]uint16, bool) {
	base := make([]uint64, n)
	step := make([]uint64, n)
	buckets := make([][]int, nb)
	for i, k := range keys {
		a := hashKey(k, seed)
		base[i] = a
		step[i] = hashKey(k, seed^stepSeedXor) | 1
		b := int(a % uint64(nb))
		buckets[b] = append(buckets[b], i)
	}
	order := make([]int, nb)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(x, y int) bool {
		if len(buckets[order[x]]) != len(buckets[order[y]]) {
			return len(buckets[order[x]]) > len(buckets[order[y]])
		}
		return order[x] < order[y]
	})
	occupied := make([]bool, n)
	displ := make([]uint16, nb)
	var tmp []int
	for _, b := range order {
		bucket := buckets[b]
		if len(bucket) == 0 {
			continue
		}
		placed := false
		for d := 0; d <= maxDisplace; d++ {
			tmp = tmp[:0]
			ok := true
			for _, i := range bucket {
				s := int((base[i] + uint64(d)*step[i]) % uint64(n))
				if occupied[s] || containsInt(tmp, s) {
					ok = false
					break
				}
				tmp = append(tmp, s)
			}
			if ok {
				for _, s := range tmp {
					occupied[s] = true
				}
				displ[b] = uint16(d)
				placed = true
				break
			}
		}
		if !placed {
			return nil, false
		}
	}
	return displ, true
}

func containsInt(v []int, x int) bool {
	for _, e := range v {
		if e == x {
			return true
		}
	}
	return false
}

// fillSlots stores keys and fingerprints in slot order after a successful
// placement.
func (ix *Index) fillSlots(sorted []string) {
	n := int(ix.numKeys)
	ix.keys = make([]string, n)
	ix.fprints = make([]uint8, n)
	for _, k := range sorted {
		slot := ix.slotOf(k)
		ix.keys[slot] = k
		ix.fprints[slot] = fingerprint(k, ix.seed)
	}
}

// slotOf computes the assigned slot of a key under the winning seed.
func (ix *Index) slotOf(key string) uint32 {
	a := hashKey(key, ix.seed)
	d := uint64(ix.displ[a%uint64(ix.numBuckets)])
	s := hashKey(key, ix.seed^stepSeedXor) | 1
	return uint32((a + d*s) % uint64(ix.numKeys))
}

// Lookup returns the slot of key in [0, Len()) and true, or false if key
// is not in the indexed set. Unknown keys are rejected by the fingerprint
// first and then by a full comparison with the stored original key, so an
// unknown key can never be reported as a hit.
func (ix *Index) Lookup(key string) (uint32, bool) {
	if ix.numKeys == 0 {
		return 0, false
	}
	a := hashKey(key, ix.seed)
	d := uint64(ix.displ[a%uint64(ix.numBuckets)])
	s := hashKey(key, ix.seed^stepSeedXor) | 1
	slot := uint32((a + d*s) % uint64(ix.numKeys))
	if ix.fprints[slot] != uint8(s>>56) {
		return 0, false
	}
	if ix.keys[slot] != key {
		return 0, false
	}
	return slot, true
}

// fingerprint is the top byte of the step hash; it matches the check in
// Lookup, where s is hashKey(key, seed^stepSeedXor)|1 (the low-bit fixup
// never touches the top byte).
func fingerprint(key string, seed uint64) uint8 {
	return uint8(hashKey(key, seed^stepSeedXor) >> 56)
}

// seedForAttempt maps an attempt counter to a candidate seed via
// splitmix64, making the seed sequence deterministic and reproducible.
func seedForAttempt(a uint64) uint64 {
	z := a + 0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// hashKey is FNV-1a-64 over the seed followed by the key. It uses only
// fixed constants, so hashes are stable across processes (unlike
// hash/maphash, which is randomly seeded per process).
func hashKey(key string, seed uint64) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < 8; i++ {
		h ^= uint64(byte(seed >> (8 * uint(i))))
		h *= prime64
	}
	for i := 0; i < len(key); i++ {
		h ^= uint64(key[i])
		h *= prime64
	}
	return h
}
