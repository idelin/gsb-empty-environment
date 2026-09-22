package fp

import (
	"encoding/binary"
	"errors"
	"unsafe"
)

// Batch converts count packed values from src using srcFmt into dst using
// dstFmt. Source and destination byte slices may partially or fully overlap:
// the result is identical to reading the entire source sequence before any
// destination write occurs.
//
// Structural errors (nil buffers, insufficient length) are detected before
// any state change, and on return dst is left completely unmodified.
func Batch(dstFmt, srcFmt *Format, dst, src []byte, count int, mode Round) ([]Flags, error) {
	if err := dstFmt.validate(); err != nil {
		return nil, err
	}
	if err := srcFmt.validate(); err != nil {
		return nil, err
	}
	if count < 0 {
		return nil, errors.New("fp: negative conversion count")
	}
	sw := srcFmt.Width()
	dw := dstFmt.Width()
	if len(src) < count*sw {
		return nil, errors.New("fp: source buffer too short")
	}
	if len(dst) < count*dw {
		return nil, errors.New("fp: destination buffer too short")
	}
	if count == 0 {
		return nil, nil
	}

	// Snapshot semantics for overlapping regions. A same-backing overlap is
	// detected conservatively through pointer ranges; the common disjoint
	// case allocates only the output flag slice.
	if overlap(dst[:count*dw], src[:count*sw]) {
		snapshot := make([]byte, count*sw)
		copy(snapshot, src[:count*sw])
		src = snapshot
	}

	out := make([]Flags, count)
	for i := 0; i < count; i++ {
		raw := decodePattern(src[i*sw:], sw)
		r := Convert(dstFmt, srcFmt, raw, mode)
		encodePattern(dst[i*dw:], dw, r.Bits)
		out[i] = r.Flags
	}
	return out, nil
}

func decodePattern(b []byte, width int) uint64 {
	switch width {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(binary.LittleEndian.Uint16(b))
	case 4:
		return uint64(binary.LittleEndian.Uint32(b))
	case 8:
		return binary.LittleEndian.Uint64(b)
	default:
		var v uint64
		for i := 0; i < width; i++ {
			v |= uint64(b[i]) << uint(8*i)
		}
		return v
	}
}

func encodePattern(b []byte, width int, v uint64) {
	switch width {
	case 1:
		b[0] = byte(v)
	case 2:
		binary.LittleEndian.PutUint16(b, uint16(v))
	case 4:
		binary.LittleEndian.PutUint32(b, uint32(v))
	case 8:
		binary.LittleEndian.PutUint64(b, v)
	default:
		for i := 0; i < width; i++ {
			b[i] = byte(v >> uint(8*i))
		}
	}
}

// overlap reports whether the byte ranges [a,a+na) and [b,b+nb) share memory.
func overlap(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	pa := uintptr(unsafe.Pointer(&a[0]))
	pb := uintptr(unsafe.Pointer(&b[0]))
	endA := pa + uintptr(len(a))
	endB := pb + uintptr(len(b))
	return pa < endB && pb < endA
}
