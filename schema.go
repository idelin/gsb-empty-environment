// Package endianconv converts fixed-layout records between big-endian and
// little-endian byte order according to a declared schema.
//
// Design guarantees:
//   - Floating-point fields are byte-reversed, never passed through a float
//     value, so NaN payloads and signaling NaNs survive a round trip.
//   - Variable-length fields have only their length header converted; the
//     payload bytes are copied verbatim.
//   - Length quotas are checked before any allocation or copy happens.
//   - Parsing distinguishes "not enough data yet" (ErrNeedMoreData) from
//     "declared lengths are impossible" (ErrRecordTooLarge).
//   - In-place conversion (dst and src are the exact same region) is
//     supported; partially overlapping regions are rejected up front with
//     ErrOverlap.
package endianconv

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Sentinel errors. Use errors.Is to test for them.
var (
	// ErrNeedMoreData means the input is a valid prefix of a record but
	// more bytes are required. This is a "keep feeding" condition, not
	// corruption.
	ErrNeedMoreData = errors.New("endianconv: need more data")

	// ErrRecordTooLarge means a declared length header exceeds the
	// configured quota. This is reported as soon as the offending header
	// is read, before allocating or waiting for the (bogus) payload.
	ErrRecordTooLarge = errors.New("endianconv: record exceeds length quota")

	// ErrOverlap means dst and src partially overlap. Exact same region
	// (in-place) and fully disjoint buffers are fine; anything in between
	// is rejected before any byte is written.
	ErrOverlap = errors.New("endianconv: dst and src partially overlap")

	// ErrShortBuffer means dst is smaller than the converted record.
	ErrShortBuffer = errors.New("endianconv: dst buffer too small")
)

// Kind describes how a field is treated during conversion.
type Kind int

const (
	KindU8       Kind = iota // 1 byte, copied
	KindU16                  // 2 bytes, byte-reversed
	KindU32                  // 4 bytes, byte-reversed
	KindU64                  // 8 bytes, byte-reversed
	KindF32                  // 4 bytes, byte-reversed (NaN bit patterns preserved)
	KindF64                  // 8 bytes, byte-reversed (NaN bit patterns preserved)
	KindBytes                // Size fixed bytes, copied verbatim
	KindVarBytes             // Size-byte length header (converted) + payload (verbatim)
)

// Field describes one record field.
type Field struct {
	Kind Kind
	// Size is the byte count for KindBytes, and the length-header width
	// (1, 2, 4 or 8) for KindVarBytes. Ignored otherwise.
	Size int
	// Max is the maximum accepted payload length for KindVarBytes.
	// A header declaring more than Max is rejected with ErrRecordTooLarge.
	Max int
}

// Convenience constructors for fixed-width fields.
func U8() Field  { return Field{Kind: KindU8} }
func U16() Field { return Field{Kind: KindU16} }
func U32() Field { return Field{Kind: KindU32} }
func U64() Field { return Field{Kind: KindU64} }
func F32() Field { return Field{Kind: KindF32} }
func F64() Field { return Field{Kind: KindF64} }

// Bytes declares a fixed-size opaque field that is copied verbatim.
func Bytes(n int) Field { return Field{Kind: KindBytes, Size: n} }

// VarBytes declares a variable-length field: a length header of
// headerSize bytes (1, 2, 4 or 8) followed by the payload. Only the
// header is byte-swapped during conversion. maxPayload is the quota
// checked before any allocation.
func VarBytes(headerSize, maxPayload int) Field {
	return Field{Kind: KindVarBytes, Size: headerSize, Max: maxPayload}
}

// Schema is the declared layout of a record.
type Schema struct {
	Fields []Field
}

// fixedSize returns the byte width of fixed-width kinds, or -1.
func (f Field) fixedSize() int {
	switch f.Kind {
	case KindU8:
		return 1
	case KindU16:
		return 2
	case KindU32, KindF32:
		return 4
	case KindU64, KindF64:
		return 8
	case KindBytes:
		return f.Size
	}
	return -1
}

// validate checks the schema is well formed.
func (s Schema) validate() error {
	for i, f := range s.Fields {
		switch f.Kind {
		case KindBytes:
			if f.Size < 0 {
				return fmt.Errorf("endianconv: field %d: negative Bytes size", i)
			}
		case KindVarBytes:
			switch f.Size {
			case 1, 2, 4, 8:
			default:
				return fmt.Errorf("endianconv: field %d: VarBytes header size must be 1, 2, 4 or 8, got %d", i, f.Size)
			}
			if f.Max < 0 {
				return fmt.Errorf("endianconv: field %d: negative VarBytes max", i)
			}
		case KindU8, KindU16, KindU32, KindU64, KindF32, KindF64:
		default:
			return fmt.Errorf("endianconv: field %d: unknown kind %d", i, f.Kind)
		}
	}
	return nil
}

// readUint reads a big- or little-endian unsigned integer of width 1, 2, 4 or 8.
func readUint(b []byte, width int, ord binary.ByteOrder) uint64 {
	switch width {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(ord.Uint16(b))
	case 4:
		return uint64(ord.Uint32(b))
	default:
		return ord.Uint64(b)
	}
}

// putUint writes an unsigned integer of width 1, 2, 4 or 8.
func putUint(b []byte, width int, ord binary.ByteOrder, v uint64) {
	switch width {
	case 1:
		b[0] = byte(v)
	case 2:
		ord.PutUint16(b, uint16(v))
	case 4:
		ord.PutUint32(b, uint32(v))
	default:
		ord.PutUint64(b, v)
	}
}

// recordLen determines the total length of one record at the start of src,
// interpreting length headers with ord. maxRecord is the quota for the
// whole record; values <= 0 mean "no limit".
//
// It returns ErrNeedMoreData if src is a valid prefix but incomplete, and
// ErrRecordTooLarge the moment a length header violates the quota — before
// any allocation based on that length could happen.
func (s Schema) recordLen(src []byte, ord binary.ByteOrder, maxRecord int) (int, error) {
	off := 0
	for _, f := range s.Fields {
		if f.Kind == KindVarBytes {
			if len(src)-off < f.Size {
				return 0, ErrNeedMoreData
			}
			n := readUint(src[off:off+f.Size], f.Size, ord)
			if n > uint64(f.Max) {
				return 0, fmt.Errorf("%w: var-length header declares %d bytes, max %d", ErrRecordTooLarge, n, f.Max)
			}
			off += f.Size
			if maxRecord > 0 && off+int(n) > maxRecord {
				return 0, fmt.Errorf("%w: record would be %d bytes, quota %d", ErrRecordTooLarge, off+int(n), maxRecord)
			}
			if len(src)-off < int(n) {
				return 0, ErrNeedMoreData
			}
			off += int(n)
			continue
		}
		sz := f.fixedSize()
		if len(src)-off < sz {
			return 0, ErrNeedMoreData
		}
		off += sz
	}
	if maxRecord > 0 && off > maxRecord {
		return 0, fmt.Errorf("%w: record is %d bytes, quota %d", ErrRecordTooLarge, off, maxRecord)
	}
	return off, nil
}
