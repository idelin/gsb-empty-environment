package roaring

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"unsafe"
)

// Binary format (version 1, little-endian):
//
//	offset  size  field
//	0       4     magic "ROAR" (0x524F4152)
//	4       2     format version (decoder accepts any version <= formatVersion)
//	6       2     flags (reserved, ignored by decoders: forward compatible)
//	8       4     container count N
//	12      4     reserved (ignored)
//	16      8*N   container headers, sorted by key:
//	                key uint16 | type uint8 | reserved uint8 | cardinality uint32
//	16+8N   ...   container payloads, each aligned to 8 bytes:
//	                array : cardinality * uint16 values (ascending)
//	                bitmap: 1024 * uint64 words (8192 bytes)
//	                run   : uint32 runCount, then runCount * (start, length) uint16 pairs
//
// Trailing bytes after the last payload are ignored, so future format
// revisions may append data without breaking old readers.
const (
	magicNumber   uint32 = 0x524F4152
	formatVersion uint16 = 1

	serialHeaderSize = 16
	serialEntrySize  = 8
	serialAlign      = 8
	maxRunCount      = 32768 // max non-overlapping runs in 65536 values
)

var (
	// ErrInvalidFormat is returned when serialized data is malformed.
	ErrInvalidFormat = errors.New("roaring: invalid serialized data")
	// ErrUnsupportedVersion is returned for data written by a newer format.
	ErrUnsupportedVersion = errors.New("roaring: unsupported format version")
	// ErrUnsupportedPlatform is returned when zero-copy decoding is
	// attempted on a big-endian machine.
	ErrUnsupportedPlatform = errors.New("roaring: zero-copy decoding requires a little-endian machine")
)

var nativeLittleEndian = func() bool {
	var x uint16 = 1
	return *(*byte)(unsafe.Pointer(&x)) == 1
}()

// SerializedSize returns the exact number of bytes Serialize will produce.
func (b *Bitmap) SerializedSize() int {
	n := serialHeaderSize + serialEntrySize*len(b.entries)
	for i := range b.entries {
		n = alignUpInt(n, serialAlign) + b.entries[i].c.serializedSize()
	}
	return n
}

// Serialize encodes the bitmap into a newly allocated byte slice.
func (b *Bitmap) Serialize() []byte {
	buf := make([]byte, b.SerializedSize())
	binary.LittleEndian.PutUint32(buf[0:], magicNumber)
	binary.LittleEndian.PutUint16(buf[4:], formatVersion)
	// buf[6:8] flags, buf[12:16] reserved: left zero.
	binary.LittleEndian.PutUint32(buf[8:], uint32(len(b.entries)))
	off := serialHeaderSize
	for i := range b.entries {
		e := &b.entries[i]
		binary.LittleEndian.PutUint16(buf[off:], e.key)
		buf[off+2] = e.c.typ()
		binary.LittleEndian.PutUint32(buf[off+4:], uint32(e.c.cardinality()))
		off += serialEntrySize
	}
	for i := range b.entries {
		e := &b.entries[i]
		off = alignUpInt(off, serialAlign)
		writePayload(buf[off:], e.c)
		off += e.c.serializedSize()
	}
	return buf
}

func writePayload(buf []byte, c container) {
	switch t := c.(type) {
	case *arrayContainer:
		for i, v := range t.vals {
			binary.LittleEndian.PutUint16(buf[2*i:], v)
		}
	case *bitmapContainer:
		for i, w := range t.words {
			binary.LittleEndian.PutUint64(buf[8*i:], w)
		}
	case *runContainer:
		binary.LittleEndian.PutUint32(buf, uint32(len(t.iv)))
		for i, iv := range t.iv {
			binary.LittleEndian.PutUint16(buf[4+4*i:], iv.start)
			binary.LittleEndian.PutUint16(buf[4+4*i+2:], iv.length)
		}
	}
}

// Deserialize decodes a bitmap from buf in zero-copy mode: container data
// structures alias buf directly without copying. The caller must keep buf
// alive and must not modify it for as long as the bitmap (or any bitmap
// derived from it) is in use. All lengths and offsets are validated, so
// malformed or adversarial input returns an error instead of panicking or
// overflowing.
func Deserialize(buf []byte) (*Bitmap, error) {
	if !nativeLittleEndian {
		return nil, ErrUnsupportedPlatform
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("%v: empty buffer", ErrInvalidFormat)
	}
	// Zero-copy casts require 8-byte aligned payloads; realign if needed.
	if uintptr(unsafe.Pointer(&buf[0]))%serialAlign != 0 {
		tmp := make([]byte, len(buf)+serialAlign)
		pad := int((-uintptr(unsafe.Pointer(&tmp[0]))) & (serialAlign - 1))
		aligned := tmp[pad : pad+len(buf)]
		copy(aligned, buf)
		buf = aligned
	}
	if len(buf) < serialHeaderSize {
		return nil, fmt.Errorf("%v: header truncated (%d bytes)", ErrInvalidFormat, len(buf))
	}
	if binary.LittleEndian.Uint32(buf[0:]) != magicNumber {
		return nil, fmt.Errorf("%v: bad magic number", ErrInvalidFormat)
	}
	version := binary.LittleEndian.Uint16(buf[4:])
	if version > formatVersion {
		return nil, fmt.Errorf("%v: data has version %d, this build supports up to %d",
			ErrUnsupportedVersion, version, formatVersion)
	}
	// flags (buf[6:8]) and reserved (buf[12:16]) are ignored for
	// forward compatibility.
	count := binary.LittleEndian.Uint32(buf[8:])
	// Use 64-bit arithmetic so a hostile count cannot overflow the offset.
	entryTableEnd := uint64(serialHeaderSize) + uint64(count)*uint64(serialEntrySize)
	if entryTableEnd > uint64(len(buf)) {
		return nil, fmt.Errorf("%v: container table truncated (count=%d, len=%d)", ErrInvalidFormat, count, len(buf))
	}
	type rawEntry struct {
		key  uint16
		typ  byte
		card uint32
	}
	raws := make([]rawEntry, 0, count)
	off := uint64(serialHeaderSize)
	prevKey := -1
	for i := uint32(0); i < count; i++ {
		key := binary.LittleEndian.Uint16(buf[off:])
		typ := buf[off+2]
		card := binary.LittleEndian.Uint32(buf[off+4:])
		if typ < containerTypeArray || typ > containerTypeRun {
			return nil, fmt.Errorf("%v: unknown container type %d", ErrInvalidFormat, typ)
		}
		if card == 0 || card > containerCapacity {
			return nil, fmt.Errorf("%v: invalid cardinality %d", ErrInvalidFormat, card)
		}
		if int(key) <= prevKey {
			return nil, fmt.Errorf("%v: container keys not strictly increasing", ErrInvalidFormat)
		}
		prevKey = int(key)
		raws = append(raws, rawEntry{key: key, typ: typ, card: card})
		off += serialEntrySize
	}
	dataOff := entryTableEnd
	bm := &Bitmap{entries: make([]bitmapEntry, 0, count)}
	for _, re := range raws {
		dataOff = alignUp(dataOff, serialAlign)
		c, next, err := parseContainer(buf, dataOff, re.typ, re.card)
		if err != nil {
			return nil, err
		}
		dataOff = next
		bm.entries = append(bm.entries, bitmapEntry{key: re.key, c: c, cow: true})
	}
	// Any trailing bytes are ignored (forward compatibility).
	return bm, nil
}

func parseContainer(buf []byte, off uint64, typ byte, card uint32) (container, uint64, error) {
	if off > uint64(len(buf)) {
		return nil, 0, fmt.Errorf("%v: payload offset out of range", ErrInvalidFormat)
	}
	remaining := uint64(len(buf)) - off
	switch typ {
	case containerTypeArray:
		size := uint64(card) * 2
		if size > remaining {
			return nil, 0, fmt.Errorf("%v: array payload truncated", ErrInvalidFormat)
		}
		vals := castUint16s(buf[off : off+size])
		for i := 1; i < len(vals); i++ {
			if vals[i] <= vals[i-1] {
				return nil, 0, fmt.Errorf("%v: array values not strictly increasing", ErrInvalidFormat)
			}
		}
		return &arrayContainer{vals: vals}, off + size, nil
	case containerTypeBitmap:
		size := uint64(bitmapWords * 8)
		if size > remaining {
			return nil, 0, fmt.Errorf("%v: bitmap payload truncated", ErrInvalidFormat)
		}
		words := castUint64s(buf[off : off+size])
		card0 := 0
		for _, w := range words {
			card0 += bits.OnesCount64(w)
		}
		if uint32(card0) != card {
			return nil, 0, fmt.Errorf("%v: bitmap cardinality mismatch (header %d, actual %d)", ErrInvalidFormat, card, card0)
		}
		return &bitmapContainer{words: words, card: card0}, off + size, nil
	case containerTypeRun:
		if remaining < 4 {
			return nil, 0, fmt.Errorf("%v: run payload truncated", ErrInvalidFormat)
		}
		n := binary.LittleEndian.Uint32(buf[off:])
		if n == 0 || n > maxRunCount {
			return nil, 0, fmt.Errorf("%v: invalid run count %d", ErrInvalidFormat, n)
		}
		size := 4 + uint64(n)*4
		if size > remaining {
			return nil, 0, fmt.Errorf("%v: run payload truncated", ErrInvalidFormat)
		}
		iv := castIntervals(buf[off+4 : off+size])
		total := 0
		prevEnd := -1
		for _, r := range iv {
			start := int(r.start)
			if start <= prevEnd {
				return nil, 0, fmt.Errorf("%v: overlapping or unsorted runs", ErrInvalidFormat)
			}
			prevEnd = start + int(r.length)
			total += int(r.length) + 1
		}
		if uint32(total) != card {
			return nil, 0, fmt.Errorf("%v: run cardinality mismatch (header %d, actual %d)", ErrInvalidFormat, card, total)
		}
		return &runContainer{iv: iv}, off + size, nil
	}
	return nil, 0, fmt.Errorf("%v: unknown container type %d", ErrInvalidFormat, typ)
}

func alignUp(v, a uint64) uint64 { return (v + a - 1) &^ (a - 1) }

func alignUpInt(v, a int) int { return (v + a - 1) &^ (a - 1) }

// castUint16s reinterprets b (even length, 2-byte aligned) as uint16s
// without copying.
func castUint16s(b []byte) []uint16 {
	if len(b) == 0 {
		return nil
	}
	return (*[1 << 28]uint16)(unsafe.Pointer(&b[0]))[: len(b)/2 : len(b)/2]
}

// castUint64s reinterprets b (multiple of 8 bytes, 8-byte aligned) as
// uint64s without copying.
func castUint64s(b []byte) []uint64 {
	if len(b) == 0 {
		return nil
	}
	return (*[1 << 27]uint64)(unsafe.Pointer(&b[0]))[: len(b)/8 : len(b)/8]
}

// castIntervals reinterprets b (multiple of 4 bytes) as run intervals
// without copying.
func castIntervals(b []byte) []runInterval {
	if len(b) == 0 {
		return nil
	}
	return (*[1 << 28]runInterval)(unsafe.Pointer(&b[0]))[: len(b)/4 : len(b)/4]
}
