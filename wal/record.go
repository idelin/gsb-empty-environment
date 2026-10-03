// Package wal implements the write-ahead log used by the ARIES
// recovery protocol: log record types, deterministic binary
// encoding, and an append-only log file.
package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// LSN is a monotonically increasing log sequence number.
// LSN 0 is reserved as NilLSN ("no record").
type LSN uint64

// NilLSN terminates PrevLSN / UndoNextLSN chains.
const NilLSN LSN = 0

// RecordType enumerates the ARIES log record kinds.
type RecordType uint8

const (
	TypeUpdate RecordType = iota + 1
	TypeCommit
	TypeAbort
	TypeCLR
	TypeEnd
	TypeCheckpoint
)

func (t RecordType) String() string {
	switch t {
	case TypeUpdate:
		return "UPDATE"
	case TypeCommit:
		return "COMMIT"
	case TypeAbort:
		return "ABORT"
	case TypeCLR:
		return "CLR"
	case TypeEnd:
		return "END"
	case TypeCheckpoint:
		return "CHECKPOINT"
	}
	return fmt.Sprintf("UNKNOWN(%d)", uint8(t))
}

// TxnStatus is the transaction state tracked in the transaction table.
type TxnStatus uint8

const (
	StatusRunning TxnStatus = iota
	StatusCommitted
)

func (s TxnStatus) String() string {
	if s == StatusCommitted {
		return "COMMITTED"
	}
	return "RUNNING"
}

// TxnCheckpointEntry is the per-transaction payload of a checkpoint record.
type TxnCheckpointEntry struct {
	LastLSN LSN
	Status  TxnStatus
}

// Record is a single WAL record. DPT/TT are only set for checkpoints.
type Record struct {
	LSN         LSN
	PrevLSN     LSN // previous record of the same transaction
	TxnID       uint32
	Type        RecordType
	PageID      uint32
	UndoNextLSN LSN // CLR only: next record to undo
	Before      []byte
	After       []byte
	DPT         map[uint32]LSN                // checkpoint: page -> recLSN
	TT          map[uint32]TxnCheckpointEntry // checkpoint: txn -> state
}

func (r *Record) String() string {
	base := fmt.Sprintf("LSN=%d %-10s txn=%d prev=%d", r.LSN, r.Type, r.TxnID, r.PrevLSN)
	switch r.Type {
	case TypeUpdate, TypeCLR:
		return fmt.Sprintf("%s page=%d undoNext=%d", base, r.PageID, r.UndoNextLSN)
	case TypeCheckpoint:
		return fmt.Sprintf("%s dpt=%v tt=%d txns", base, r.DPT, len(r.TT))
	}
	return base
}

// Encode serializes the record deterministically (map keys sorted).
func (r *Record) Encode() []byte {
	buf := new(bytes.Buffer)
	buf.WriteByte(byte(r.Type))
	writeUint64(buf, uint64(r.LSN))
	writeUint64(buf, uint64(r.PrevLSN))
	writeUint64(buf, uint64(r.TxnID))
	writeUint64(buf, uint64(r.PageID))
	writeUint64(buf, uint64(r.UndoNextLSN))
	writeBytes(buf, r.Before)
	writeBytes(buf, r.After)
	if r.Type == TypeCheckpoint {
		pages := make([]uint32, 0, len(r.DPT))
		for p := range r.DPT {
			pages = append(pages, p)
		}
		sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
		writeUint64(buf, uint64(len(pages)))
		for _, p := range pages {
			writeUint64(buf, uint64(p))
			writeUint64(buf, uint64(r.DPT[p]))
		}
		txns := make([]uint32, 0, len(r.TT))
		for t := range r.TT {
			txns = append(txns, t)
		}
		sort.Slice(txns, func(i, j int) bool { return txns[i] < txns[j] })
		writeUint64(buf, uint64(len(txns)))
		for _, t := range txns {
			writeUint64(buf, uint64(t))
			writeUint64(buf, uint64(r.TT[t].LastLSN))
			buf.WriteByte(byte(r.TT[t].Status))
		}
	}
	return buf.Bytes()
}

// DecodeRecord parses a record produced by Encode.
func DecodeRecord(data []byte) (*Record, error) {
	r := &Record{}
	rd := bytes.NewReader(data)
	typ, err := rd.ReadByte()
	if err != nil {
		return nil, err
	}
	r.Type = RecordType(typ)
	var v uint64
	if v, err = readUint64(rd); err != nil {
		return nil, err
	}
	r.LSN = LSN(v)
	if v, err = readUint64(rd); err != nil {
		return nil, err
	}
	r.PrevLSN = LSN(v)
	if v, err = readUint64(rd); err != nil {
		return nil, err
	}
	r.TxnID = uint32(v)
	if v, err = readUint64(rd); err != nil {
		return nil, err
	}
	r.PageID = uint32(v)
	if v, err = readUint64(rd); err != nil {
		return nil, err
	}
	r.UndoNextLSN = LSN(v)
	if r.Before, err = readBytes(rd); err != nil {
		return nil, err
	}
	if r.After, err = readBytes(rd); err != nil {
		return nil, err
	}
	if r.Type == TypeCheckpoint {
		n, err := readUint64(rd)
		if err != nil {
			return nil, err
		}
		r.DPT = make(map[uint32]LSN, n)
		for i := uint64(0); i < n; i++ {
			p, err1 := readUint64(rd)
			l, err2 := readUint64(rd)
			if err1 != nil || err2 != nil {
				return nil, errors.New("wal: corrupt checkpoint DPT")
			}
			r.DPT[uint32(p)] = LSN(l)
		}
		n, err = readUint64(rd)
		if err != nil {
			return nil, err
		}
		r.TT = make(map[uint32]TxnCheckpointEntry, n)
		for i := uint64(0); i < n; i++ {
			t, err1 := readUint64(rd)
			l, err2 := readUint64(rd)
			st, err3 := rd.ReadByte()
			if err1 != nil || err2 != nil || err3 != nil {
				return nil, errors.New("wal: corrupt checkpoint TT")
			}
			r.TT[uint32(t)] = TxnCheckpointEntry{LastLSN: LSN(l), Status: TxnStatus(st)}
		}
	}
	if rd.Len() != 0 {
		return nil, errors.New("wal: trailing bytes in record")
	}
	return r, nil
}

func writeUint64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func writeBytes(buf *bytes.Buffer, b []byte) {
	writeUint64(buf, uint64(len(b)))
	buf.Write(b)
}

func readUint64(rd *bytes.Reader) (uint64, error) {
	var b [8]byte
	if _, err := rd.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b[:]), nil
}

func readBytes(rd *bytes.Reader) ([]byte, error) {
	n, err := readUint64(rd)
	if err != nil {
		return nil, err
	}
	if n > uint64(rd.Len()) {
		return nil, errors.New("wal: byte field overruns record")
	}
	if n == 0 {
		return nil, nil
	}
	out := make([]byte, n)
	if _, err := rd.Read(out); err != nil {
		return nil, err
	}
	return out, nil
}
