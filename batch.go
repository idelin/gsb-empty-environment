package floatconv

import (
	"encoding/binary"
	"errors"
	"unsafe"
)

// ErrSrcLength reports a source buffer whose length is not a whole number
// of source elements.
var ErrSrcLength = errors.New("floatconv: source length is not a multiple of the source element size")

// ErrDstTooSmall reports a destination buffer too small for the converted
// output.
var ErrDstTooSmall = errors.New("floatconv: destination buffer too small")

// ConvertBatch converts a slice of source elements to the destination
// format. Elements are stored little-endian, each occupying
// Format.Size() bytes. It returns the bitwise OR of the exception flags
// raised by the individual conversions.
//
// dst and src may partially overlap: the result is always equivalent to
// reading the complete source sequence before writing any destination
// element.
//
// Structural errors (invalid format, misaligned source length, undersized
// destination) are detected before any write takes place, so dst is left
// completely untouched when an error is returned.
func ConvertBatch(dst []byte, dstFmt Format, src []byte, srcFmt Format, mode RoundingMode) (Flags, error) {
	if err := dstFmt.Validate(); err != nil {
		return 0, err
	}
	if err := srcFmt.Validate(); err != nil {
		return 0, err
	}
	ss, ds := srcFmt.Size(), dstFmt.Size()
	if len(src)%ss != 0 {
		return 0, ErrSrcLength
	}
	n := len(src) / ss
	need := n * ds
	if len(dst) < need {
		return 0, ErrDstTooSmall
	}
	dst = dst[:need]

	// All validation is done above this point; dst is written only on the
	// success path. If the buffers overlap, snapshot the source first so
	// the conversion observes the original source sequence.
	if overlap(dst, src) {
		src = append([]byte(nil), src...)
	}

	var fl Flags
	switch {
	case ss == 2 && ds == 2:
		for i := 0; i < n; i++ {
			b, f := convert(dstFmt, srcFmt, uint64(binary.LittleEndian.Uint16(src[i*2:])), mode)
			binary.LittleEndian.PutUint16(dst[i*2:], uint16(b))
			fl |= f
		}
	case ss == 4 && ds == 2:
		for i := 0; i < n; i++ {
			b, f := convert(dstFmt, srcFmt, uint64(binary.LittleEndian.Uint32(src[i*4:])), mode)
			binary.LittleEndian.PutUint16(dst[i*2:], uint16(b))
			fl |= f
		}
	case ss == 2 && ds == 4:
		for i := 0; i < n; i++ {
			b, f := convert(dstFmt, srcFmt, uint64(binary.LittleEndian.Uint16(src[i*2:])), mode)
			binary.LittleEndian.PutUint32(dst[i*4:], uint32(b))
			fl |= f
		}
	default:
		for i := 0; i < n; i++ {
			b, f := convert(dstFmt, srcFmt, readElem(src[i*ss:], ss), mode)
			writeElem(dst[i*ds:], ds, b)
			fl |= f
		}
	}
	return fl, nil
}

func readElem(b []byte, size int) uint64 {
	switch size {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(binary.LittleEndian.Uint16(b))
	case 4:
		return uint64(binary.LittleEndian.Uint32(b))
	default:
		return binary.LittleEndian.Uint64(b)
	}
}

func writeElem(b []byte, size int, v uint64) {
	switch size {
	case 1:
		b[0] = byte(v)
	case 2:
		binary.LittleEndian.PutUint16(b, uint16(v))
	case 4:
		binary.LittleEndian.PutUint32(b, uint32(v))
	default:
		binary.LittleEndian.PutUint64(b, v)
	}
}

// overlap reports whether the two byte slices share any memory.
func overlap(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	pa := uintptr(unsafe.Pointer(unsafe.SliceData(a)))
	pb := uintptr(unsafe.Pointer(unsafe.SliceData(b)))
	return pa < pb+uintptr(len(b)) && pb < pa+uintptr(len(a))
}
