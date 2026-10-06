// Package auditlog implements a hash-chained audit log and a merge
// algorithm for reconciling replicas that may be missing segments.
//
// Each entry carries a monotonic sequence number and the digest of the
// previous entry, forming a chain anchored at a zero genesis hash.
// Merging never picks a side by sequence height: it only emits the
// longest prefix that all non-tampered evidence agrees on, and reports
// forks as evidence instead of resolving them automatically.
package auditlog

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// Genesis is the Prev value of the first entry in a chain.
var Genesis [32]byte

// Entry is a single audit record. Hash commits to Seq, Prev and Data.
type Entry struct {
	Seq  uint64
	Data []byte
	Prev [32]byte
	Hash [32]byte
}

// ComputeHash derives the digest for an entry with the given fields.
func ComputeHash(seq uint64, prev [32]byte, data []byte) [32]byte {
	h := sha256.New()
	var seqBuf [8]byte
	binary.BigEndian.PutUint64(seqBuf[:], seq)
	h.Write(seqBuf[:])
	h.Write(prev[:])
	h.Write(data)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// NewEntry builds an entry with a correctly computed hash.
func NewEntry(seq uint64, prev [32]byte, data []byte) Entry {
	return Entry{Seq: seq, Data: data, Prev: prev, Hash: ComputeHash(seq, prev, data)}
}

// BuildChain constructs a valid chain starting at sequence 1.
func BuildChain(payloads ...[]byte) []Entry {
	entries := make([]Entry, 0, len(payloads))
	prev := Genesis
	for i, data := range payloads {
		e := NewEntry(uint64(i)+1, prev, data)
		entries = append(entries, e)
		prev = e.Hash
	}
	return entries
}

// ForkEvidence records two conflicting entries observed at the same
// sequence number. Both sides are preserved; the merge never chooses.
type ForkEvidence struct {
	Seq uint64
	A   Entry
	B   Entry
}

// Issue describes an entry rejected during validation.
type Issue struct {
	Replica int
	Seq     uint64
	Reason  string
}

// MergeResult is the outcome of merging replicas.
type MergeResult struct {
	// Prefix is the longest common trusted prefix: consecutive entries
	// from genesis with no contradiction and an intact hash chain.
	Prefix []Entry
	// Forks holds both sides of every conflicting sequence number.
	Forks []ForkEvidence
	// Issues lists entries rejected as tampered or unanchored.
	Issues []Issue
}

// Merge reconciles any number of replicas, each of which may contain
// gaps, duplicates or out-of-order entries. It returns the longest
// common trusted prefix plus fork evidence and validation issues.
//
// An entry is accepted into the prefix only if every replica that
// covers its sequence number agrees on the identical hash, the chain
// links continuously from genesis, and no earlier gap or fork exists.
// A replica that is merely longer never wins on height alone.
func Merge(replicas ...[]Entry) MergeResult {
	var res MergeResult
	bySeq := make(map[uint64]map[[32]byte]Entry)

	for idx, replica := range replicas {
		for _, e := range validateReplica(idx, replica, &res) {
			slots := bySeq[e.Seq]
			if slots == nil {
				slots = make(map[[32]byte]Entry)
				bySeq[e.Seq] = slots
			}
			if _, ok := slots[e.Hash]; !ok {
				slots[e.Hash] = e
			}
		}
	}

	seqs := make([]uint64, 0, len(bySeq))
	for seq := range bySeq {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	prev := Genesis
	broken := false
	for _, seq := range seqs {
		slots := bySeq[seq]
		if len(slots) > 1 {
			fork := ForkEvidence{Seq: seq}
			for _, e := range slots {
				if fork.A.Hash == ([32]byte{}) {
					fork.A = e
				} else {
					fork.B = e
					break
				}
			}
			res.Forks = append(res.Forks, fork)
			broken = true
			continue
		}
		var only Entry
		for _, e := range slots {
			only = e
		}
		if broken || seq != uint64(len(res.Prefix))+1 || only.Prev != prev {
			broken = true
			continue
		}
		res.Prefix = append(res.Prefix, only)
		prev = only.Hash
	}
	return res
}

// validateReplica filters a single replica: entries whose hash does
// not match their contents are rejected as tampered, and entries that
// do not link to their in-replica predecessor are rejected as
// unanchored. Duplicates of an identical entry collapse silently.
func validateReplica(idx int, replica []Entry, res *MergeResult) []Entry {
	sorted := make([]Entry, len(replica))
	copy(sorted, replica)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Seq != sorted[j].Seq {
			return sorted[i].Seq < sorted[j].Seq
		}
		return string(sorted[i].Hash[:]) < string(sorted[j].Hash[:])
	})

	var kept []Entry
	seen := make(map[[32]byte]bool)
	var lastSeq uint64
	var lastHash [32]byte
	haveLast := false
	for _, e := range sorted {
		if seen[e.Hash] {
			continue
		}
		seen[e.Hash] = true
		if e.Hash != ComputeHash(e.Seq, e.Prev, e.Data) {
			res.Issues = append(res.Issues, Issue{Replica: idx, Seq: e.Seq, Reason: "hash mismatch: entry tampered"})
			continue
		}
		if haveLast && e.Seq == lastSeq+1 && e.Prev != lastHash {
			res.Issues = append(res.Issues, Issue{Replica: idx, Seq: e.Seq, Reason: "broken link to predecessor"})
			continue
		}
		kept = append(kept, e)
		lastSeq = e.Seq
		lastHash = e.Hash
		haveLast = true
	}
	return kept
}

// Verify checks that entries form one intact chain from genesis.
func Verify(entries []Entry) error {
	prev := Genesis
	for i, e := range entries {
		want := uint64(i) + 1
		if e.Seq != want {
			return fmt.Errorf("entry %d: sequence %d, want %d", i, e.Seq, want)
		}
		if e.Prev != prev {
			return fmt.Errorf("entry %d: broken link to predecessor", i)
		}
		if e.Hash != ComputeHash(e.Seq, e.Prev, e.Data) {
			return fmt.Errorf("entry %d: hash mismatch", i)
		}
		prev = e.Hash
	}
	return nil
}
