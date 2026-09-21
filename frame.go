// Package framechain implements a chained frame protocol: every frame
// carries a sequence number, the digest of the previous frame, and a CRC
// over its own bytes, so a stream of frames forms a tamper-evident chain.
package framechain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Binary frame layout (big-endian):
//
//	magic    4 bytes   "FCH1"
//	seq      8 bytes   uint64 sequence number
//	prev     32 bytes  SHA-256 digest of the previous frame
//	paylen   4 bytes   uint32 payload length
//	payload  paylen    payload bytes
//	crc32    4 bytes   IEEE CRC-32 over magic..payload
const (
	Magic      = "FCH1"
	DigestSize = sha256.Size
	headerSize = 4 + 8 + DigestSize + 4
	crcSize    = 4
	MaxPayload = 1 << 20 // length quota, checked before any allocation
	GenesisSeq = 0
)

// GenesisPrev is the prev-digest of the first frame in a chain.
var GenesisPrev [DigestSize]byte

// Frame is a single chained frame.
type Frame struct {
	Seq     uint64
	Prev    [DigestSize]byte
	Payload []byte
}

// ErrBadData marks corruption (bad magic, bad CRC, oversize length, ...),
// as opposed to a simple lack of input bytes.
var ErrBadData = errors.New("framechain: bad data")

// encode serializes f. crc is appended by Encode; encodeBody is shared.
func (f *Frame) encodeBody() []byte {
	body := make([]byte, 0, headerSize+len(f.Payload))
	body = append(body, Magic...)
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], f.Seq)
	body = append(body, tmp[:]...)
	body = append(body, f.Prev[:]...)
	binary.BigEndian.PutUint32(tmp[:4], uint32(len(f.Payload)))
	body = append(body, tmp[:4]...)
	body = append(body, f.Payload...)
	return body
}

// Encode returns the full wire encoding of f, CRC included.
func (f *Frame) Encode() []byte {
	body := f.encodeBody()
	var crc [crcSize]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(body))
	return append(body, crc[:]...)
}

// Digest is the chain digest of a frame: SHA-256 over its full encoding.
func Digest(encoded []byte) [DigestSize]byte {
	return sha256.Sum256(encoded)
}

// DigestOf returns the chain digest of f.
func (f *Frame) DigestOf() [DigestSize]byte { return Digest(f.Encode()) }

// frameSize returns the total encoded size given a payload length.
func frameSize(paylen uint32) int { return headerSize + int(paylen) + crcSize }

// decode parses one complete frame from buf (len(buf) >= frameSize(paylen)).
func decode(buf []byte, paylen uint32) (*Frame, error) {
	total := frameSize(paylen)
	if uint32(len(buf[headerSize:total-crcSize])) != paylen { // sanity
		return nil, fmt.Errorf("%w: length mismatch", ErrBadData)
	}
	want := binary.BigEndian.Uint32(buf[total-crcSize : total])
	got := crc32.ChecksumIEEE(buf[:total-crcSize])
	if want != got {
		return nil, fmt.Errorf("%w: crc mismatch (want %08x, got %08x)", ErrBadData, want, got)
	}
	f := &Frame{
		Seq: binary.BigEndian.Uint64(buf[4:12]),
	}
	copy(f.Prev[:], buf[12:12+DigestSize])
	f.Payload = append([]byte(nil), buf[headerSize:total-crcSize]...)
	return f, nil
}
