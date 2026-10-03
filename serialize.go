package cbitmap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"reflect"
	"unsafe"
)

// Binary format (all integers little-endian):
//
//	header (16 bytes):
//	  0  uint32  magic number 0x52424D50 ("RBMP")
//	  4  uint16  format version (currently 1)
//	  6  uint16  flags (unknown bits are ignored for forward compatibility)
//	  8  uint32  container count (<= 65536)
//	  12 uint32  reserved (must be written as 0, ignored on read)
//	descriptor array (count * 24 bytes):
//	  0  uint16  container key (high 16 bits), strictly increasing
//	  2  uint8   container type: 1=array, 2=bitmap, 3=run
//	  3  uint8   reserved
//	  4  uint32  cardinality
//	  8  uint32  meta (run container: number of runs; otherwise 0)
//	  12 uint64  absolute offset of the payload, 8-byte aligned
//	  20 uint32  reserved (written as 0, ignored on read)
//	payload sections, each 8-byte aligned:
//	  array:  cardinality * 2 bytes (sorted uint16 values)
//	  bitmap: 8192 bytes (1024 little-endian uint64 words)
//	  run:    runs * 4 bytes (uint16 start, uint16 length-1 pairs)
//
// Trailing bytes after the last payload are ignored, so newer writers may
// append extensions without breaking older readers (forward compatibility).
// Readers reject versions newer than formatVersion (backward safety).

const (
	magicNumber   uint32 = 0x52424D50
	formatVersion uint16 = 1
	headerSize           = 16
	descSize             = 24
	maxContainers        = 1 << 16
	maxRuns              = 32768
)

var (
	// ErrInvalidMagic is returned when the buffer is not a cbitmap payload.
	ErrInvalidMagic = errors.New("cbitmap: invalid magic number")
	// ErrUnsupportedVersion is returned for payloads newer than this library.
	ErrUnsupportedVersion = errors.New("cbitmap: unsupported format version")
)

// FormatError describes a malformed payload.
type FormatError struct{ Reason string }

func (e *FormatError) Error() string { return "cbitmap: malformed data: " + e.Reason }

func align8(n int) int { return (n + 7) &^ 7 }

// SerializedSize returns the exact size of MarshalBinary output.
func (bm *Bitmap) SerializedSize() int {
	n := headerSize + len(bm.cons)*descSize
	for _, c := range bm.cons {
		n = align8(n) + c.dataSize()
	}
	return n
}

// MarshalBinary encodes the bitmap into the compact binary format.
func (bm *Bitmap) MarshalBinary() []byte {
	n := len(bm.cons)
	buf := make([]byte, bm.SerializedSize())
	binary.LittleEndian.PutUint32(buf[0:], magicNumber)
	binary.LittleEndian.PutUint16(buf[4:], formatVersion)
	binary.LittleEndian.PutUint16(buf[6:], 0) // flags
	binary.LittleEndian.PutUint32(buf[8:], uint32(n))
	// buf[12:16] reserved, already zero
	off := headerSize + n*descSize
	for i, c := range bm.cons {
		off = align8(off)
		d := buf[headerSize+i*descSize:]
		binary.LittleEndian.PutUint16(d[0:], bm.keys[i])
		d[2] = byte(c.typeID())
		d[3] = 0
		binary.LittleEndian.PutUint32(d[4:], uint32(c.cardinality()))
		var meta uint32
		if rc, ok := c.(*runContainer); ok {
			meta = uint32(len(rc.runs))
		}
		binary.LittleEndian.PutUint32(d[8:], meta)
		binary.LittleEndian.PutUint64(d[12:], uint64(off))
		writeContainer(buf[off:off+c.dataSize()], c)
		off += c.dataSize()
	}
	return buf
}

func writeContainer(dst []byte, c container) {
	switch c := c.(type) {
	case *arrayContainer:
		for i, v := range c.data {
			binary.LittleEndian.PutUint16(dst[2*i:], v)
		}
	case *bitmapContainer:
		for i, w := range c.words {
			binary.LittleEndian.PutUint64(dst[8*i:], w)
		}
	case *runContainer:
		for i, r := range c.runs {
			binary.LittleEndian.PutUint16(dst[4*i:], r.start)
			binary.LittleEndian.PutUint16(dst[4*i+2:], r.length)
		}
	}
}

// UnmarshalBinary decodes a bitmap, copying all container data out of buf.
// The result is fully independent of buf and safe to mutate.
func UnmarshalBinary(buf []byte) (*Bitmap, error) { return parse(buf, false) }

// UnmarshalZeroCopy decodes a bitmap without copying container payloads:
// containers reference sub-slices of buf (which may be a memory-mapped
// file). The returned bitmap is frozen; mutating it triggers a private
// copy of the containers, leaving buf untouched. buf must not be modified
// or released while the bitmap is in use. On big-endian hosts or misaligned
// buffers it transparently falls back to copying.
func UnmarshalZeroCopy(buf []byte) (*Bitmap, error) { return parse(buf, true) }

func parse(buf []byte, zeroCopy bool) (*Bitmap, error) {
	if len(buf) < headerSize {
		return nil, &FormatError{fmt.Sprintf("truncated header (len=%d)", len(buf))}
	}
	if binary.LittleEndian.Uint32(buf[0:]) != magicNumber {
		return nil, ErrInvalidMagic
	}
	version := binary.LittleEndian.Uint16(buf[4:])
	if version == 0 || version > formatVersion {
		return nil, ErrUnsupportedVersion
	}
	// flags at buf[6:]: unknown bits ignored (forward compatibility)
	count := binary.LittleEndian.Uint32(buf[8:])
	if count > maxContainers {
		return nil, &FormatError{fmt.Sprintf("container count %d exceeds %d", count, maxContainers)}
	}
	if uint64(len(buf)) < uint64(headerSize)+uint64(count)*descSize {
		return nil, &FormatError{"truncated descriptor array"}
	}
	shared := zeroCopy && isLittleEndian && baseAligned(buf)
	bm := &Bitmap{frozen: zeroCopy}
	prevKey := -1
	for i := 0; i < int(count); i++ {
		d := buf[headerSize+i*descSize:]
		key := binary.LittleEndian.Uint16(d[0:])
		if int(key) <= prevKey {
			return nil, &FormatError{"container keys not strictly increasing"}
		}
		prevKey = int(key)
		ctype := d[2]
		card := binary.LittleEndian.Uint32(d[4:])
		meta := binary.LittleEndian.Uint32(d[8:])
		off := binary.LittleEndian.Uint64(d[12:])
		var size uint64
		switch ctype {
		case arrayContainerID:
			if card == 0 || card > 1<<16 {
				return nil, &FormatError{fmt.Sprintf("array cardinality %d out of range", card)}
			}
			size = uint64(card) * 2
		case bitmapContainerID:
			if card == 0 || card > 1<<16 {
				return nil, &FormatError{fmt.Sprintf("bitmap cardinality %d out of range", card)}
			}
			size = bitmapBytes
		case runContainerID:
			if meta == 0 || meta > maxRuns {
				return nil, &FormatError{fmt.Sprintf("run count %d out of range", meta)}
			}
			size = uint64(meta) * 4
		default:
			return nil, &FormatError{fmt.Sprintf("unknown container type %d", ctype)}
		}
		if off%8 != 0 {
			return nil, &FormatError{"payload offset not 8-byte aligned"}
		}
		// overflow-safe bounds check: both sides fit in uint64
		if off > uint64(len(buf)) || size > uint64(len(buf))-off {
			return nil, &FormatError{"payload offset/size out of bounds"}
		}
		data := buf[int(off):int(off+size)]
		c, err := buildContainer(ctype, card, meta, data, shared)
		if err != nil {
			return nil, err
		}
		bm.keys = append(bm.keys, key)
		bm.cons = append(bm.cons, c)
	}
	return bm, nil
}

func buildContainer(ctype byte, card, meta uint32, data []byte, shared bool) (container, error) {
	switch ctype {
	case arrayContainerID:
		var vals []uint16
		if shared {
			vals = bytesToUint16s(data)
		} else {
			vals = make([]uint16, card)
			for i := range vals {
				vals[i] = binary.LittleEndian.Uint16(data[2*i:])
			}
		}
		for i := 1; i < len(vals); i++ {
			if vals[i] <= vals[i-1] {
				return nil, &FormatError{"array container not strictly increasing"}
			}
		}
		return &arrayContainer{data: vals}, nil
	case bitmapContainerID:
		var words []uint64
		if shared {
			words = bytesToUint64s(data)
		} else {
			words = make([]uint64, bitmapWords)
			for i := range words {
				words[i] = binary.LittleEndian.Uint64(data[8*i:])
			}
		}
		c := 0
		for _, w := range words {
			c += bits.OnesCount64(w)
		}
		if uint32(c) != card {
			return nil, &FormatError{fmt.Sprintf("bitmap cardinality mismatch: header=%d actual=%d", card, c)}
		}
		return &bitmapContainer{words: words, card: c}, nil
	case runContainerID:
		var runs []run16
		if shared {
			runs = bytesToRuns(data)
		} else {
			runs = make([]run16, meta)
			for i := range runs {
				runs[i] = run16{
					start:  binary.LittleEndian.Uint16(data[4*i:]),
					length: binary.LittleEndian.Uint16(data[4*i+2:]),
				}
			}
		}
		var total uint32
		for i, r := range runs {
			if i > 0 && uint32(r.start) <= uint32(runs[i-1].last()) {
				return nil, &FormatError{"overlapping or unsorted runs"}
			}
			total += uint32(r.length) + 1
		}
		if total != card {
			return nil, &FormatError{fmt.Sprintf("run cardinality mismatch: header=%d actual=%d", card, total)}
		}
		return &runContainer{runs: runs, card: int(total)}, nil
	}
	return nil, &FormatError{fmt.Sprintf("unknown container type %d", ctype)}
}

// ---------------------------------------------------------------------------
// zero-copy reinterpretation helpers
// ---------------------------------------------------------------------------

var isLittleEndian = func() bool {
	var x uint16 = 1
	return *(*byte)(unsafe.Pointer(&x)) == 1
}()

func baseAligned(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	return uintptr(unsafe.Pointer(&b[0]))%8 == 0
}

func bytesToUint16s(b []byte) []uint16 {
	n := len(b) / 2
	hdr := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	out := *(*[]uint16)(unsafe.Pointer(&reflect.SliceHeader{Data: hdr.Data, Len: n, Cap: n}))
	return out
}

func bytesToUint64s(b []byte) []uint64 {
	n := len(b) / 8
	hdr := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	out := *(*[]uint64)(unsafe.Pointer(&reflect.SliceHeader{Data: hdr.Data, Len: n, Cap: n}))
	return out
}

func bytesToRuns(b []byte) []run16 {
	n := len(b) / 4
	hdr := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	out := *(*[]run16)(unsafe.Pointer(&reflect.SliceHeader{Data: hdr.Data, Len: n, Cap: n}))
	return out
}
