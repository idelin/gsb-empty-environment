// Package simhashidx implements a near-duplicate index over 64-bit SimHash
// fingerprints.
//
// A query returns every stored fingerprint whose Hamming distance to the query
// fingerprint is at most MaxHamming (3). The index is built from segmented
// permutation tables: each fingerprint is split into four 16-bit blocks and
// inserted into one bucket per block. Because a fingerprint within distance 3
// must match the query exactly on at least one of the four blocks
// (3 flips cannot cover all four blocks), probing the matching bucket of every
// table is guaranteed to find every neighbor while only scanning a small set
// of candidate fingerprints.
package simhashidx

import (
	"hash/fnv"
	"sort"
)

// MaxHamming is the largest Hamming distance supported by the segmented
// tables. With tableCount (4) segments and tableCount-1 (3) allowed flips,
// every near neighbor shares at least one unchanged segment with the query.
const MaxHamming = 3

const (
	tableCount = 4                        // number of segmented permutation tables
	blockBits  = 64 / tableCount          // bits covered by one segment (16)
	blockMask  = uint64(1)<<blockBits - 1 // low-block bucket key mask
)

// Feature is a weighted SimHash input token. Weight is typically the token's
// term frequency inside the document.
type Feature struct {
	Token  string
	Weight int
}

// WeightedTokens converts an ordered token stream into weighted features,
// using each token's occurrence count as its weight.
func WeightedTokens(tokens []string) []Feature {
	counts := make(map[string]int, len(tokens))
	order := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, seen := counts[token]; !seen {
			order = append(order, token)
		}
		counts[token]++
	}
	features := make([]Feature, 0, len(order))
	for _, token := range order {
		features = append(features, Feature{Token: token, Weight: counts[token]})
	}
	return features
}

// hashToken hashes a token to a stable 64-bit value with FNV-1a.
func hashToken(token string) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(token))
	return hasher.Sum64()
}

// SimHash computes a 64-bit SimHash fingerprint from weighted features. Each
// feature contributes its weight positively to bit positions set in its token
// hash and negatively elsewhere; the fingerprint bit is the sign of the
// accumulated weight, with zero accumulation resolving to zero.
func SimHash(features []Feature) uint64 {
	var accumulator [64]int64
	for _, feature := range features {
		if feature.Weight <= 0 {
			continue
		}
		hash := hashToken(feature.Token)
		weight := int64(feature.Weight)
		for bit := 0; bit < 64; bit++ {
			if hash&(uint64(1)<<bit) != 0 {
				accumulator[bit] += weight
			} else {
				accumulator[bit] -= weight
			}
		}
	}

	var fingerprint uint64
	for bit := 0; bit < 64; bit++ {
		if accumulator[bit] > 0 {
			fingerprint |= uint64(1) << bit
		}
	}
	return fingerprint
}

// Hamming returns the popcount of x xor y, i.e. the number of differing bits.
func Hamming(x, y uint64) int {
	diff := x ^ y
	// Software popcount, equivalent to bits.OnesCount64.
	diff = diff - ((diff >> 1) & 0x5555555555555555)
	diff = (diff & 0x3333333333333333) + ((diff >> 2) & 0x3333333333333333)
	diff = (diff + (diff >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((diff * 0x0101010101010101) >> 56)
}

// Index is a near-duplicate index of SimHash fingerprints.
//
// Every table corresponds to one 16-bit block of the fingerprint. A table maps
// the block value (bucket key) to the multiset of stored fingerprints having
// that block value. Multisets let the same fingerprint be inserted more than
// once and keep deletions symmetric with insertions.
type Index struct {
	tables [tableCount]map[uint64]map[uint64]int
	size   int
}

// NewIndex creates an empty segmented permutation-table index.
func NewIndex() *Index {
	index := &Index{}
	for table := range index.tables {
		index.tables[table] = make(map[uint64]map[uint64]int)
	}
	return index
}

// blockKey extracts the 16-bit bucket key of table for fingerprint. Table 0
// uses the low block and each following table rotates the fingerprint left by
// one block, so the four tables together cover every 16-bit segment.
func blockKey(table int, fingerprint uint64) uint64 {
	if table == 0 {
		return fingerprint & blockMask
	}
	shift := uint(table * blockBits)
	return ((fingerprint << shift) | (fingerprint >> (64 - shift))) & blockMask
}

// Insert adds one occurrence of fingerprint to every permutation table.
func (index *Index) Insert(fingerprint uint64) {
	for table := range index.tables {
		key := blockKey(table, fingerprint)
		bucket := index.tables[table][key]
		if bucket == nil {
			bucket = make(map[uint64]int)
			index.tables[table][key] = bucket
		}
		bucket[fingerprint]++
	}
	index.size++
}

// Delete removes one occurrence of fingerprint from every table. It reports
// whether the fingerprint was present. Empty buckets are removed so table
// counts reflect only live data; a deleted fingerprint is never returned by a
// later query.
func (index *Index) Delete(fingerprint uint64) bool {
	// Every table holds the same occurrences, so the first table decides
	// whether the fingerprint exists at all.
	firstKey := blockKey(0, fingerprint)
	if index.tables[0][firstKey][fingerprint] == 0 {
		return false
	}

	for table := range index.tables {
		key := blockKey(table, fingerprint)
		bucket := index.tables[table][key]
		bucket[fingerprint]--
		if bucket[fingerprint] == 0 {
			delete(bucket, fingerprint)
		}
		if len(bucket) == 0 {
			delete(index.tables[table], key)
		}
	}
	index.size--
	return true
}

// Query returns the distinct stored fingerprints whose Hamming distance to
// queryFingerprint is at most MaxHamming (3), sorted ascending.
//
// Only the buckets whose key equals the query's block in each of the four
// permutation tables are inspected. Any fingerprint within distance 3 must
// match one 16-bit block of the query exactly, so no neighbor can be missed.
func (index *Index) Query(queryFingerprint uint64) []uint64 {
	matches := make(map[uint64]struct{})
	for table := range index.tables {
		key := blockKey(table, queryFingerprint)
		bucket := index.tables[table][key]
		for candidate := range bucket {
			if Hamming(queryFingerprint, candidate) <= MaxHamming {
				matches[candidate] = struct{}{}
			}
		}
	}

	result := make([]uint64, 0, len(matches))
	for fingerprint := range matches {
		result = append(result, fingerprint)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// QueryCandidates works like Query but also returns the number of candidate
// entries inspected across the four probed buckets, including candidates
// rejected by the exact Hamming-distance check. It is useful for measuring how
// much work the segmented tables save versus a full scan.
func (index *Index) QueryCandidates(queryFingerprint uint64) (matches []uint64, candidates int) {
	found := make(map[uint64]struct{})
	for table := range index.tables {
		key := blockKey(table, queryFingerprint)
		bucket := index.tables[table][key]
		candidates += len(bucket)
		for candidate := range bucket {
			if Hamming(queryFingerprint, candidate) <= MaxHamming {
				found[candidate] = struct{}{}
			}
		}
	}

	matches = make([]uint64, 0, len(found))
	for fingerprint := range found {
		matches = append(matches, fingerprint)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i] < matches[j] })
	return matches, candidates
}

// Len returns the number of stored fingerprint occurrences.
func (index *Index) Len() int {
	return index.size
}

// Stats summarizes the physical layout of the permutation tables.
type Stats struct {
	TableCount   int // number of permutation tables (always 4)
	BucketCount  int // total non-empty buckets across every table
	Occurrences  int // total fingerprint occurrences across every table
	Fingerprints int // number of stored fingerprint occurrences (Len)
}

// Stats collects table and bucket counts for the current index.
func (index *Index) Stats() Stats {
	stats := Stats{
		TableCount:   tableCount,
		Fingerprints: index.size,
	}
	for table := range index.tables {
		for _, bucket := range index.tables[table] {
			stats.BucketCount++
			stats.Occurrences += len(bucket)
		}
	}
	return stats
}

// BruteForce is the reference implementation: it scans every distinct stored
// fingerprint and returns those within maxDistance of queryFingerprint,
// sorted ascending. Tests compare it against the permutation-table index.
func BruteForce(stored []uint64, queryFingerprint uint64, maxDistance int) []uint64 {
	matches := make(map[uint64]struct{})
	for _, fingerprint := range stored {
		if Hamming(queryFingerprint, fingerprint) <= maxDistance {
			matches[fingerprint] = struct{}{}
		}
	}
	result := make([]uint64, 0, len(matches))
	for fingerprint := range matches {
		result = append(result, fingerprint)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
