package gsb

import (
	"encoding/binary"
	"hash/fnv"
)

// Digest summarizes one shard. It binds the shard boundary (shard index and
// total shard count), the record count and the full content hash, taken at
// the shard snapshot Version.
type Digest struct {
	Shard     int
	NumShards int
	Count     int
	Hash      uint64
	Version   uint64
}

// SameContent reports whether two digests describe identical shard content.
func (d Digest) SameContent(o Digest) bool {
	return d.Shard == o.Shard &&
		d.NumShards == o.NumShards &&
		d.Count == o.Count &&
		d.Hash == o.Hash
}

// HashRecords computes the content hash of a shard. The hashed input covers
// the shard boundary (index and total shard count), the record count and
// every record in canonical (NodeID-ascending) order.
func HashRecords(shard, numShards int, recs []Record) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	putU64 := func(v uint64) {
		binary.LittleEndian.PutUint64(buf[:], v)
		h.Write(buf[:])
	}
	putU64(uint64(shard))
	putU64(uint64(numShards))
	putU64(uint64(len(recs)))
	for _, r := range recs {
		putU64(uint64(len(r.NodeID)))
		h.Write([]byte(r.NodeID))
		putU64(r.Incarnation)
		putU64(uint64(r.State))
	}
	return h.Sum64()
}
