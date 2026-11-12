package netsec

import (
	"bytes"
	"crypto/sha256"
)

// Seal is one entry of a partition's hash-chained prefix proof for a window.
//
// A seal is fully self-describing and generation-bound: Window and Partition
// name the exact slot, Seq is the 1-based record number, PrevDigest links to
// the previous seal (or to the partition anchor for seq 1) and Digest
// authenticates every field. JSON tags let the Go sealing host persist or
// transport seals without extra adapters.
type Seal struct {
	Window     WindowID    `json:"window"`
	Partition  PartitionID `json:"partition"`
	Seq        Seq         `json:"seq"`
	PrevDigest Digest      `json:"prev_digest"`
	Digest     Digest      `json:"digest"`
}

// canonicalSealMessage is the unambiguous byte stream that SealDigest hashes.
// Every variable-length field is length-prefixed, so distinct message tuples
// can never share an encoding.
func canonicalSealMessage(window WindowID, partition PartitionID, seq Seq, prev Digest) []byte {
	msg := make([]byte, 0, 1+8+8+8+32)
	msg = append(msg, domainSeal)
	msg = writeUint(msg, uint64(window))
	msg = writeString(msg, string(partition))
	msg = writeUint(msg, uint64(seq))
	msg = append(msg, prev[:]...)
	return msg
}

// SealDigest computes the canonical digest of one seal's fields.
func SealDigest(window WindowID, partition PartitionID, seq Seq, prev Digest) Digest {
	return sha256.Sum256(canonicalSealMessage(window, partition, seq, prev))
}

// NewSeal constructs a seal for the given slot. prev is the digest of the
// preceding seal, or the partition's trusted anchor when seq == 1. The
// returned Digest binds the window, partition, sequence and predecessor, so
// the barrier can later verify continuity and reject replayed generations.
func NewSeal(window WindowID, partition PartitionID, seq Seq, prev Digest) Seal {
	if seq == 0 {
		panic("barrier: sequence numbers are 1-based")
	}
	return Seal{
		Window:     window,
		Partition:  partition,
		Seq:        seq,
		PrevDigest: prev,
		Digest:     SealDigest(window, partition, seq, prev),
	}
}

// hasValidDigest reports whether the seal's Digest matches its own fields.
func (s Seal) hasValidDigest() bool {
	want := SealDigest(s.Window, s.Partition, s.Seq, s.PrevDigest)
	return bytes.Equal(s.Digest[:], want[:])
}

// Equal reports whether two seals carry identical fields.
func (s Seal) Equal(other Seal) bool {
	return s.Window == other.Window &&
		s.Partition == other.Partition &&
		s.Seq == other.Seq &&
		s.PrevDigest == other.PrevDigest &&
		s.Digest == other.Digest
}

// PrefixProof is the ordered, continuous seal chain one partition submits
// for a window. It must start at seq 1 and end at the manifest upper bound.
type PrefixProof struct {
	Partition PartitionID
	Seals     []Seal
}

// rootMessage is the canonical byte stream signed as the window root.
func rootMessage(window WindowID, manifestDigest, prevRoot Digest, roots []PartitionRoot) []byte {
	msg := make([]byte, 0, 1+8+32+32+len(roots)*(8+32))
	msg = append(msg, domainRoot)
	msg = writeUint(msg, uint64(window))
	msg = append(msg, manifestDigest[:]...)
	msg = append(msg, prevRoot[:]...)
	msg = writeUint(msg, uint64(len(roots)))
	for _, root := range roots {
		msg = writeString(msg, string(root.Partition))
		msg = append(msg, root.LastDigest[:]...)
	}
	return msg
}

// AggregateDigest combines the per-partition prefix roots into the single
// window root. PartitionRoot entries must be provided in sorted partition
// order; callers normally obtain them from a Barrier via Roots.
func AggregateDigest(window WindowID, manifestDigest, prevRoot Digest, roots []PartitionRoot) Digest {
	return sha256.Sum256(rootMessage(window, manifestDigest, prevRoot, roots))
}

// PartitionRoot is the terminal seal digest of one partition's proven
// prefix, in the order used by the aggregate root.
type PartitionRoot struct {
	Partition  PartitionID `json:"partition"`
	LastDigest Digest      `json:"last_digest"`
}
