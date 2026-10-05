// Package varstream encodes and decodes streams of variable-length
// integers (signed and unsigned) whose field bit widths are fixed by a
// schema. Encodings are canonical: overflow and non-minimal forms are
// rejected, so every value maps to exactly one byte string.
package varstream

import (
	"errors"
	"fmt"
)

var (
	// ErrNeedMore is returned when the input ends in the middle of a
	// varint or record. A truncated integer is never reported as zero.
	ErrNeedMore = errors.New("varstream: need more input")
	// ErrOverflow is returned when a value does not fit the bit width
	// its schema field allows.
	ErrOverflow = errors.New("varstream: value overflows field bit width")
	// ErrNonMinimal is returned when a varint is not the shortest
	// encoding of its value (redundant trailing zero group).
	ErrNonMinimal = errors.New("varstream: non-minimal varint encoding")
	// ErrVarintOverflow is returned when a varint exceeds 64 bits.
	ErrVarintOverflow = errors.New("varstream: varint exceeds 64 bits")
	// ErrSchemaVersion is returned when resuming a paused decoder with
	// a schema whose version differs from the paused state.
	ErrSchemaVersion = errors.New("varstream: pause state schema version mismatch")
)

// Field describes one integer field of a record.
type Field struct {
	Name   string
	Bits   uint // 1..64
	Signed bool
}

// Schema fixes the layout of every record in a stream. Version is
// recorded in pause states so a stream cannot be resumed against a
// different schema revision.
type Schema struct {
	Version uint32
	Fields  []Field
}

// Validate reports whether the schema is usable.
func (s *Schema) Validate() error {
	if len(s.Fields) == 0 {
		return errors.New("varstream: schema has no fields")
	}
	for _, f := range s.Fields {
		if f.Bits == 0 || f.Bits > 64 {
			return fmt.Errorf("varstream: field %q: bit width %d out of range [1,64]", f.Name, f.Bits)
		}
	}
	return nil
}

// Value is one decoded or to-be-encoded field value. Use Uint for
// unsigned fields and Int for signed fields; the schema decides which
// member is read.
type Value struct {
	U uint64
	I int64
}

// Uint builds an unsigned field value.
func Uint(v uint64) Value { return Value{U: v} }

// Int builds a signed field value.
func Int(v int64) Value { return Value{I: v} }

// Record is one fully decoded record, one Value per schema field.
type Record []Value

func fitsUint(v uint64, bits uint) bool {
	return bits >= 64 || v < uint64(1)<<bits
}

func fitsInt(v int64, bits uint) bool {
	if bits >= 64 {
		return true
	}
	lim := int64(1) << (bits - 1)
	return v >= -lim && v <= lim-1
}

func zigzag(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

func unzigzag(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }
