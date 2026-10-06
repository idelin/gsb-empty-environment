package bitmap

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// On-disk layout, all integers little-endian:
//
//	magic        uint32  formatMagic
//	version      uint32  formatVersion
//	threshold    uint32  array/bitmap switch threshold
//	containers   uint32  number of containers
//	per container:
//	  key        uint16  high 16 bits, strictly increasing
//	  kind       uint8   0 = array, 1 = bitmap
//	  reserved   uint8   must be 0
//	  card       uint32  advisory cardinality (never trusted on load)
//	  payload    array: card * uint16, strictly increasing
//	             bitmap: 1024 * uint64
//	checksum     uint32  CRC-32 (IEEE) of all preceding bytes
const (
	formatMagic   uint32 = 0x52424D31 // "RBM1"
	formatVersion uint32 = 1

	headerSize   = 16
	checksumSize = 4
)

// Validation errors returned by Unmarshal and UnmarshalBinary.
var (
	ErrBadMagic   = errors.New("bitmap: bad magic")
	ErrBadVersion = errors.New("bitmap: unsupported format version")
	ErrChecksum   = errors.New("bitmap: checksum mismatch")
	ErrCorrupt    = errors.New("bitmap: corrupt or non-canonical data")
)

// MarshalBinary serializes the bitmap including format version and a
// CRC-32 checksum over the whole payload.
func (b *Bitmap) MarshalBinary() ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 64))
	writeUint32(buf, formatMagic)
	writeUint32(buf, formatVersion)
	writeUint32(buf, uint32(b.threshold))
	writeUint32(buf, uint32(len(b.keys)))
	for i, key := range b.keys {
		c := &b.cons[i]
		writeUint16(buf, key)
		buf.WriteByte(byte(c.kind))
		buf.WriteByte(0)
		writeUint32(buf, uint32(c.card))
		if c.kind == kindArray {
			for _, v := range c.array {
				writeUint16(buf, v)
			}
		} else {
			for _, w := range c.words {
				writeUint64(buf, w)
			}
		}
	}
	return appendChecksum(buf.Bytes()), nil
}

// appendChecksum returns payload with its CRC-32 (IEEE) appended.
func appendChecksum(payload []byte) []byte {
	out := make([]byte, len(payload)+checksumSize)
	copy(out, payload)
	binary.LittleEndian.PutUint32(out[len(payload):], crc32.ChecksumIEEE(payload))
	return out
}

// UnmarshalBinary replaces the receiver with the decoded bitmap.
func (b *Bitmap) UnmarshalBinary(data []byte) error {
	nb, err := Unmarshal(data)
	if err != nil {
		return err
	}
	*b = *nb
	return nil
}

// Unmarshal decodes a serialized bitmap. The checksum, magic, version
// and all structural invariants are verified. Stored cardinalities are
// never trusted: every container's cardinality is recomputed from its
// payload, and each container is normalized to the canonical
// representation for the stored threshold.
func Unmarshal(data []byte) (*Bitmap, error) {
	if len(data) < headerSize+checksumSize {
		return nil, ErrCorrupt
	}
	payload := data[:len(data)-checksumSize]
	want := binary.LittleEndian.Uint32(data[len(data)-checksumSize:])
	if crc32.ChecksumIEEE(payload) != want {
		return nil, ErrChecksum
	}
	r := &reader{data: payload}
	if r.uint32() != formatMagic {
		return nil, ErrBadMagic
	}
	if r.uint32() != formatVersion {
		return nil, ErrBadVersion
	}
	threshold := int64(r.uint32())
	count := r.uint32()
	if r.err != nil || threshold < 1 || threshold > maxContainerCard {
		return nil, ErrCorrupt
	}
	b := New(int(threshold))
	prevKey := -1
	for k := uint32(0); k < count; k++ {
		key := r.uint16()
		kind := containerKind(r.uint8())
		reserved := r.uint8()
		storedCard := r.uint32()
		if r.err != nil || reserved != 0 || (kind != kindArray && kind != kindBitmap) {
			return nil, ErrCorrupt
		}
		if int(key) <= prevKey {
			return nil, ErrCorrupt
		}
		prevKey = int(key)
		var c container
		switch kind {
		case kindArray:
			n := int(storedCard)
			if n < 1 || n > maxContainerCard {
				return nil, ErrCorrupt
			}
			arr := make([]uint16, n)
			prev := -1
			for i := range arr {
				v := r.uint16()
				if r.err != nil {
					return nil, ErrCorrupt
				}
				if int(v) <= prev {
					return nil, ErrCorrupt
				}
				prev = int(v)
				arr[i] = v
			}
			// Cardinality is derived from the payload, not the header.
			c = newArrayContainer(arr)
		case kindBitmap:
			words := make([]uint64, bitmapWords)
			for i := range words {
				words[i] = r.uint64()
			}
			if r.err != nil {
				return nil, ErrCorrupt
			}
			// The stored cardinality is ignored and recomputed.
			c = newBitmapContainer(words)
			if c.card == 0 {
				return nil, ErrCorrupt
			}
		}
		c.normalize(b.threshold)
		b.keys = append(b.keys, key)
		b.cons = append(b.cons, c)
	}
	if r.err != nil || r.off != len(payload) {
		return nil, ErrCorrupt
	}
	return b, nil
}

func writeUint16(buf *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], v)
	buf.Write(tmp[:])
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	buf.Write(tmp[:])
}

func writeUint64(buf *bytes.Buffer, v uint64) {
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	buf.Write(tmp[:])
}

// reader is a bounds-checked little-endian cursor with a sticky error.
type reader struct {
	data []byte
	off  int
	err  error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || len(r.data)-r.off < n {
		r.err = ErrCorrupt
		return nil
	}
	b := r.data[r.off : r.off+n]
	r.off += n
	return b
}

func (r *reader) uint8() uint8 {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *reader) uint16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func (r *reader) uint32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (r *reader) uint64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}
