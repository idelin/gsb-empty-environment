package structmut

import "encoding/binary"

func validWidth(w Width) bool {
	switch w {
	case 1, 2, 4, 8:
		return true
	default:
		return false
	}
}

func uintMax(w Width) uint64 {
	return (uint64(1) << (8 * w)) - 1
}

func fitsUint(v uint64, w Width) bool {
	return v <= uintMax(w)
}

func fitsInt(v int64, w Width) bool {
	min := int64(-1) << (8*w - 1)
	max := int64(uintMax(w) >> 1)
	return v >= min && v <= max
}

func putUint(b []byte, w Width, v uint64) {
	switch w {
	case 1:
		b[0] = byte(v)
	case 2:
		binary.LittleEndian.PutUint16(b, uint16(v))
	case 4:
		binary.LittleEndian.PutUint32(b, uint32(v))
	case 8:
		binary.LittleEndian.PutUint64(b, v)
	}
}

// putInt stores v as a two's-complement little-endian integer of width w.
func putInt(b []byte, w Width, v int64) {
	putUint(b, w, uint64(v))
}
