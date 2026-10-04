package pagezip

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io/ioutil"
)

const pageMagic = "PZP1"

// pageHeader is the decoded on-disk page header. The (dictID, digest) pair
// pins the page to one exact dictionary version.
type pageHeader struct {
	dictID  string
	digest  Digest
	payload []byte
}

// Page layout: magic | u16 dictIDLen | dictID | 32B digest |
// u32 payloadLen | payload | crc32IEEE(everything before it).
func encodePage(h pageHeader) []byte {
	var buf bytes.Buffer
	buf.WriteString(pageMagic)
	writeU16(&buf, uint16(len(h.dictID)))
	buf.WriteString(h.dictID)
	buf.Write(h.digest[:])
	writeU32(&buf, uint32(len(h.payload)))
	buf.Write(h.payload)
	writeU32(&buf, crc32.ChecksumIEEE(buf.Bytes()))
	return buf.Bytes()
}

func decodePage(b []byte) (pageHeader, error) {
	var h pageHeader
	if len(b) < len(pageMagic)+2+sha256Size+4+4 {
		return h, errors.New("file too short")
	}
	if string(b[:len(pageMagic)]) != pageMagic {
		return h, errors.New("bad magic")
	}
	body := b[:len(b)-4]
	wantCRC := binary.BigEndian.Uint32(b[len(b)-4:])
	if crc32.ChecksumIEEE(body) != wantCRC {
		return h, errors.New("checksum mismatch")
	}
	off := len(pageMagic)
	idLen := int(binary.BigEndian.Uint16(body[off:]))
	off += 2
	if off+idLen+sha256Size+4 > len(body) {
		return h, errors.New("truncated header")
	}
	h.dictID = string(body[off : off+idLen])
	off += idLen
	copy(h.digest[:], body[off:off+sha256Size])
	off += sha256Size
	pLen := int(binary.BigEndian.Uint32(body[off:]))
	off += 4
	if off+pLen != len(body) {
		return h, errors.New("payload length mismatch")
	}
	h.payload = body[off : off+pLen]
	return h, nil
}

const sha256Size = 32

func writeU16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func compressWithDict(dict, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevelDict(&buf, zlib.BestCompression, dict)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressWithDict(dict, payload []byte) ([]byte, error) {
	r, err := zlib.NewReaderDict(bytes.NewReader(payload), dict)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return ioutil.ReadAll(r)
}
