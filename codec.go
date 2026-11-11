package netsec

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

// magic identifies an audit log file; the high byte carries the format
// version so incompatible logs can be rejected.
const (
	logMagic    uint32 = 0x41554400 | logVersion // "AUD\0" | version
	logVersion  uint32 = 1
	headerSize         = 8 // magic(4) + reserved(4)
	framePrefix        = 4 // payload length
	crcSize            = 4
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// encodeRecord serializes one record into the on-disk payload. All variable
// fields are length-prefixed so records can be extended later without breaking
// older logs.
func encodeRecord(rec Record) []byte {
	fields := [4]string{rec.OpID, rec.Actor, rec.Action, rec.Detail}

	size := 8 + 1 // seq + status
	for _, f := range fields {
		size += 4 + len(f)
	}
	if rec.Status == StatusFailed {
		size += 4 + len(rec.Failure)
	}

	buf := make([]byte, 0, size)
	var tmp8 [8]byte
	var tmp [4]byte

	binary.BigEndian.PutUint64(tmp8[:], rec.Seq)
	buf = append(buf, tmp8[:]...)
	buf = append(buf, byte(rec.Status))

	for _, f := range fields {
		binary.BigEndian.PutUint32(tmp[:], uint32(len(f)))
		buf = append(buf, tmp[:]...)
		buf = append(buf, f...)
	}
	if rec.Status == StatusFailed {
		binary.BigEndian.PutUint32(tmp[:], uint32(len(rec.Failure)))
		buf = append(buf, tmp[:]...)
		buf = append(buf, rec.Failure...)
	}
	return buf
}

func decodeRecord(payload []byte) (Record, error) {
	var rec Record
	r := newReader(payload)

	seq, ok := r.uint64()
	if !ok {
		return rec, io.ErrUnexpectedEOF
	}
	rec.Seq = seq

	status, ok := r.byte()
	if !ok {
		return rec, io.ErrUnexpectedEOF
	}
	rec.Status = Status(status)

	fields := [...]*string{&rec.OpID, &rec.Actor, &rec.Action, &rec.Detail}
	for i := range fields {
		s, ok := r.string()
		if !ok {
			return rec, io.ErrUnexpectedEOF
		}
		*fields[i] = s
	}
	if rec.Status == StatusFailed {
		failure, ok := r.string()
		if !ok {
			return rec, io.ErrUnexpectedEOF
		}
		rec.Failure = failure
	}

	switch rec.Status {
	case StatusPending, StatusSucceeded, StatusFailed:
		return rec, nil
	default:
		return rec, ErrCorrupt
	}
}

// frame wraps a payload as: payload length (4) | payload | crc32 (4). The CRC
// covers the payload only; a torn tail write fails the CRC check instead of
// being mistaken for a valid event.
func encodeFrame(rec Record) []byte {
	payload := encodeRecord(rec)
	frame := make([]byte, 0, framePrefix+len(payload)+crcSize)

	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(len(payload)))
	frame = append(frame, tmp[:]...)
	frame = append(frame, payload...)

	checksum := crc32.Checksum(payload, crcTable)
	binary.BigEndian.PutUint32(tmp[:], checksum)
	frame = append(frame, tmp[:]...)
	return frame
}

func frameEncodedLen(rec Record) int {
	return framePrefix + len(encodeRecord(rec)) + crcSize
}

type byteReader struct {
	buf []byte
	pos int
}

func newReader(buf []byte) *byteReader { return &byteReader{buf: buf} }

func (r *byteReader) byte() (byte, bool) {
	if r.pos >= len(r.buf) {
		return 0, false
	}
	v := r.buf[r.pos]
	r.pos++
	return v, true
}

func (r *byteReader) uint64() (uint64, bool) {
	if r.pos+8 > len(r.buf) {
		return 0, false
	}
	v := binary.BigEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return v, true
}

func (r *byteReader) string() (string, bool) {
	if r.pos+4 > len(r.buf) {
		return "", false
	}
	n := int(binary.BigEndian.Uint32(r.buf[r.pos:]))
	r.pos += 4
	if n < 0 || r.pos+n > len(r.buf) {
		return "", false
	}
	s := string(r.buf[r.pos : r.pos+n])
	r.pos += n
	return s, true
}

func (r *byteReader) exhausted() bool { return r.pos == len(r.buf) }
