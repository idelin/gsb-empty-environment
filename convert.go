package endianconv

import (
	"encoding/binary"
	"unsafe"
)

// Convert converts one record from byte order `from` to byte order `to`
// according to the schema. src must hold exactly one record (trailing bytes
// are ignored); the record length is determined by parsing length headers
// with `from`. maxRecord is the length quota, checked before any copy;
// values <= 0 mean "no limit".
//
// dst and src may be the exact same region (in-place conversion) or fully
// disjoint. Partially overlapping regions are rejected with ErrOverlap
// before any byte is written.
//
// It returns the number of bytes written, which equals the record length.
func (s Schema) Convert(dst, src []byte, from, to binary.ByteOrder, maxRecord int) (int, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	n, err := s.recordLen(src, from, maxRecord)
	if err != nil {
		return 0, err
	}
	if len(dst) < n {
		return 0, ErrShortBuffer
	}
	dst, src = dst[:n], src[:n]
	if overlaps(dst, src) && !sameRegion(dst, src) {
		return 0, ErrOverlap
	}

	off := 0
	for _, f := range s.Fields {
		switch f.Kind {
		case KindU8:
			dst[off] = src[off]
			off++
		case KindU16, KindU32, KindU64, KindF32, KindF64:
			// Floats are reversed byte-by-byte, never materialized as a
			// float value, so NaN payloads and signaling NaNs are kept.
			sz := f.fixedSize()
			reverseCopy(dst[off:off+sz], src[off:off+sz])
			off += sz
		case KindBytes:
			copy(dst[off:off+f.Size], src[off:off+f.Size])
			off += f.Size
		case KindVarBytes:
			// Read the length from src BEFORE writing the swapped header:
			// this is what makes in-place conversion safe.
			ln := readUint(src[off:off+f.Size], f.Size, from)
			putUint(dst[off:off+f.Size], f.Size, to, ln)
			off += f.Size
			// Payload is copied verbatim — never byte-swapped.
			copy(dst[off:off+int(ln)], src[off:off+int(ln)])
			off += int(ln)
		}
	}
	return n, nil
}

// ConvertRecord is a convenience wrapper that allocates the output and
// returns the converted record.
func (s Schema) ConvertRecord(src []byte, from, to binary.ByteOrder, maxRecord int) ([]byte, error) {
	n, err := s.recordLen(src, from, maxRecord)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, n)
	if _, err := s.Convert(dst, src, from, to, maxRecord); err != nil {
		return nil, err
	}
	return dst, nil
}

// reverseCopy copies b into dst reversing byte order. Safe for in-place use.
func reverseCopy(dst, src []byte) {
	if sameRegion(dst, src) {
		for i, j := 0, len(src)-1; i < j; i, j = i+1, j-1 {
			src[i], src[j] = src[j], src[i]
		}
		return
	}
	for i := range src {
		dst[i] = src[len(src)-1-i]
	}
}

// sameRegion reports whether a and b are the exact same memory region.
func sameRegion(a, b []byte) bool {
	return len(a) == len(b) && len(a) > 0 && &a[0] == &b[0] ||
		len(a) == 0 && len(b) == 0
}

// overlaps reports whether a and b share any memory.
func overlaps(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	pa := uintptr(unsafe.Pointer(&a[0]))
	pb := uintptr(unsafe.Pointer(&b[0]))
	return pa < pb+uintptr(len(b)) && pb < pa+uintptr(len(a))
}
