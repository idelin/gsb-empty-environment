package aries

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// LogType identifies the kind of a WAL record.
type LogType uint8

const (
	LogUpdate LogType = iota + 1
	LogCommit
	LogAbort
	LogEnd
	LogCLR
	LogBeginCheckpoint
	LogEndCheckpoint
)

func (t LogType) String() string {
	switch t {
	case LogUpdate:
		return "UPDATE"
	case LogCommit:
		return "COMMIT"
	case LogAbort:
		return "ABORT"
	case LogEnd:
		return "END"
	case LogCLR:
		return "CLR"
	case LogBeginCheckpoint:
		return "BEGIN_CHKPT"
	case LogEndCheckpoint:
		return "END_CHKPT"
	}
	return "UNKNOWN"
}

// TxnStatus is the lifecycle state of a transaction tracked in the
// transaction table.
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

// TxnTableEntry is one row of the ARIES transaction table.
type TxnTableEntry struct {
	LastLSN uint64
	Status  TxnStatus
}

// LogRecord is a single write-ahead log record. Only the fields relevant
// to the record type are populated.
type LogRecord struct {
	LSN     uint64
	PrevLSN uint64 // previous LSN of the same transaction (0 = none)
	TxnID   uint64
	Type    LogType

	// UPDATE / CLR payload.
	PageID  uint32
	Offset  uint32
	OldData []byte // CLR: the (redo) image being undone... see NewData
	NewData []byte

	// CLR only: LSN of the next record to undo (skips already-undone ops).
	UndoNextLSN uint64

	// END_CHKPT payload: consistent snapshot of the recovery tables.
	TxnTable     map[uint64]TxnTableEntry
	DirtyPageTab map[uint32]uint64 // pageID -> recLSN
}

const fixedHeaderSize = 8 + // LSN
	8 + // PrevLSN
	8 + // TxnID
	1 + // Type
	4 + // PageID
	4 + // Offset
	8 + // UndoNextLSN
	4 + // len(OldData)
	4 // len(NewData)

// Marshal encodes the record into its binary on-disk form.
func (r *LogRecord) Marshal() []byte {
	buf := bytes.NewBuffer(make([]byte, 0, fixedHeaderSize+len(r.OldData)+len(r.NewData)))
	writeUint(buf, r.LSN)
	writeUint(buf, r.PrevLSN)
	writeUint(buf, r.TxnID)
	buf.WriteByte(byte(r.Type))
	writeUint32(buf, r.PageID)
	writeUint32(buf, r.Offset)
	writeUint(buf, r.UndoNextLSN)
	writeUint32(buf, uint32(len(r.OldData)))
	writeUint32(buf, uint32(len(r.NewData)))
	buf.Write(r.OldData)
	buf.Write(r.NewData)
	if r.Type == LogEndCheckpoint {
		writeUint32(buf, uint32(len(r.TxnTable)))
		for txnID, e := range r.TxnTable {
			writeUint(buf, txnID)
			writeUint(buf, e.LastLSN)
			buf.WriteByte(byte(e.Status))
		}
		writeUint32(buf, uint32(len(r.DirtyPageTab)))
		for pageID, recLSN := range r.DirtyPageTab {
			writeUint32(buf, pageID)
			writeUint(buf, recLSN)
		}
	}
	return buf.Bytes()
}

// Unmarshal decodes a record previously produced by Marshal.
func (r *LogRecord) Unmarshal(data []byte) error {
	if len(data) < fixedHeaderSize {
		return fmt.Errorf("log record too short: %d bytes", len(data))
	}
	rd := bytes.NewReader(data)
	r.LSN = readUint(rd)
	r.PrevLSN = readUint(rd)
	r.TxnID = readUint(rd)
	typ, _ := rd.ReadByte()
	r.Type = LogType(typ)
	r.PageID = readUint32(rd)
	r.Offset = readUint32(rd)
	r.UndoNextLSN = readUint(rd)
	oldLen := readUint32(rd)
	newLen := readUint32(rd)
	if uint64(oldLen)+uint64(newLen) > uint64(rd.Len()) {
		return fmt.Errorf("corrupt log record at LSN %d: payload lengths exceed frame", r.LSN)
	}
	if oldLen > 0 {
		r.OldData = make([]byte, oldLen)
		io.ReadFull(rd, r.OldData)
	}
	if newLen > 0 {
		r.NewData = make([]byte, newLen)
		io.ReadFull(rd, r.NewData)
	}
	if r.Type == LogEndCheckpoint {
		r.TxnTable = make(map[uint64]TxnTableEntry)
		r.DirtyPageTab = make(map[uint32]uint64)
		txnCount := readUint32(rd)
		for i := uint32(0); i < txnCount; i++ {
			txnID := readUint(rd)
			lastLSN := readUint(rd)
			status, _ := rd.ReadByte()
			r.TxnTable[txnID] = TxnTableEntry{LastLSN: lastLSN, Status: TxnStatus(status)}
		}
		dptCount := readUint32(rd)
		for i := uint32(0); i < dptCount; i++ {
			pageID := readUint32(rd)
			recLSN := readUint(rd)
			r.DirtyPageTab[pageID] = recLSN
		}
	}
	return nil
}

func (r *LogRecord) String() string {
	base := fmt.Sprintf("LSN=%d type=%s txn=%d prev=%d", r.LSN, r.Type, r.TxnID, r.PrevLSN)
	switch r.Type {
	case LogUpdate, LogCLR:
		return fmt.Sprintf("%s page=%d off=%d old=%q new=%q undoNext=%d",
			base, r.PageID, r.Offset, r.OldData, r.NewData, r.UndoNextLSN)
	case LogEndCheckpoint:
		return fmt.Sprintf("%s txns=%d dpt=%d", base, len(r.TxnTable), len(r.DirtyPageTab))
	}
	return base
}

func writeUint(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func readUint(rd *bytes.Reader) uint64 {
	var b [8]byte
	io.ReadFull(rd, b[:])
	return binary.BigEndian.Uint64(b[:])
}

func readUint32(rd *bytes.Reader) uint32 {
	var b [4]byte
	io.ReadFull(rd, b[:])
	return binary.BigEndian.Uint32(b[:])
}
