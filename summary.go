package syncsim

import (
	"encoding/binary"
	"hash/fnv"
	"sort"
)

// The summary tree partitions the key space by nibbles of a 32-bit key
// hash: arity 16, depth 8. Each subtree has a digest; equal digests mean
// "same content" (up to hash collisions, which the audit path covers).
const (
	Arity = 16
	Depth = 8
)

// Digest identifies the content of one subtree.
type Digest uint64

// DigestHash hashes a canonical encoding to a digest. It is injectable so
// tests can install a weak hash and force summary collisions on demand.
type DigestHash func([]byte) uint64

// FnvDigest is the production digest hash (FNV-1a 64).
func FnvDigest(b []byte) uint64 {
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}

// KeyHash maps a key into the 32-bit summary-tree space.
func KeyHash(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32()
}

// extend returns the prefix of the child nibble below (prefix, plen).
func extend(prefix uint32, nibble, plen int) uint32 {
	return prefix | uint32(nibble)<<uint(28-4*plen)
}

func underPrefix(keyHash, prefix uint32, plen int) bool {
	if plen <= 0 {
		return true
	}
	mask := uint32(0xFFFFFFFF) << uint(32-4*plen)
	return keyHash&mask == prefix&mask
}

// Summary is an immutable digest view over a snapshot of records.
type Summary struct {
	dh      DigestHash
	records []Record // sorted by (KeyHash, Key) for deterministic folding
	hashes  []uint32
}

// BuildSummary snapshots records into a Summary.
func BuildSummary(dh DigestHash, recs map[string]Record) *Summary {
	s := &Summary{dh: dh}
	for _, r := range recs {
		s.records = append(s.records, r)
	}
	sort.Slice(s.records, func(i, j int) bool {
		hi, hj := KeyHash(s.records[i].Key), KeyHash(s.records[j].Key)
		if hi != hj {
			return hi < hj
		}
		return s.records[i].Key < s.records[j].Key
	})
	s.hashes = make([]uint32, len(s.records))
	for i, r := range s.records {
		s.hashes[i] = KeyHash(r.Key)
	}
	return s
}

func (s *Summary) leafDigest(r Record) Digest {
	var buf []byte
	buf = append(buf, "leaf"...)
	buf = append(buf, r.Key...)
	buf = append(buf, 0)
	buf = append(buf, r.Value...)
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], r.Ver.Clock)
	buf = append(buf, tmp[:]...)
	buf = append(buf, r.Ver.Node...)
	if r.Tombstone {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	return Digest(s.dh(buf))
}

// Digest folds the leaf digests of every record under (prefix, plen),
// ordered by key hash, into one subtree digest. The empty subtree is 0.
func (s *Summary) Digest(prefix uint32, plen int) Digest {
	var buf []byte
	buf = append(buf, "node"...)
	found := false
	for i, r := range s.records {
		if !underPrefix(s.hashes[i], prefix, plen) {
			continue
		}
		found = true
		var tmp [8]byte
		binary.BigEndian.PutUint64(tmp[:], uint64(s.leafDigest(r)))
		buf = append(buf, tmp[:]...)
	}
	if !found {
		return 0
	}
	return Digest(s.dh(buf))
}

// Children returns the digest of each non-empty child subtree of the node
// at (prefix, plen), keyed by nibble.
func (s *Summary) Children(prefix uint32, plen int) map[int]Digest {
	out := make(map[int]Digest)
	for nib := 0; nib < Arity; nib++ {
		d := s.Digest(extend(prefix, nib, plen), plen+1)
		if d != 0 {
			out[nib] = d
		}
	}
	return out
}

// Leaves returns all records (tombstones included) under (prefix, plen).
func (s *Summary) Leaves(prefix uint32, plen int) []Record {
	var out []Record
	for i, r := range s.records {
		if underPrefix(s.hashes[i], prefix, plen) {
			out = append(out, r)
		}
	}
	return out
}
