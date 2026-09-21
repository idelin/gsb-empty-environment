// Package endconv converts binary records between big-endian and
// little-endian layouts according to a declared schema.
//
// All multi-byte numeric fields (including IEEE-754 floats) are converted
// by reversing the raw bytes. Floats never pass through floating-point
// arithmetic, so NaN payload/signaling bits are preserved bit-for-bit.
//
// Variable-length fields consist of a 16-bit length header in the source
// endianness followed by an opaque payload. Only the header is byte-swapped;
// the payload is copied unchanged.
package endconv

import "errors"

// Kind enumerates the field kinds understood by a Schema.
type Kind int

const (
	U8      Kind = iota // unsigned 8-bit integer (no byte order)
	U16                 // unsigned 16-bit integer
	U32                 // unsigned 32-bit integer
	U64                 // unsigned 64-bit integer
	F32                 // IEEE-754 binary32, converted as raw bits
	F64                 // IEEE-754 binary64, converted as raw bits
	Blob                // fixed-length opaque bytes; N gives the length
	VarBlob             // 16-bit length header + opaque payload of that length
)

func (k Kind) String() string {
	switch k {
	case U8:
		return "U8"
	case U16:
		return "U16"
	case U32:
		return "U32"
	case U64:
		return "U64"
	case F32:
		return "F32"
	case F64:
		return "F64"
	case Blob:
		return "Blob"
	case VarBlob:
		return "VarBlob"
	default:
		return "Unknown"
	}
}

// Field is one schema element. N only applies to Blob and gives the fixed
// number of opaque bytes; it must be non-negative.
type Field struct {
	Kind Kind
	N    int
}

// Schema describes the ordered fields of one record.
type Schema []Field

// ErrBadSchema reports an invalid field declaration.
var ErrBadSchema = errors.New("endconv: invalid schema")

// Validate checks the schema for malformed field declarations.
func (s Schema) Validate() error {
	for _, f := range s {
		switch f.Kind {
		case U8, U16, U32, U64, F32, F64, VarBlob:
			// no extra constraints
		case Blob:
			if f.N < 0 {
				return ErrBadSchema
			}
		default:
			return ErrBadSchema
		}
	}
	return nil
}

// fixedSize returns the byte size of a field that never depends on data,
// or 0 for VarBlob whose size must be read from the record.
func fixedSize(f Field) int {
	switch f.Kind {
	case U8:
		return 1
	case U16:
		return 2
	case U32, F32:
		return 4
	case U64, F64:
		return 8
	case Blob:
		return f.N
	default: // VarBlob
		return 0
	}
}
