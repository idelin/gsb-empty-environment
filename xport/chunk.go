// Package xport implements large-transaction chunked transfer with
// memory-protective rate limiting and disk spill-over.
package xport

import (
	"encoding/binary"
	"hash/fnv"
)

// Op is the kind of row change.
type Op byte

const (
	OpInsert Op = 'I'
	OpUpdate Op = 'U'
	OpDelete Op = 'D'
)

// Change is a single row-level change inside a transaction.
type Change struct {
	Op  Op
	Key uint64
	Val []byte
}

// Stage marks the position of a Chunk inside its transaction.
type Stage int

const (
	StageSingle Stage = iota // transaction fits in one chunk
	StageBegin               // first chunk of a split transaction
	StageMiddle              // intermediate chunk
	StageEnd                 // final chunk, carries transaction totals
)

func (s Stage) String() string {
	switch s {
	case StageSingle:
		return "SINGLE"
	case StageBegin:
		return "BEGIN"
	case StageMiddle:
		return "MIDDLE"
	case StageEnd:
		return "END"
	}
	return "UNKNOWN"
}

// Chunk is a bounded-size shard of a larger transaction.
type Chunk struct {
	TxID  uint64
	Seq   int // 0-based, strictly increasing within a transaction
	Stage Stage

	// The following fields are only meaningful on StageEnd / StageSingle
	// and are used by the receiver to verify transaction integrity.
	TotalChunks int
	TotalRows   int
	TxChecksum  uint64

	Changes  []Change
	Checksum uint64 // FNV-1a checksum of this chunk's changes
}

// Bytes estimates the in-memory footprint of the chunk payload.
func (c *Chunk) Bytes() int64 {
	n := int64(64)
	for i := range c.Changes {
		n += int64(24 + len(c.Changes[i].Val))
	}
	return n
}

// chunkChecksum computes the FNV-1a hash of one chunk's changes.
func chunkChecksum(changes []Change) uint64 {
	h := fnv.New64a()
	var buf [9]byte
	for _, c := range changes {
		buf[0] = byte(c.Op)
		binary.LittleEndian.PutUint64(buf[1:], c.Key)
		h.Write(buf[:])
		h.Write(c.Val)
	}
	return h.Sum64()
}

// foldChecksums combines chunk checksums into a transaction-level checksum.
func foldChecksums(acc, chunkSum uint64) uint64 {
	return acc*1099511628211 ^ chunkSum
}
