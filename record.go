// Package ledger implements an append-only, hash-chained ledger with
// fixed-length records. Amounts are integer cents. Only the standard
// library is used.
package ledger

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Fixed wire layout. Field order and sizes are part of the format and
// must never change:
//
//	seq        uint64   8
//	kind       uint8    1
//	reqID      [16]byte 16
//	refSeq     uint64   8
//	acctA      [16]byte 16
//	deltaA     int64    8
//	acctB      [16]byte 16
//	deltaB     int64    8
//	prevDigest [32]byte 32  digest of the previous record
//	digest     [32]byte 32  sha256 of all fields above
const (
	ReqIDLen  = 16
	AcctLen   = 16
	DigestLen = 32

	headerSize = 8 + 1 + ReqIDLen + 8 + AcctLen + 8 + AcctLen + 8 + DigestLen
	// RecordSize is the exact encoded length of every record.
	RecordSize = headerSize + DigestLen
)

// Kind is the record type.
type Kind uint8

const (
	KindFund     Kind = 1 // credit an account (external money enters)
	KindTransfer Kind = 2 // move money between two accounts
	KindReverse  Kind = 3 // full reversal of a committed transfer
)

func (k Kind) String() string {
	switch k {
	case KindFund:
		return "fund"
	case KindTransfer:
		return "transfer"
	case KindReverse:
		return "reverse"
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// Record is one ledger entry. DeltaA/DeltaB are signed cent amounts
// applied to AcctA/AcctB. A zero AcctB means the slot is unused.
type Record struct {
	Seq        uint64
	Kind       Kind
	ReqID      [ReqIDLen]byte
	RefSeq     uint64
	AcctA      [AcctLen]byte
	DeltaA     int64
	AcctB      [AcctLen]byte
	DeltaB     int64
	PrevDigest [DigestLen]byte
	Digest     [DigestLen]byte
}

var zeroAcct [AcctLen]byte

// padFixed copies s into a fixed-size array; s must fit.
func padFixed(s string, n int, what string) ([AcctLen]byte, error) {
	var out [AcctLen]byte
	if len(s) == 0 {
		return out, fmt.Errorf("%s must not be empty", what)
	}
	if len(s) > n {
		return out, fmt.Errorf("%s %q exceeds %d bytes", what, s, n)
	}
	copy(out[:], s)
	return out, nil
}

func padReqID(s string) ([ReqIDLen]byte, error) {
	b, err := padFixed(s, ReqIDLen, "request ID")
	return [ReqIDLen]byte(b), err
}

func padAcct(s string) ([AcctLen]byte, error) {
	return padFixed(s, AcctLen, "account")
}

// TrimString converts a fixed-size field back to a string.
func TrimString(b []byte) string {
	end := len(b)
	for end > 0 && b[end-1] == 0 {
		end--
	}
	return string(b[:end])
}

// header serializes every field except Digest, in fixed order.
func (r *Record) header() []byte {
	buf := make([]byte, headerSize)
	off := 0
	binary.BigEndian.PutUint64(buf[off:], r.Seq)
	off += 8
	buf[off] = byte(r.Kind)
	off++
	copy(buf[off:], r.ReqID[:])
	off += ReqIDLen
	binary.BigEndian.PutUint64(buf[off:], r.RefSeq)
	off += 8
	copy(buf[off:], r.AcctA[:])
	off += AcctLen
	binary.BigEndian.PutUint64(buf[off:], uint64(r.DeltaA))
	off += 8
	copy(buf[off:], r.AcctB[:])
	off += AcctLen
	binary.BigEndian.PutUint64(buf[off:], uint64(r.DeltaB))
	off += 8
	copy(buf[off:], r.PrevDigest[:])
	return buf
}

// computeDigest returns the digest of the record's header fields.
func (r *Record) computeDigest() [DigestLen]byte {
	return sha256.Sum256(r.header())
}

// Marshal encodes the record, filling in Digest from the other fields.
func (r *Record) Marshal() []byte {
	r.Digest = r.computeDigest()
	out := make([]byte, 0, RecordSize)
	out = append(out, r.header()...)
	out = append(out, r.Digest[:]...)
	return out
}

// UnmarshalRecord decodes one record and verifies its self digest.
func UnmarshalRecord(buf []byte) (*Record, error) {
	if len(buf) != RecordSize {
		return nil, fmt.Errorf("record has %d bytes, want %d", len(buf), RecordSize)
	}
	r := &Record{}
	off := 0
	r.Seq = binary.BigEndian.Uint64(buf[off:])
	off += 8
	r.Kind = Kind(buf[off])
	off++
	copy(r.ReqID[:], buf[off:])
	off += ReqIDLen
	r.RefSeq = binary.BigEndian.Uint64(buf[off:])
	off += 8
	copy(r.AcctA[:], buf[off:])
	off += AcctLen
	r.DeltaA = int64(binary.BigEndian.Uint64(buf[off:]))
	off += 8
	copy(r.AcctB[:], buf[off:])
	off += AcctLen
	r.DeltaB = int64(binary.BigEndian.Uint64(buf[off:]))
	off += 8
	copy(r.PrevDigest[:], buf[off:])
	off += DigestLen
	copy(r.Digest[:], buf[off:])
	if r.computeDigest() != r.Digest {
		return nil, errors.New("record digest mismatch")
	}
	return r, nil
}
