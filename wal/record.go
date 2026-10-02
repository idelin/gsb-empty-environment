// Package wal implements the write-ahead log used by the ARIES
// recovery engine: record encoding/decoding, CRC integrity checks,
// append-only writes and sequential scans.
package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// LSN is a log sequence number: the byte offset of a record in the log file.
type LSN = uint64

// TxnID identifies a transaction.
type TxnID = uint64

// PageID identifies a database page.
type PageID = uint64

// RecType enumerates the ARIES log record kinds.
type RecType uint8

const (
	TypeInvalid RecType = iota
	TypeBegin
	TypeUpdate
	TypeCommit
	TypeAbort
	TypeEnd
	TypeCLR
	TypeCheckpoint
)

func (t RecType) String() string {
	switch t {
	case TypeBegin:
		return "BEGIN"
	case TypeUpdate:
		return "UPDATE"
	case TypeCommit:
		return "COMMIT"
	case TypeAbort:
		return "ABORT"
	case TypeEnd:
		return "END"
	case TypeCLR:
		return "CLR"
	case TypeCheckpoint:
		return "CHECKPOINT"
	}
	return "INVALID"
}

// Record is a single WAL entry. OldData/NewData carry before/after images
// for UPDATE and CLR records; Payload carries the serialized checkpoint
// snapshot for CHECKPOINT records.
type Record struct {
	LSN      LSN
	Type     RecType
	TxnID    TxnID
	PrevLSN  LSN
	PageID   PageID
	Offset   uint32
	UndoNext LSN // CLR only: LSN of the next record to undo
	OldData  []byte
	NewData  []byte
	Payload  []byte
}

func (r *Record) String() string {
	return fmt.Sprintf("LSN=%d %s txn=%d prev=%d page=%d off=%d undoNext=%d",
		r.LSN, r.Type, r.TxnID, r.PrevLSN, r.PageID, r.Offset, r.UndoNext)
}

// encodedSize returns the exact on-disk size of the record.
func (r *Record) encodedSize() int {
	// 4 length + 4 crc + 1 type + 8*5 fixed uint64s + 4 offset + 3*4 lengths
	return 4 + 4 + 1 + 8*5 + 4 + 4*3 + len(r.OldData) + len(r.NewData) + len(r.Payload)
}

// encode serializes the record into buf, which must be at least encodedSize
// bytes long, and returns the number of bytes written.
func (r *Record) encode(buf []byte) int {
	size := r.encodedSize()
	binary.LittleEndian.PutUint32(buf[0:4], uint32(size))
	body := buf[8:]
	body[0] = byte(r.Type)
	binary.LittleEndian.PutUint64(body[1:9], r.LSN)
	binary.LittleEndian.PutUint64(body[9:17], r.TxnID)
	binary.LittleEndian.PutUint64(body[17:25], r.PrevLSN)
	binary.LittleEndian.PutUint64(body[25:33], r.PageID)
	binary.LittleEndian.PutUint32(body[33:37], r.Offset)
	binary.LittleEndian.PutUint64(body[37:45], r.UndoNext)
	binary.LittleEndian.PutUint32(body[45:49], uint32(len(r.OldData)))
	binary.LittleEndian.PutUint32(body[49:53], uint32(len(r.NewData)))
	binary.LittleEndian.PutUint32(body[53:57], uint32(len(r.Payload)))
	p := 57
	copy(body[p:], r.OldData)
	p += len(r.OldData)
	copy(body[p:], r.NewData)
	p += len(r.NewData)
	copy(body[p:], r.Payload)
	p += len(r.Payload)
	binary.LittleEndian.PutUint32(buf[4:8], crc32.ChecksumIEEE(body[:p]))
	return size
}

// ErrCorruptTail marks a torn or corrupt record at the tail of the log,
// which is expected after a crash and simply terminates the scan.
var ErrCorruptTail = errors.New("wal: corrupt or torn record at tail")

// decodeRecord parses one record from buf starting at offset 0.
// It returns the record and its total size in bytes.
func decodeRecord(buf []byte) (*Record, int, error) {
	if len(buf) < 8 {
		return nil, 0, ErrCorruptTail
	}
	size := int(binary.LittleEndian.Uint32(buf[0:4]))
	if size < 8+57 || size > len(buf) {
		return nil, 0, ErrCorruptTail
	}
	body := buf[8:size]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(buf[4:8]) {
		return nil, 0, ErrCorruptTail
	}
	r := &Record{
		Type:     RecType(body[0]),
		LSN:      binary.LittleEndian.Uint64(body[1:9]),
		TxnID:    binary.LittleEndian.Uint64(body[9:17]),
		PrevLSN:  binary.LittleEndian.Uint64(body[17:25]),
		PageID:   binary.LittleEndian.Uint64(body[25:33]),
		Offset:   binary.LittleEndian.Uint32(body[33:37]),
		UndoNext: binary.LittleEndian.Uint64(body[37:45]),
	}
	oldLen := int(binary.LittleEndian.Uint32(body[45:49]))
	newLen := int(binary.LittleEndian.Uint32(body[49:53]))
	payLen := int(binary.LittleEndian.Uint32(body[53:57]))
	p := 57
	if p+oldLen+newLen+payLen > len(body) {
		return nil, 0, ErrCorruptTail
	}
	r.OldData = append([]byte(nil), body[p:p+oldLen]...)
	p += oldLen
	r.NewData = append([]byte(nil), body[p:p+newLen]...)
	p += newLen
	r.Payload = append([]byte(nil), body[p:p+payLen]...)
	return r, size, nil
}
