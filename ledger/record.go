// Package ledger implements an append-only, hash-chained account ledger.
// Amounts are integer cents. Only the standard library is used.
package ledger

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Fixed wire layout, big-endian, field order is part of the format:
//
//	Seq        uint64    8
//	RequestID  [16]byte 16
//	From       uint64    8   debited account
//	To         uint64    8   credited account
//	Amount     int64     8   cents, always > 0
//	ReversalOf uint64    8   seq of the reversed record, 0 for normal transfers
//	PrevHash   [32]byte 32
//	Hash       [32]byte 32   sha256 over the 88 preceding bytes
const RecordSize = 120

const hashOffset = RecordSize - sha256.Size // 88

// RequestID is a fixed-length idempotency key (e.g. a UUID).
type RequestID [16]byte

// Record is one immutable ledger entry.
type Record struct {
	Seq        uint64
	RequestID  RequestID
	From       uint64
	To         uint64
	Amount     int64 // cents
	ReversalOf uint64
	PrevHash   [sha256.Size]byte
	Hash       [sha256.Size]byte
}

// Encode serializes r into buf (len >= RecordSize) and fills r.Hash.
func (r *Record) Encode(buf []byte) {
	binary.BigEndian.PutUint64(buf[0:8], r.Seq)
	copy(buf[8:24], r.RequestID[:])
	binary.BigEndian.PutUint64(buf[24:32], r.From)
	binary.BigEndian.PutUint64(buf[32:40], r.To)
	binary.BigEndian.PutUint64(buf[40:48], uint64(r.Amount))
	binary.BigEndian.PutUint64(buf[48:56], r.ReversalOf)
	copy(buf[56:88], r.PrevHash[:])
	r.Hash = sha256.Sum256(buf[:hashOffset])
	copy(buf[hashOffset:RecordSize], r.Hash[:])
}

// DecodeRecord parses one fixed-size record and verifies its self-hash.
// A mismatch means a complete record was corrupted; the caller reports
// the position.
func DecodeRecord(buf []byte) (Record, error) {
	var r Record
	if len(buf) != RecordSize {
		return r, fmt.Errorf("record needs exactly %d bytes, got %d", RecordSize, len(buf))
	}
	r.Seq = binary.BigEndian.Uint64(buf[0:8])
	copy(r.RequestID[:], buf[8:24])
	r.From = binary.BigEndian.Uint64(buf[24:32])
	r.To = binary.BigEndian.Uint64(buf[32:40])
	r.Amount = int64(binary.BigEndian.Uint64(buf[40:48]))
	r.ReversalOf = binary.BigEndian.Uint64(buf[48:56])
	copy(r.PrevHash[:], buf[56:88])
	copy(r.Hash[:], buf[hashOffset:RecordSize])
	if want := sha256.Sum256(buf[:hashOffset]); want != r.Hash {
		return r, errors.New("record hash mismatch")
	}
	return r, nil
}

// CorruptError reports a complete record whose stored hash does not match
// its contents. Offset is the byte offset of the record in the file.
type CorruptError struct {
	Offset int64
	Seq    uint64
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("ledger corrupt at byte offset %d (record seq %d): hash mismatch", e.Offset, e.Seq)
}
