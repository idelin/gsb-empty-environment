package netsec

import (
	"fmt"
	"io"
	"strings"
)

// TableType identifies the layout kind of a table.
type TableType uint16

// TableID uniquely identifies a table. Identity is bound to the file
// version, the byte offset, and the table type: the same offset decoded
// as two different types yields two distinct tables, and identical bytes
// in two different file versions never alias.
type TableID struct {
	Version uint32
	Offset  uint64
	Type    TableType
}

func (id TableID) String() string {
	return fmt.Sprintf("v%d@%d:T%d", id.Version, id.Offset, id.Type)
}

// Descriptor is the host-decoded local layout of a single table.
// The host must not follow references itself; the Reader validates the
// declared extent before any reference is chased.
type Descriptor struct {
	Size uint64    // total byte size of the table starting at TableID.Offset
	Refs []TableID // outgoing references discovered in the local layout
}

// Parser decodes the local layout of one table from the source image.
// It must only read the table's own bytes; the Reader independently
// re-validates the declared extent (bounds + full read) before
// following any reference in the returned Descriptor.
type Parser func(id TableID, r io.ReaderAt) (Descriptor, error)

// Source is a versioned binary metadata image.
type Source interface {
	io.ReaderAt
	Size() int64
}

// Node is a resolved table in the expanded graph. Shared tables resolve
// to the same *Node, so the result is a DAG (or an error on cycles).
type Node struct {
	ID       TableID
	Desc     Descriptor
	Children []*Node // parallel to Desc.Refs
}

// VersionError reports a reference into a different file version than
// the one the Reader is bound to.
type VersionError struct {
	ID   TableID
	Want uint32
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("netsec: table %s belongs to file version %d, reader is bound to %d",
		e.ID, e.ID.Version, e.Want)
}

// BoundsError reports a table whose declared extent escapes the image.
type BoundsError struct {
	ID       TableID
	Size     uint64
	FileSize int64
}

func (e *BoundsError) Error() string {
	return fmt.Sprintf("netsec: table %s declares %d bytes, end %d exceeds image size %d",
		e.ID, e.Size, e.ID.Offset+e.Size, e.FileSize)
}

// ShortReadError reports a table whose declared extent passed the bounds
// check but could not be fully read from the source (truncated image).
type ShortReadError struct {
	ID   TableID
	Want uint64
	Got  uint64
	Err  error
}

func (e *ShortReadError) Error() string {
	return fmt.Sprintf("netsec: table %s short read: got %d of %d bytes: %v",
		e.ID, e.Got, e.Want, e.Err)
}

func (e *ShortReadError) Unwrap() error { return e.Err }

// RefNotAllowedError reports a reference edge rejected by the caller's
// recursion whitelist.
type RefNotAllowedError struct {
	From TableID
	To   TableID
}

func (e *RefNotAllowedError) Error() string {
	return fmt.Sprintf("netsec: reference T%d -> T%d not in whitelist (%s -> %s)",
		e.From.Type, e.To.Type, e.From, e.To)
}

// BudgetKind selects which budget a BudgetError reports.
type BudgetKind int

const (
	BudgetTables BudgetKind = iota
	BudgetBytes
)

// BudgetError reports exhaustion of a caller-declared budget.
type BudgetError struct {
	Kind  BudgetKind
	Limit int64
	Used  int64
}

func (e *BudgetError) Error() string {
	what := "tables"
	if e.Kind == BudgetBytes {
		what = "bytes"
	}
	return fmt.Sprintf("netsec: %s budget exhausted: %d of %d used", what, e.Used, e.Limit)
}

// CycleError reports a reference loop found on the current expansion
// stack. Path is the shortest reference evidence for the cycle: the
// stack segment from the first occurrence of the repeated table back to
// it, e.g. [B, C, B] for B -> C -> B.
type CycleError struct {
	Path []TableID
}

func (e *CycleError) Error() string {
	parts := make([]string, len(e.Path))
	for i, id := range e.Path {
		parts[i] = id.String()
	}
	return fmt.Sprintf("netsec: reference cycle: %s", strings.Join(parts, " -> "))
}
