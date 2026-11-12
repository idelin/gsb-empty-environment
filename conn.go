package netsec

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
)

// ParamType is the wire type of a bind parameter. The parameter signature
// is part of the statement handle identity: the same SQL text with a
// different signature is a different handle.
type ParamType int

const (
	TypeNull ParamType = iota
	TypeInt64
	TypeFloat64
	TypeBool
	TypeString
	TypeBytes
)

func (t ParamType) String() string {
	switch t {
	case TypeNull:
		return "null"
	case TypeInt64:
		return "int64"
	case TypeFloat64:
		return "float64"
	case TypeBool:
		return "bool"
	case TypeString:
		return "string"
	case TypeBytes:
		return "bytes"
	}
	return "unknown"
}

// Value is one structured bind parameter. Values always travel out-of-band
// next to the server-side statement ID; they are never interpolated into
// SQL text anywhere in this package.
type Value struct {
	Type ParamType
	Data any
}

func Null() Value             { return Value{Type: TypeNull} }
func Int64(v int64) Value     { return Value{Type: TypeInt64, Data: v} }
func Float64(v float64) Value { return Value{Type: TypeFloat64, Data: v} }
func Bool(v bool) Value       { return Value{Type: TypeBool, Data: v} }
func Str(v string) Value      { return Value{Type: TypeString, Data: v} }
func Bytes(v []byte) Value    { return Value{Type: TypeBytes, Data: v} }

var (
	// ErrParamCount is returned when the argument list does not match the
	// template's parameter signature. Execution is refused before any
	// network I/O; there is no fallback path.
	ErrParamCount = errors.New("netsec: parameter count mismatch")
	// ErrParamType is returned when an argument's type does not match the
	// template signature.
	ErrParamType = errors.New("netsec: parameter type mismatch")
	// ErrClosed is returned when a handle was closed concurrently with the
	// call. Executor.Execute retries internally on a fresh handle.
	ErrClosed = errors.New("netsec: statement handle closed")
	// ErrDatabaseMismatch is returned when the pool hands out a connection
	// whose current database identity differs from the requested one. The
	// statement is never executed under the wrong tenant identity.
	ErrDatabaseMismatch = errors.New("netsec: connection database identity mismatch")
)

// Template is a canonicalized SQL statement plus its parameter signature.
// The digest, not the raw text, is used as part of the handle key.
type Template struct {
	SQL        string
	Digest     [32]byte
	ParamTypes []ParamType
}

func NewTemplate(sql string, paramTypes ...ParamType) Template {
	return Template{
		SQL:        sql,
		Digest:     sha256.Sum256([]byte(sql)),
		ParamTypes: paramTypes,
	}
}

// ValidateArgs checks count and types before any connection is touched.
// A mismatch is a hard error; the gateway must not "repair" the call by
// falling back to text SQL.
func (t Template) ValidateArgs(args []Value) error {
	if len(args) != len(t.ParamTypes) {
		return fmt.Errorf("%w: template wants %d, got %d", ErrParamCount, len(t.ParamTypes), len(args))
	}
	for i, a := range args {
		if a.Type != t.ParamTypes[i] && a.Type != TypeNull {
			return fmt.Errorf("%w: param %d wants %s, got %s", ErrParamType, i, t.ParamTypes[i], a.Type)
		}
	}
	return nil
}

// Result is a minimal execution result.
type Result struct {
	RowsAffected int64
	Rows         [][]Value
}

// Conn is one physical connection to the database server. Statement IDs
// returned by Prepare are scoped to this connection only and may collide
// with IDs on any other connection.
//
// Note there is deliberately no text-query method on this interface:
// structured binding is the only way to execute, so falling back to string
// concatenation after a failure is impossible at this layer.
type Conn interface {
	Prepare(ctx context.Context, sql string, paramTypes []ParamType) (serverID uint64, err error)
	Execute(ctx context.Context, serverID uint64, args []Value) (Result, error)
	CloseStmt(ctx context.Context, serverID uint64) error
}

// PConn is a leased physical connection plus the identity markers a
// prepared statement is bound to. A server-side statement is valid only
// for the exact combination of these fields.
type PConn struct {
	Conn Conn
	// Generation bumps every time the pool rebuilds the physical
	// connection. A new generation invalidates every statement ID ever
	// issued on the old one, even if the IDs look identical.
	Generation uint64
	// Database is the current database (tenant) identity of the session.
	Database string
	// SchemaVersion bumps on DDL / mode changes; prepared statements may
	// refer to dropped or altered objects and must be re-prepared.
	SchemaVersion uint64
}

// Source hands out the current physical connection for a database. The
// returned connection is expected to already operate under the requested
// database identity.
type Source interface {
	Acquire(ctx context.Context, database string) (*PConn, error)
}
