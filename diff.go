package cdc

import "crypto/sha256"

// AlignDiff compares the old and new chunk sequences of two versions of
// the same stream and returns the minimal differing middle ranges:
// everything before and after the edited region matches by hash.
//
// Because chunking is content-defined, a local edit re-splits only a
// couple of chunks, so both returned slices are typically length 1-2
// even when the edit shifts the offsets of all later chunks.
func AlignDiff(oldChunks, newChunks []Chunk) (oldChanged, newChanged []Chunk) {
	n := len(oldChunks)
	if len(newChunks) < n {
		n = len(newChunks)
	}
	prefix := 0
	for prefix < n && oldChunks[prefix].Hash == newChunks[prefix].Hash {
		prefix++
	}
	suffix := 0
	for suffix < n-prefix &&
		oldChunks[len(oldChunks)-1-suffix].Hash == newChunks[len(newChunks)-1-suffix].Hash {
		suffix++
	}
	return oldChunks[prefix : len(oldChunks)-suffix],
		newChunks[prefix : len(newChunks)-suffix]
}

// ReuseRatio returns the fraction of new chunks whose hash also exists
// in the old chunk set (compared as multisets). 1.0 means every new
// chunk was already present in the old version.
func ReuseRatio(oldChunks, newChunks []Chunk) float64 {
	if len(newChunks) == 0 {
		return 1
	}
	available := make(map[[sha256.Size]byte]int, len(oldChunks))
	for _, c := range oldChunks {
		available[c.Hash]++
	}
	common := 0
	for _, c := range newChunks {
		if available[c.Hash] > 0 {
			available[c.Hash]--
			common++
		}
	}
	return float64(common) / float64(len(newChunks))
}
