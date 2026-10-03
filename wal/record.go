// Package wal implements the write-ahead log used by the ARIES
// recovery protocol: record types, binary encoding with CRC
// integrity framing, and a log manager supporting append, flush
// and sequential/random scans.
package wal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"
)

// RecordType enumerates the ARIES log record kinds.
type RecordType byte

const (
	TypeInvalid RecordType = iota
	TypeBegin
	TypeUpdate
	TypeCommit
	TypeAbort
	TypeEnd
	TypeCLR
	TypeCheckpoint
)

func (t RecordType) String() string {
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
	default:
		return "INVALID"
	}
}

// Record is a single WAL entry. LSNs are assigned by the LogManager
// on append. A zero PrevLSN / UndoNext means "no link".
type Record struct {
	LSN      uint64
	Type     RecordType
	TxnID    int
	PrevLSN  uint64 // previous record of the same transaction
	PageID   int    // UPDATE / CLR only
	UndoNext uint64 // CLR only: next record to undo
	Before   []byte // UPDATE: before image; CLR: image being undone
	After    []byte // UPDATE: after image; CLR: restoration image
	ATT      map[int]uint64
	DPT      map[int]uint64
}

func (r *Record) String() string {
	switch r.Type {
	case TypeUpdate, TypeCLR:
		return fmt.Sprintf("LSN=%-3d %-5s txn=%d prev=%d page=%d undoNext=%d before=%q after=%q",
			r.LSN, r.Type, r.TxnID, r.PrevLSN, r.PageID, r.UndoNext, r.Before, r.After)
	case TypeCheckpoint:
		return fmt.Sprintf("LSN=%-3d CHECKPOINT att=%v dpt=%v", r.LSN, r.ATT, r.DPT)
	default:
		return fmt.Sprintf("LSN=%-3d %-5s txn=%d prev=%d", r.LSN, r.Type, r.TxnID, r.PrevLSN)
	}
}

// maxRecordSize bounds a single record body to reject corrupt frames.
const maxRecordSize = 1 << 20

func encodeBody(r *Record) []byte {
	var buf bytes.Buffer
	buf.WriteByte(byte(r.Type))
	writeInt32(&buf, int32(r.TxnID))
	writeUint64(&buf, r.LSN)
	writeUint64(&buf, r.PrevLSN)
	writeInt32(&buf, int32(r.PageID))
	writeUint64(&buf, r.UndoNext)
	writeBytes(&buf, r.Before)
	writeBytes(&buf, r.After)
	writeUint32(&buf, uint32(len(r.ATT)))
	for _, k := range sortedKeys(r.ATT) {
		writeInt32(&buf, int32(k))
		writeUint64(&buf, r.ATT[k])
	}
	writeUint32(&buf, uint32(len(r.DPT)))
	for _, k := range sortedKeys(r.DPT) {
		writeInt32(&buf, int32(k))
		writeUint64(&buf, r.DPT[k])
	}
	return buf.Bytes()
}

func decodeBody(body []byte) (*Record, error) {
	d := &decoder{b: body}
	r := &Record{
		Type:     RecordType(d.u8()),
		TxnID:    int(d.i32()),
		LSN:      d.u64(),
		PrevLSN:  d.u64(),
		PageID:   int(d.i32()),
		UndoNext: d.u64(),
		Before:   d.bytes(),
		After:    d.bytes(),
	}
	if d.err != nil {
		return nil, d.err
	}
	if n := int(d.u32()); d.err == nil && n > 0 {
		r.ATT = make(map[int]uint64, n)
		for i := 0; i < n && d.err == nil; i++ {
			k := int(d.i32())
			v := d.u64()
			r.ATT[k] = v
		}
	}
	if d.err != nil {
		return nil, d.err
	}
	if n := int(d.u32()); d.err == nil && n > 0 {
		r.DPT = make(map[int]uint64, n)
		for i := 0; i < n && d.err == nil; i++ {
			k := int(d.i32())
			v := d.u64()
			r.DPT[k] = v
		}
	}
	if d.err != nil {
		return nil, d.err
	}
	if d.off != len(d.b) {
		return nil, fmt.Errorf("wal: %d trailing bytes in record body", len(d.b)-d.off)
	}
	return r, nil
}

// encodeFrame wraps a record body with a length and CRC32 header.
func encodeFrame(r *Record) []byte {
	body := encodeBody(r)
	frame := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(body)))
	binary.BigEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(body))
	copy(frame[8:], body)
	return frame
}

func decodeFrame(hdr, body []byte) (*Record, error) {
	bodyLen := binary.BigEndian.Uint32(hdr[0:4])
	crc := binary.BigEndian.Uint32(hdr[4:8])
	if bodyLen == 0 || bodyLen > maxRecordSize {
		return nil, fmt.Errorf("wal: implausible record length %d", bodyLen)
	}
	if crc32.ChecksumIEEE(body) != crc {
		return nil, fmt.Errorf("wal: CRC mismatch")
	}
	return decodeBody(body)
}

func writeUint64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeInt32(buf *bytes.Buffer, v int32) { writeUint32(buf, uint32(v)) }

func writeBytes(buf *bytes.Buffer, b []byte) {
	writeUint32(buf, uint32(len(b)))
	buf.Write(b)
}

func sortedKeys(m map[int]uint64) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

type decoder struct {
	b   []byte
	off int
	err error
}

func (d *decoder) fail() {
	if d.err == nil {
		d.err = fmt.Errorf("wal: truncated record body")
	}
}

func (d *decoder) u8() uint8 {
	if d.err != nil {
		return 0
	}
	if d.off+1 > len(d.b) {
		d.fail()
		return 0
	}
	v := d.b[d.off]
	d.off++
	return v
}

func (d *decoder) u32() uint32 {
	if d.err != nil {
		return 0
	}
	if d.off+4 > len(d.b) {
		d.fail()
		return 0
	}
	v := binary.BigEndian.Uint32(d.b[d.off:])
	d.off += 4
	return v
}

func (d *decoder) i32() int32 { return int32(d.u32()) }

func (d *decoder) u64() uint64 {
	if d.err != nil {
		return 0
	}
	if d.off+8 > len(d.b) {
		d.fail()
		return 0
	}
	v := binary.BigEndian.Uint64(d.b[d.off:])
	d.off += 8
	return v
}

func (d *decoder) bytes() []byte {
	n := int(d.u32())
	if d.err != nil {
		return nil
	}
	if n < 0 || d.off+n > len(d.b) {
		d.fail()
		return nil
	}
	if n == 0 {
		return nil
	}
	out := make([]byte, n)
	copy(out, d.b[d.off:d.off+n])
	d.off += n
	return out
}
