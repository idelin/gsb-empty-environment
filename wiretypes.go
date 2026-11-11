// Package wiretypes decodes network messages carrying reference-table object
// graphs.
//
// A message is decoded under a negotiated Mode that whitelists the stable type
// IDs which may be instantiated, and under a Budget that caps object, field,
// reference and nesting resources. Every reference and every budget is
// validated before any factory runs; the fully linked graph (forward
// references, aliases and cycles included) is then offered to a business
// callback through a check-then-receive protocol so that a rejected message
// has no externally observable side effect.
//
// The type registry supports atomic hot swap. Each Decode call binds to an
// immutable registry snapshot taken when the message starts, so swapping the
// registry can never affect an in-flight message.
package wiretypes

// TypeID is the stable, negotiated identifier of a registered type. It must
// remain stable across wire protocol versions.
type TypeID string

// RefID identifies an entry in a message's reference table.
type RefID string

// ValueKind enumerates the wire-level value kinds. Field specs declare one of
// these kinds and raw values must match the declaration exactly.
type ValueKind uint8

const (
	// KindNull is only legal for fields whose spec marks Nullable.
	KindNull ValueKind = iota
	KindBool
	KindInt
	KindString
	KindRef
	KindList
)

// RawValue is a single field value on the wire. The Kind field selects which
// of the typed payload fields is meaningful.
type RawValue struct {
	Kind ValueKind
	Bool bool
	Int  int64
	Str  string
	Ref  RefID
	List []RawValue
}

// VBool builds a boolean wire value.
func VBool(b bool) RawValue { return RawValue{Kind: KindBool, Bool: b} }

// VInt builds an int64 wire value.
func VInt(i int64) RawValue { return RawValue{Kind: KindInt, Int: i} }

// VStr builds a string wire value.
func VStr(s string) RawValue { return RawValue{Kind: KindString, Str: s} }

// VRef builds a reference wire value pointing at another table entry.
func VRef(id RefID) RawValue { return RawValue{Kind: KindRef, Ref: id} }

// VList builds a list wire value.
func VList(elems ...RawValue) RawValue { return RawValue{Kind: KindList, List: elems} }

// VNull builds a null wire value, legal only for nullable fields.
func VNull() RawValue { return RawValue{Kind: KindNull} }

// RawObject is one entry of the reference table.
type RawObject struct {
	ID     RefID
	Type   TypeID
	Fields map[string]RawValue
}

// Message is the wire input: a reference table plus the root entry ID.
type Message struct {
	Root    RefID
	Objects []RawObject
}
