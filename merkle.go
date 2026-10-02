package antientropy

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"
)

// bucketOf deterministically maps a node ID to one of 1<<depth leaf buckets.
// The low bits of FNV-1a carry the avalanche, so they index the buckets.
func bucketOf(id string, depth int) int {
	h := fnv.New64a()
	h.Write([]byte(id))
	return int(h.Sum64() & (uint64(1)<<uint(depth) - 1))
}

// hashBucket hashes the sorted contents of one leaf bucket.
func hashBucket(entries []NodeState) uint64 {
	if len(entries) == 0 {
		return 0
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	h := fnv.New64a()
	for _, e := range entries {
		fmt.Fprintf(h, "%s|%s|%d;", e.ID, e.Status, e.Version)
	}
	return h.Sum64()
}

// buildLevels builds a full binary Merkle tree over the snapshot, returning
// the levels root-first; the last level holds the 1<<depth leaf-bucket hashes.
func buildLevels(snapshot map[string]NodeState, depth int) [][]uint64 {
	leafCount := 1 << uint(depth)
	buckets := make(map[int][]NodeState)
	for _, st := range snapshot {
		b := bucketOf(st.ID, depth)
		buckets[b] = append(buckets[b], st)
	}
	leaves := make([]uint64, leafCount)
	for i := 0; i < leafCount; i++ {
		leaves[i] = hashBucket(buckets[i])
	}
	levels := [][]uint64{leaves}
	for len(levels[0]) > 1 {
		cur := levels[0]
		next := make([]uint64, len(cur)/2)
		for i := range next {
			h := fnv.New64a()
			var buf [16]byte
			binary.LittleEndian.PutUint64(buf[:8], cur[2*i])
			binary.LittleEndian.PutUint64(buf[8:], cur[2*i+1])
			h.Write(buf[:])
			next[i] = h.Sum64()
		}
		levels = append([][]uint64{next}, levels...)
	}
	return levels
}

// RootHash returns the Merkle root hash of a snapshot at the given depth.
func RootHash(snapshot map[string]NodeState, depth int) uint64 {
	return buildLevels(snapshot, depth)[0][0]
}

// entriesInBucket returns the snapshot entries falling into leaf bucket b.
func entriesInBucket(snapshot map[string]NodeState, b, depth int) map[string]NodeState {
	out := make(map[string]NodeState)
	for id, st := range snapshot {
		if bucketOf(id, depth) == b {
			out[id] = st
		}
	}
	return out
}
