package keydir

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// On-disk record layout (all integers little-endian):
//
//	| crc32 (4) | version (8) | keyLen (2) | valLen (4) | key | value |
//
// The CRC covers everything after the CRC field. A record is the unit of
// atomicity: either the whole record is durable or the tail is torn, and
// readers must detect a torn tail via length or CRC failure.
const recordHeaderSize = 4 + 8 + 2 + 4

var (
	// ErrCorruptRecord means a record failed length or CRC validation.
	ErrCorruptRecord = errors.New("keydir: corrupt record")
	// ErrKeyTooLarge rejects keys that do not fit the 2-byte length field.
	ErrKeyTooLarge = errors.New("keydir: key too large")
)

type record struct {
	version uint64
	key     string
	value   []byte
}

func encodeRecord(rec record) ([]byte, error) {
	if len(rec.key) > 0xFFFF {
		return nil, ErrKeyTooLarge
	}
	buf := make([]byte, recordHeaderSize+len(rec.key)+len(rec.value))
	binary.LittleEndian.PutUint64(buf[4:12], rec.version)
	binary.LittleEndian.PutUint16(buf[12:14], uint16(len(rec.key)))
	binary.LittleEndian.PutUint32(buf[14:18], uint32(len(rec.value)))
	copy(buf[18:], rec.key)
	copy(buf[18+len(rec.key):], rec.value)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.ChecksumIEEE(buf[4:]))
	return buf, nil
}

// decodeRecordHeader parses the fixed-size header and returns the record
// version, key/value lengths and the total on-disk size of the record.
func decodeRecordHeader(hdr []byte) (version uint64, keyLen, valLen, total int, err error) {
	if len(hdr) < recordHeaderSize {
		return 0, 0, 0, 0, ErrCorruptRecord
	}
	version = binary.LittleEndian.Uint64(hdr[4:12])
	klen := int(binary.LittleEndian.Uint16(hdr[12:14]))
	vlen := int(binary.LittleEndian.Uint32(hdr[14:18]))
	return version, klen, vlen, recordHeaderSize + klen + vlen, nil
}

// decodeRecord validates a full record buffer (exactly total bytes).
func decodeRecord(buf []byte) (record, error) {
	if len(buf) < recordHeaderSize {
		return record{}, ErrCorruptRecord
	}
	if crc32.ChecksumIEEE(buf[4:]) != binary.LittleEndian.Uint32(buf[0:4]) {
		return record{}, ErrCorruptRecord
	}
	version, klen, _, total, err := decodeRecordHeader(buf)
	if err != nil {
		return record{}, err
	}
	if len(buf) != total {
		return record{}, ErrCorruptRecord
	}
	return record{
		version: version,
		key:     string(buf[recordHeaderSize : recordHeaderSize+klen]),
		value:   buf[recordHeaderSize+klen : total],
	}, nil
}
