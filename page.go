package pagestore

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

// Page file layout:
//
//	magic      4 bytes  "PGC1"
//	pageID     8 bytes  little-endian
//	dictID     8 bytes  little-endian
//	dictDigest 32 bytes SHA-256 of the dictionary content
//	payloadLen 4 bytes  little-endian
//	payload    payloadLen bytes (flate stream compressed with the dictionary)
//	crc32      4 bytes  IEEE, over everything above
//
// The header binds the page to the exact dictionary version by digest, so
// decoding never depends on which dictionary version happens to be newest.
const (
	pageMagic      = "PGC1"
	pageHeaderSize = 4 + 8 + 8 + 32 + 4
)

func encodePage(pageID uint64, key DictKey, payload []byte) []byte {
	buf := make([]byte, pageHeaderSize, pageHeaderSize+len(payload)+4)
	copy(buf[0:4], pageMagic)
	binary.LittleEndian.PutUint64(buf[4:12], pageID)
	binary.LittleEndian.PutUint64(buf[12:20], key.ID)
	copy(buf[20:52], key.Digest[:])
	binary.LittleEndian.PutUint32(buf[52:56], uint32(len(payload)))
	buf = append(buf, payload...)
	crc := crc32.ChecksumIEEE(buf)
	var tail [4]byte
	binary.LittleEndian.PutUint32(tail[:], crc)
	return append(buf, tail[:]...)
}

func decodePage(data []byte) (pageID uint64, key DictKey, payload []byte, err error) {
	if len(data) < pageHeaderSize+4 || string(data[:4]) != pageMagic {
		return 0, DictKey{}, nil, fmt.Errorf("%w: bad header", ErrPageCorrupt)
	}
	stored := binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(data[:len(data)-4]) != stored {
		return 0, DictKey{}, nil, fmt.Errorf("%w: crc mismatch", ErrPageCorrupt)
	}
	pageID = binary.LittleEndian.Uint64(data[4:12])
	key.ID = binary.LittleEndian.Uint64(data[12:20])
	copy(key.Digest[:], data[20:52])
	payloadLen := binary.LittleEndian.Uint32(data[52:56])
	if uint64(payloadLen) != uint64(len(data))-pageHeaderSize-4 {
		return 0, DictKey{}, nil, fmt.Errorf("%w: payload length mismatch", ErrPageCorrupt)
	}
	return pageID, key, data[pageHeaderSize : len(data)-4], nil
}

func compressWithDict(raw, dict []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := flate.NewWriterDict(&buf, flate.DefaultCompression, dict)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressWithDict(payload, dict []byte) ([]byte, error) {
	r := flate.NewReaderDict(bytes.NewReader(payload), dict)
	defer r.Close()
	return io.ReadAll(r)
}
