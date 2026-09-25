package kvdb

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// On-disk record layout (little endian):
//
//	| crc32 (4B) | keyLen (4B) | valLen (4B) | flags (1B) | key | value |
//
// The CRC covers everything after the CRC field itself. A record whose
// flagTombstone bit is set is a deletion marker and carries no value.
const (
	flagTombstone    byte = 1 << 0
	recordHeaderSize      = 4 + 4 + 4 + 1
	maxRecordSize         = 1 << 30
)

var (
	castagnoli = crc32.MakeTable(crc32.Castagnoli)

	errCorruptRecord = errors.New("kvdb: corrupt record")
	errEmptyKey      = errors.New("kvdb: empty key")
)

func encodeRecord(key, value []byte, tombstone bool) []byte {
	var flags byte
	if tombstone {
		flags = flagTombstone
	}
	buf := make([]byte, recordHeaderSize+len(key)+len(value))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(key)))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(value)))
	buf[12] = flags
	copy(buf[recordHeaderSize:], key)
	copy(buf[recordHeaderSize+len(key):], value)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.Checksum(buf[4:], castagnoli))
	return buf
}

// readRecord decodes one record from r. It returns io.EOF only when the
// stream ends exactly on a record boundary; any partial or corrupt tail
// yields errCorruptRecord so the caller can truncate it.
func readRecord(r io.Reader) (key, value []byte, tombstone bool, err error) {
	var hdr [recordHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if err == io.EOF {
			return nil, nil, false, io.EOF
		}
		return nil, nil, false, errCorruptRecord
	}
	klen := binary.LittleEndian.Uint32(hdr[4:8])
	vlen := binary.LittleEndian.Uint32(hdr[8:12])
	if klen > maxRecordSize || vlen > maxRecordSize {
		return nil, nil, false, errCorruptRecord
	}
	body := make([]byte, klen+vlen)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, nil, false, errCorruptRecord
	}
	if crc32.Checksum(append(hdr[4:], body...), castagnoli) != binary.LittleEndian.Uint32(hdr[0:4]) {
		return nil, nil, false, errCorruptRecord
	}
	return body[:klen], body[klen:], hdr[12]&flagTombstone != 0, nil
}

// decodeRecord decodes a record from an in-memory buffer previously read
// from a known (offset, length) pair in the index.
func decodeRecord(buf []byte) (key, value []byte, tombstone bool, err error) {
	if len(buf) < recordHeaderSize {
		return nil, nil, false, errCorruptRecord
	}
	klen := binary.LittleEndian.Uint32(buf[4:8])
	vlen := binary.LittleEndian.Uint32(buf[8:12])
	if int(klen)+int(vlen) != len(buf)-recordHeaderSize {
		return nil, nil, false, errCorruptRecord
	}
	if crc32.Checksum(buf[4:], castagnoli) != binary.LittleEndian.Uint32(buf[0:4]) {
		return nil, nil, false, errCorruptRecord
	}
	return buf[recordHeaderSize : recordHeaderSize+klen],
		buf[recordHeaderSize+klen:],
		buf[12]&flagTombstone != 0, nil
}
