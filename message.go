package pgoutput

import (
	"fmt"
	"time"
)

// LSN is a PostgreSQL log sequence number (byte offset in the WAL).
type LSN uint64

// String renders an LSN in PostgreSQL's X/Y hexadecimal notation.
func (l LSN) String() string {
	return fmt.Sprintf("%X/%08X", uint64(l)>>32, uint64(l)&0xffffffff)
}

// pgEpoch is 2000-01-01 00:00:00 UTC, the epoch used by replication timestamps.
var pgEpoch = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

func microsToTime(micros int64) time.Time {
	return pgEpoch.Add(time.Duration(micros) * time.Microsecond)
}

// Op identifies the kind of row change carried by a Change.
type Op uint8

const (
	OpInsert Op = iota + 1
	OpUpdate
	OpDelete
	OpTruncate
)

func (o Op) String() string {
	switch o {
	case OpInsert:
		return "I"
	case OpUpdate:
		return "U"
	case OpDelete:
		return "D"
	case OpTruncate:
		return "T"
	default:
		return "?"
	}
}

// Column flags as emitted in the Relation message.
const (
	colFlagKey    = 1 // column is part of the replica identity / primary key
	colFlagBinary = 2 // column's data type has binary send/recv functions
)

// Tuple value tags used inside Insert/Update/Delete tuples.
const (
	tupleNull      = 'n' // value is NULL
	tupleUnchanged = 'u' // unchanged TOAST column, value not sent
	tupleText      = 't' // value in text representation
	tupleBinary    = 'b' // value in binary representation
)

// TupleColumn is one decoded column value of a row tuple.
type TupleColumn struct {
	// Flag is the wire tag: 'n' NULL, 't' text, 'b' binary, 'u' unchanged TOAST.
	Flag byte
	// Value is nil for NULL and unchanged-TOAST columns, otherwise the raw payload.
	Value []byte
	// Unchanged reports the column was not included in the change because its
	// TOASTed value did not change (and may be unchanged on disk as well).
	Unchanged bool
}

func (c TupleColumn) String() string {
	switch {
	case c.Unchanged:
		return "unchanged-toast"
	case c.Value == nil:
		return "NULL"
	default:
		return string(c.Value)
	}
}

// Relation describes the schema of a replicated table (message type 'R').
type Relation struct {
	ID        uint32
	Namespace string
	Name      string
	// ReplicaIdentity is the REPLICA IDENTITY setting: 'd' default, 'n' nothing,
	// 'f' full, 'i' index.
	ReplicaIdentity byte
	Columns         []RelationColumn
}

// RelationColumn describes one column of a Relation.
type RelationColumn struct {
	// Key marks columns that are part of the replica identity.
	Key bool
	// Binary marks columns whose data type supports binary representation.
	Binary       bool
	Name         string
	DataType     uint32
	TypeModifier int32
}

// Type describes a replicated data type (message type 'Y').
type Type struct {
	ID        uint32
	Namespace string
	Name      string
}

// Insert is message type 'I'.
type Insert struct {
	RelationID uint32
	Row        []TupleColumn
}

// Update is message type 'U'.
type Update struct {
	RelationID uint32
	// Old is the old tuple. It is nil when replica identity does not send one.
	// Entries for unchanged TOAST columns keep Unchanged set and are merged
	// against New by the assembler.
	Old []TupleColumn
	// OldKey is true when Old carries only replica identity key columns.
	OldKey bool
	New    []TupleColumn
}

// Delete is message type 'D'.
type Delete struct {
	RelationID uint32
	Old        []TupleColumn
	// OldKey is true when Old carries only replica identity key columns.
	OldKey bool
}

// Truncate is message type 'T'.
type Truncate struct {
	RelationIDs []uint32
	// Options bitmask: 1=CASCADE, 2=RESTART IDENTITY.
	Options uint8
}

// Begin is message type 'B' (non-streamed transaction).
type Begin struct {
	FinalLSN   LSN
	CommitTime time.Time
	XID        uint32
}

// Commit is message type 'C' (non-streamed transaction).
type Commit struct {
	Flags      uint8
	CommitLSN  LSN
	EndLSN     LSN
	CommitTime time.Time
}

// Origin is message type 'O'.
type Origin struct {
	LSN  LSN
	Name string
}

// LogicalMessage is message type 'M' (transactional) or 'm' (non-transactional).
type LogicalMessage struct {
	Transactional bool
	LSN           LSN
	Prefix        string
	Content       []byte
}

// Keepalive is the primary-keepalive submessage ('k') carried in CopyData.
type Keepalive struct {
	LSN            LSN
	Time           time.Time
	ReplyRequested bool
}

// StreamStart is message type 'S'.
type StreamStart struct {
	XID uint32
	// FirstSegment marks the first streamed segment of the transaction.
	FirstSegment bool
	// SubXID is present only on protocol v4 subtransaction streams; zero means
	// the segment belongs to the top-level transaction.
	SubXID uint32
}

// StreamStop is message type 'E'.
type StreamStop struct{}

// StreamCommit is message type 'c'.
type StreamCommit struct {
	XID        uint32
	Flags      uint8
	CommitLSN  LSN
	EndLSN     LSN
	CommitTime time.Time
}

// StreamAbort is message type 'A'.
type StreamAbort struct {
	XID    uint32
	SubXID uint32
	// SubAbort reports a subtransaction abort. On protocol v2 the server only
	// ever aborts the whole top-level transaction (XID == SubXID); a
	// subtransaction abort is sent followed by a full re-stream.
	SubAbort   bool
	LSN        LSN
	EndLSN     LSN
	CommitTime time.Time
}
