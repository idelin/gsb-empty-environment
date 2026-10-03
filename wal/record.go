// Package wal implements the write-ahead log used by the ARIES
// recovery engine: record types, a checksummed on-disk format and a
// builder that simulates a running system producing log records.
package wal

// LSN is a monotonically increasing log sequence number.
type LSN uint64

// RecordType enumerates the ARIES log record kinds.
type RecordType uint8

const (
	TypeUpdate RecordType = iota + 1
	TypeCLR
	TypeCommit
	TypeAbort
	TypeEnd
	TypeCheckpoint
)

func (t RecordType) String() string {
	switch t {
	case TypeUpdate:
		return "UPDATE"
	case TypeCLR:
		return "CLR"
	case TypeCommit:
		return "COMMIT"
	case TypeAbort:
		return "ABORT"
	case TypeEnd:
		return "END"
	case TypeCheckpoint:
		return "CHECKPOINT"
	}
	return "UNKNOWN"
}

// TxnStatus is the lifecycle state of a transaction in the TxnTable.
type TxnStatus uint8

const (
	StatusRunning TxnStatus = iota
	StatusCommitted
	StatusAborting
)

func (s TxnStatus) String() string {
	switch s {
	case StatusRunning:
		return "RUNNING"
	case StatusCommitted:
		return "COMMITTED"
	case StatusAborting:
		return "ABORTING"
	}
	return "UNKNOWN"
}

// LogRecord is a single WAL entry. Data-carrying records (UPDATE, CLR)
// reference a page; CLR records carry UndoNext pointing at the next
// record to undo for the transaction. Checkpoint records snapshot the
// DirtyPageTable and the transaction table.
type LogRecord struct {
	LSN      LSN
	PrevLSN  LSN
	TxnID    uint64
	Type     RecordType
	PageID   uint64
	Before   []byte
	After    []byte
	UndoNext LSN
	// Checkpoint payload.
	DPT  map[uint64]LSN
	Txns map[uint64]LSN
}
