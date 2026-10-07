package bitmap

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// File layout (little endian):
//
//	magic      4 bytes  "RBMP"
//	version    uint16   currently 1
//	threshold  uint32   array/bitmap threshold used by the writer
//	count      uint32   number of containers
//	containers ...      repeated count times:
//	  key        uint16
//	  type       uint8   0 = array, 1 = bitmap
//	  card       uint32  writer's cardinality hint (not trusted on read)
//	  payload    array: card * uint16 values, sorted
//	             bitmap: wordCount * uint64 words
//	crc32      uint32   IEEE checksum of everything before it
const (
	fileMagic        = "RBMP"
	fileVersion      = 1
	containerHeader  = 2 + 1 + 4 // key + type + card
	arrayContainerT  = 0
	bitmapContainerT = 1
)

var (
	ErrInvalidMagic       = errors.New("bitmap: invalid magic")
	ErrUnsupportedVersion = errors.New("bitmap: unsupported version")
	ErrChecksumMismatch   = errors.New("bitmap: checksum mismatch")
	ErrTruncated          = errors.New("bitmap: truncated data")
	ErrCorrupt            = errors.New("bitmap: corrupt container data")
)

// MarshalBinary encodes b with version and checksum headers.
func (b *Bitmap) MarshalBinary() []byte {
	buf := make([]byte, 0, 64)
	buf = append(buf, fileMagic...)
	buf = binary.LittleEndian.AppendUint16(buf, fileVersion)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(b.threshold))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(b.keys)))
	for i, key := range b.keys {
		buf = binary.LittleEndian.AppendUint16(buf, key)
		switch c := b.containers[i].(type) {
		case *arrayContainer:
			buf = append(buf, arrayContainerT)
			buf = binary.LittleEndian.AppendUint32(buf, uint32(len(c.values)))
			for _, v := range c.values {
				buf = binary.LittleEndian.AppendUint16(buf, v)
			}
		case *bitmapContainer:
			buf = append(buf, bitmapContainerT)
			buf = binary.LittleEndian.AppendUint32(buf, uint32(c.card))
			for _, w := range c.bits {
				buf = binary.LittleEndian.AppendUint64(buf, w)
			}
		}
	}
	return binary.LittleEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
}

// UnmarshalBinary decodes data produced by MarshalBinary. The stored
// cardinality hints are never trusted: every container's cardinality is
// recomputed from its payload, and containers are re-normalized so the
// result is canonical regardless of what the file claims.
func UnmarshalBinary(data []byte) (*Bitmap, error) {
	if len(data) < 4+2+4+4+4 {
		return nil, ErrTruncated
	}
	body, crcBytes := data[:len(data)-4], data[len(data)-4:]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(crcBytes) {
		return nil, ErrChecksumMismatch
	}
	if string(body[:4]) != fileMagic {
		return nil, ErrInvalidMagic
	}
	if v := binary.LittleEndian.Uint16(body[4:6]); v != fileVersion {
		return nil, ErrUnsupportedVersion
	}
	threshold := int(binary.LittleEndian.Uint32(body[6:10]))
	count := binary.LittleEndian.Uint32(body[10:14])

	b := New(WithArrayMaxSize(threshold))
	pos := 14
	prevKey := -1
	for n := uint32(0); n < count; n++ {
		if len(body)-pos < containerHeader {
			return nil, ErrTruncated
		}
		key := binary.LittleEndian.Uint16(body[pos : pos+2])
		typ := body[pos+2]
		storedCard := binary.LittleEndian.Uint32(body[pos+3 : pos+7])
		pos += containerHeader
		if int(key) <= prevKey {
			return nil, ErrCorrupt // keys must be strictly increasing
		}
		prevKey = int(key)

		var c container
		switch typ {
		case arrayContainerT:
			if storedCard > 1<<16 || len(body)-pos < int(storedCard)*2 {
				return nil, ErrTruncated
			}
			ac := &arrayContainer{values: make([]uint16, storedCard)}
			prev := -1
			for i := range ac.values {
				v := binary.LittleEndian.Uint16(body[pos+i*2 : pos+i*2+2])
				if int(v) <= prev {
					return nil, ErrCorrupt // array payload must be sorted, deduplicated
				}
				prev = int(v)
				ac.values[i] = v
			}
			pos += int(storedCard) * 2
			c = ac // cardinality is len(values), recomputed by construction
		case bitmapContainerT:
			if len(body)-pos < wordCount*8 {
				return nil, ErrTruncated
			}
			bc := &bitmapContainer{}
			for i := range bc.bits {
				bc.bits[i] = binary.LittleEndian.Uint64(body[pos+i*8 : pos+i*8+8])
			}
			pos += wordCount * 8
			bc.recomputeCard() // ignore storedCard; derive from payload
			c = bc
		default:
			return nil, ErrCorrupt
		}

		c = normalize(c, b.threshold)
		if c.cardinality() == 0 {
			return nil, ErrCorrupt // canonical files never contain empty chunks
		}
		b.keys = append(b.keys, key)
		b.containers = append(b.containers, c)
	}
	if pos != len(body) {
		return nil, ErrCorrupt // trailing garbage
	}
	return b, nil
}
