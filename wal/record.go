// Package wal implements the write-ahead log used by the ARIES recovery
// engine: a compact binary record format, a sequential writer, and a
// memory-mapped zero-copy reader.
package wal

import "errors"

// RecType identifies the kind of a log record.
type RecType uint8

const (
	RecInvalid    RecType = iota
	RecUpdate             // physiological update: before/after image on a page
	RecCommit             // transaction commit decision
	RecAbort              // transaction abort decision
	RecCLR                // compensation log record (undo of an update)
	RecCheckpoint         // checkpoint carrying ATT + DPT snapshot
	RecEnd                // transaction fully finished (written after commit/undo)
)

// TxnStatus is the transaction state tracked in the transaction table.
type TxnStatus uint8

const (
	TxnRunning TxnStatus = iota
	TxnCommitted
	TxnAborting
)

// NilLSN terminates the prevLSN / undoNextLSN chains.
const NilLSN = 0

// CheckpointTxn is one transaction-table entry inside a checkpoint record.
type CheckpointTxn struct {
	TxnID   uint64
	LastLSN uint64
	Status  TxnStatus
}

// CheckpointPage is one dirty-page-table entry inside a checkpoint record.
type CheckpointPage struct {
	PageID uint64
	RecLSN uint64
}

// Record is the decoded in-memory form of a log record.
//
// Before/After are sub-slices of the underlying log buffer when produced by
// the mmap-backed Reader (zero-copy). They stay valid until the Reader is
// closed; call Clone to take ownership.
type Record struct {
	LSN      uint64
	Type     RecType
	TxnID    uint64
	PrevLSN  uint64
	PageID   uint64 // UPDATE / CLR
	Offset   uint16 // byte offset inside the page (UPDATE / CLR)
	UndoNext uint64 // CLR only: next LSN to undo
	Before   []byte // UPDATE / CLR
	After    []byte // UPDATE / CLR

	// CHECKPOINT payload
	Txns  []CheckpointTxn
	Pages []CheckpointPage
}

// Clone returns a deep copy that owns all of its byte slices.
func (r *Record) Clone() *Record {
	out := *r
	if r.Before != nil {
		out.Before = append([]byte(nil), r.Before...)
	}
	if r.After != nil {
		out.After = append([]byte(nil), r.After...)
	}
	if r.Txns != nil {
		out.Txns = append([]CheckpointTxn(nil), r.Txns...)
	}
	if r.Pages != nil {
		out.Pages = append([]CheckpointPage(nil), r.Pages...)
	}
	return &out
}

var (
	ErrShortHeader  = errors.New("wal: record header truncated")
	ErrShortPayload = errors.New("wal: record payload truncated")
	ErrBadMagic     = errors.New("wal: bad record magic")
	ErrBadVersion   = errors.New("wal: unsupported format version")
	ErrBadCRC       = errors.New("wal: record checksum mismatch")
	ErrCorrupt      = errors.New("wal: record payload corrupt")
	ErrBadType      = errors.New("wal: unknown record type")
	ErrImageTooBig  = errors.New("wal: image exceeds 65535 bytes")
)
