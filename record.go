package kvlog

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// ErrCorrupt marks a record that is incomplete, torn, or fails validation.
var ErrCorrupt = errors.New("kvlog: corrupt record")

const (
	recordMagic = 0x4B564C52 // "KVLR"
	headerSize  = 28         // magic(4) gen(8) version(8) keyLen(4) valLen(4)
	maxFieldLen = 1 << 26    // sanity cap against garbage length fields
)

// record is the unit of truth in the data log. Every record carries its
// own key, version, and file generation so any offset can be validated
// before it is trusted.
type record struct {
	gen     uint64
	version uint64
	key     string
	value   []byte
}

func encodeRecord(rec record) []byte {
	buf := make([]byte, headerSize+len(rec.key)+len(rec.value)+4)
	binary.LittleEndian.PutUint32(buf[0:4], recordMagic)
	binary.LittleEndian.PutUint64(buf[4:12], rec.gen)
	binary.LittleEndian.PutUint64(buf[12:20], rec.version)
	binary.LittleEndian.PutUint32(buf[20:24], uint32(len(rec.key)))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(len(rec.value)))
	copy(buf[headerSize:], rec.key)
	copy(buf[headerSize+len(rec.key):], rec.value)
	crc := crc32.ChecksumIEEE(buf[:len(buf)-4])
	binary.LittleEndian.PutUint32(buf[len(buf)-4:], crc)
	return buf
}

// decodeRecord validates buf as one complete record and returns it along
// with its total encoded size.
func decodeRecord(buf []byte) (record, int, error) {
	if len(buf) < headerSize+4 {
		return record{}, 0, ErrCorrupt
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != recordMagic {
		return record{}, 0, ErrCorrupt
	}
	keyLen := binary.LittleEndian.Uint32(buf[20:24])
	valLen := binary.LittleEndian.Uint32(buf[24:28])
	if keyLen > maxFieldLen || valLen > maxFieldLen {
		return record{}, 0, ErrCorrupt
	}
	total := headerSize + int(keyLen) + int(valLen) + 4
	if len(buf) < total {
		return record{}, 0, ErrCorrupt
	}
	want := binary.LittleEndian.Uint32(buf[total-4 : total])
	if crc32.ChecksumIEEE(buf[:total-4]) != want {
		return record{}, 0, ErrCorrupt
	}
	rec := record{
		gen:     binary.LittleEndian.Uint64(buf[4:12]),
		version: binary.LittleEndian.Uint64(buf[12:20]),
		key:     string(buf[headerSize : headerSize+int(keyLen)]),
		value:   append([]byte(nil), buf[headerSize+int(keyLen):total-4]...),
	}
	return rec, total, nil
}

// readRecordAt reads and validates one record at an absolute offset.
func readRecordAt(f File, off int64) (record, int, error) {
	header := make([]byte, headerSize)
	if err := readFullAt(f, header, off); err != nil {
		return record{}, 0, ErrCorrupt
	}
	if binary.LittleEndian.Uint32(header[0:4]) != recordMagic {
		return record{}, 0, ErrCorrupt
	}
	keyLen := binary.LittleEndian.Uint32(header[20:24])
	valLen := binary.LittleEndian.Uint32(header[24:28])
	if keyLen > maxFieldLen || valLen > maxFieldLen {
		return record{}, 0, ErrCorrupt
	}
	buf := make([]byte, headerSize+int(keyLen)+int(valLen)+4)
	if err := readFullAt(f, buf, off); err != nil {
		return record{}, 0, ErrCorrupt
	}
	return decodeRecord(buf)
}

func readFullAt(f File, buf []byte, off int64) error {
	for len(buf) > 0 {
		n, err := f.ReadAt(buf, off)
		if err != nil {
			return err
		}
		buf = buf[n:]
		off += int64(n)
	}
	return nil
}
