package endconv

import (
	"encoding/binary"
	"errors"
	"unsafe"
)

// Direction selects which endianness the source record uses.
// Byte reversal is an involution, so the same swap implements both
// directions; the direction only matters when interpreting VarBlob
// length headers while walking the record.
type Direction int

const (
	BigToLittle Direction = iota // source is big-endian, target little-endian
	LittleToBig                  // source is little-endian, target big-endian
)

var (
	// ErrShortRecord means src ends before the schema is satisfied.
	ErrShortRecord = errors.New("endconv: short record")
	// ErrShortBuffer means dst cannot hold the converted record.
	ErrShortBuffer = errors.New("endconv: destination buffer too short")
	// ErrOverlap means src and dst overlap without being the exact same
	// backing range. In-place conversion (same slice) is supported;
	// partial overlap is rejected up front rather than producing a
	// partially corrupted record.
	ErrOverlap = errors.New("endconv: src and dst partially overlap")
)

func sourceOrder(dir Direction) binary.ByteOrder {
	if dir == LittleToBig {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func targetOrder(dir Direction) binary.ByteOrder {
	if dir == LittleToBig {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

// RecordLength walks src according to the schema and returns the total
// number of bytes one record occupies. It returns ErrShortRecord when src
// does not yet contain a full record.
func RecordLength(src []byte, s Schema, dir Direction) (int, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	off := 0
	bo := sourceOrder(dir)
	for _, f := range s {
		size := fixedSize(f)
		if f.Kind == VarBlob {
			if len(src)-off < 2 {
				return 0, ErrShortRecord
			}
			n := int(bo.Uint16(src[off : off+2]))
			size = 2 + n
		}
		if len(src)-off < size {
			return 0, ErrShortRecord
		}
		off += size
	}
	return off, nil
}

// rangesOverlap reports whether the byte ranges a and b intersect.
func rangesOverlap(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	pa := uintptr(unsafe.Pointer(&a[0]))
	pb := uintptr(unsafe.Pointer(&b[0]))
	return pa < pb+uintptr(len(b)) && pb < pa+uintptr(len(a))
}

func sameBase(a, b []byte) bool {
	return len(a) > 0 && len(b) > 0 &&
		uintptr(unsafe.Pointer(&a[0])) == uintptr(unsafe.Pointer(&b[0]))
}

// Convert converts one full record from src into dst. It returns the
// number of bytes written. dst and src may be the exact same slice for
// in-place conversion; they may also be fully disjoint. Partial overlap
// is rejected with ErrOverlap before any byte is written.
//
// src must contain at least one complete record (see RecordLength) and
// dst must be at least that many bytes long.
func Convert(dst, src []byte, s Schema, dir Direction) (int, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	n, err := RecordLength(src, s, dir)
	if err != nil {
		return 0, err
	}
	if len(dst) < n {
		return 0, ErrShortBuffer
	}
	dstUse := dst[:n]
	srcUse := src[:n]
	if rangesOverlap(dstUse, srcUse) && !sameBase(dstUse, srcUse) {
		return 0, ErrOverlap
	}
	convertWalk(dstUse, srcUse, s, dir)
	return n, nil
}

// ConvertInPlace converts a record within a single buffer.
func ConvertInPlace(buf []byte, s Schema, dir Direction) error {
	_, err := Convert(buf, buf, s, dir)
	return err
}

// convertWalk performs the field-by-field conversion. For in-place calls
// dst and src share memory; every field transform must be safe on the
// shared backing array.
func convertWalk(dst, src []byte, s Schema, dir Direction) {
	srcBO := sourceOrder(dir)
	dstBO := targetOrder(dir)
	inPlace := sameBase(dst, src)
	off := 0
	for _, f := range s {
		switch f.Kind {
		case U8:
			dst[off] = src[off]
			off++
		case U16, U32, U64, F32, F64:
			size := fixedSize(f)
			reverseRegion(dst[off:off+size], src[off:off+size], inPlace)
			off += size
		case Blob:
			copy(dst[off:off+f.N], src[off:off+f.N])
			off += f.N
		case VarBlob:
			// Read the length from the source header before writing,
			// so the in-place case stays valid.
			length := srcBO.Uint16(src[off : off+2])
			dstBO.PutUint16(dst[off:off+2], length)
			off += 2
			// Header converted; opaque payload stays byte-for-byte.
			copy(dst[off:off+int(length)], src[off:off+int(length)])
			off += int(length)
		}
	}
}

// reverseRegion writes src reversed into dst. In the in-place case the
// two regions are identical, so mirrored pairs must be swapped; writing
// forward would overwrite bytes before they are mirrored.
func reverseRegion(dst, src []byte, inPlace bool) {
	n := len(src)
	if inPlace {
		for i := 0; i < n/2; i++ {
			dst[i], dst[n-1-i] = dst[n-1-i], dst[i]
		}
		return
	}
	for i := 0; i < n; i++ {
		dst[i] = src[n-1-i]
	}
}
