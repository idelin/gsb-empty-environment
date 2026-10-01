package coordinator

import "fmt"

// Event is any item flowing through the CDC stream.
type Event interface{ event() }

// DMLEvent is a raw data-change record whose fields are not yet aligned
// to any schema; alignment happens at parse time against the schema
// snapshot captured by the coordinator.
type DMLEvent struct {
	Table string
	Raw   []string
}

// DDLKind enumerates supported schema-change operations.
type DDLKind int

const (
	AddColumn DDLKind = iota
)

// DDLEvent is a schema-change record, e.g. AlterTableAddColumn.
type DDLEvent struct {
	Table  string
	Kind   DDLKind
	Column Column
}

func (DMLEvent) event() {}
func (DDLEvent) event() {}

// Output is anything the coordinator emits downstream.
type Output interface{ output() }

// ParsedRow is a DML record aligned to a specific schema version.
type ParsedRow struct {
	Table         string
	SchemaVersion uint64
	Fields        map[string]string
}

// RowOutput carries a parsed DML row.
type RowOutput struct{ Row ParsedRow }

// BarrierOutput marks the global barrier inserted at the DDL point:
// every row emitted before it used FromVersion, every row after uses
// ToVersion.
type BarrierOutput struct {
	Table       string
	FromVersion uint64
	ToVersion   uint64
}

// ErrorOutput carries a non-fatal stream error (unknown table, field
// count mismatch, unsupported DDL).
type ErrorOutput struct {
	Table string
	Err   error
}

func (RowOutput) output()     {}
func (BarrierOutput) output() {}
func (ErrorOutput) output()   {}

// UnknownTableError is returned when an event references a table with no
// registered schema.
type UnknownTableError struct{ Table string }

func (e *UnknownTableError) Error() string {
	return fmt.Sprintf("cdc: unknown table %q", e.Table)
}

// UnsupportedDDLError is returned for DDL kinds the registry cannot apply.
type UnsupportedDDLError struct{ Table string }

func (e *UnsupportedDDLError) Error() string {
	return fmt.Sprintf("cdc: unsupported DDL for table %q", e.Table)
}

// FieldCountError reports a DML record whose raw field count does not
// match the schema snapshot it was captured against.
type FieldCountError struct {
	Table         string
	SchemaVersion uint64
	Got, Want     int
}

func (e *FieldCountError) Error() string {
	return fmt.Sprintf("cdc: table %q schema v%d expects %d fields, got %d",
		e.Table, e.SchemaVersion, e.Want, e.Got)
}
