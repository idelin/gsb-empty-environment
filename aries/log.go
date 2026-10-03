package aries

// LSN is a monotonically increasing log sequence number.
type LSN int64

// TxID identifies a transaction.
type TxID int64

// PageID identifies a database page.
type PageID int32

// RecordType enumerates ARIES log record kinds.
type RecordType uint8

const (
	TypeUpdate RecordType = iota + 1
	TypeCommit
	TypeAbort
	TypeEnd
	TypeCLR
	TypeBeginCheckpoint
	TypeEndCheckpoint
)

func (t RecordType) String() string {
	switch t {
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
	case TypeBeginCheckpoint:
		return "BEGIN_CKPT"
	case TypeEndCheckpoint:
		return "END_CKPT"
	}
	return "UNKNOWN"
}

// TxStatus is the transaction state tracked in the transaction table.
type TxStatus uint8

const (
	StatusRunning TxStatus = iota
	StatusCommitted
	StatusAborted
)

func (s TxStatus) String() string {
	switch s {
	case StatusRunning:
		return "RUNNING"
	case StatusCommitted:
		return "COMMITTED"
	case StatusAborted:
		return "ABORTED"
	}
	return "UNKNOWN"
}

// TTEntry is a transaction-table entry.
type TTEntry struct {
	Status  TxStatus
	LastLSN LSN
}

// LogRecord is a single ARIES WAL record.
type LogRecord struct {
	LSN      LSN
	TxID     TxID
	Type     RecordType
	PrevLSN  LSN
	PageID   PageID
	UndoNext LSN
	Before   []byte
	After    []byte

	// End-checkpoint payload.
	BeginLSN LSN
	TT       map[TxID]TTEntry
	DPT      map[PageID]LSN
}
