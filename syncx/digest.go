// Package syncx implements the anti-entropy synchronization protocol:
// per round, peers exchange paginated shard digests, then pull only the
// ordered record ranges of shards whose digests disagree.
package syncx

import (
	"crypto/sha256"
	"encoding/binary"
	"hash/fnv"
	"sort"

	"antientropy/member"
)

// Wire-size accounting constants. Requests and responses are byte-limited;
// these constants model serialized sizes so the simulation can enforce and
// measure the limits without a real codec.
const (
	DigestSize           = 8 + 8 + 8 + sha256.Size
	DigestRequestBytes   = 16
	DigestResponseHeader = 16
	PullRequestBaseBytes = 32
	PullResponseHeader   = 32
	RecordOverhead       = 16
)

// RecordSize is the serialized size of one record inside a pull response.
func RecordSize(r member.Record) int { return len(r.NodeID) + RecordOverhead }

// ShardOf maps a node ID to its shard using a fixed hash (FNV-1a). The
// assignment is a pure function of (nodeID, numShards), so every node
// computes identical shard boundaries independently.
func ShardOf(nodeID string, numShards int) int {
	if numShards <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(nodeID))
	return int(h.Sum64() % uint64(numShards))
}

// Digest binds a shard's boundary (Shard, NumShards), its record Count and
// its content Hash. Two shards with equal digests hold identical record sets.
type Digest struct {
	Shard     int
	NumShards int
	Count     int
	Hash      [sha256.Size]byte
}

// ComputeDigest hashes the sorted records of one shard together with the
// shard boundary, so the digest commits to boundary, count and content.
func ComputeDigest(recs []member.Record, shardID, numShards int) Digest {
	sorted := make([]member.Record, len(recs))
	copy(sorted, recs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].NodeID < sorted[j].NodeID })
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(shardID))
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(numShards))
	h.Write(buf[:])
	for _, r := range sorted {
		h.Write([]byte(r.NodeID))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(buf[:], r.Incarnation)
		h.Write(buf[:])
		h.Write([]byte{byte(r.State)})
	}
	var hash [sha256.Size]byte
	copy(hash[:], h.Sum(nil))
	return Digest{Shard: shardID, NumShards: numShards, Count: len(sorted), Hash: hash}
}

// ChecksumRecords authenticates one page of a pull response so truncated or
// corrupted (incomplete) responses are detected by the requester.
func ChecksumRecords(recs []member.Record) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, r := range recs {
		h.Write([]byte(r.NodeID))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(buf[:], r.Incarnation)
		h.Write(buf[:])
		h.Write([]byte{byte(r.State)})
	}
	return h.Sum64()
}
