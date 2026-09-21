// Package chain implements a framed, chained byte-stream protocol.
//
// Wire layout of one frame (all multi-byte integers are big-endian):
//
//	+--------+-----+------------+--------+---------+-----+
//	| magic  | seq | prevDigest | length | payload | crc |
//	| 2 byte |  8  |    32      |   4    | length  |  4  |
//	+--------+-----+------------+--------+---------+-----+
//
// magic is 0xCF 0xC7. crc is CRC-32/IEEE over seq || prevDigest ||
// length || payload, i.e. every byte of the frame after magic.
//
// prevDigest is the SHA-256 digest of the previous frame in the chain.
// The first frame of a fresh stream links against the zero digest.
package chain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	// Magic0 Magic1 are the two leading synchronization bytes.
	Magic0 = 0xCF
	Magic1 = 0xC7

	magicSize    = 2
	seqSize      = 8
	digestSize   = sha256.Size
	lengthSize   = 4
	crcSize      = 4
	headerFields = seqSize + digestSize + lengthSize

	// HeaderLen is the fixed header size (magic through length).
	HeaderLen = magicSize + headerFields
	// TrailerLen is the fixed trailer size (CRC).
	TrailerLen = crcSize
	// MinFrameLen is the size of a frame with an empty payload.
	MinFrameLen = HeaderLen + TrailerLen

	// MaxPayload is the accepted per-frame payload quota.
	MaxPayload = 1 << 20
)

var crcTable = crc32.MakeTable(crc32.IEEE)

// Frame is one decoded protocol frame.
type Frame struct {
	Seq        uint64
	PrevDigest []byte // len 32
	Payload    []byte

	// digest is this frame's own digest, populated by DecodeFrame.
	digest []byte
}

// ZeroDigest is the digest the first frame links against.
func ZeroDigest() []byte {
	return make([]byte, digestSize)
}

// Digest returns the SHA-256 digest of the frame's wire bytes.
func (f Frame) Digest() []byte {
	sum := sha256.Sum256(f.signedBytes())
	return sum[:]
}

// signedBytes returns the bytes covered by the CRC (everything after magic).
func (f *Frame) signedBytes() []byte {
	buf := make([]byte, headerFields+len(f.Payload))
	binary.BigEndian.PutUint64(buf, f.Seq)
	copy(buf[seqSize:seqSize+digestSize], f.PrevDigest)
	binary.BigEndian.PutUint32(buf[seqSize+digestSize:], uint32(len(f.Payload)))
	copy(buf[headerFields:], f.Payload)
	return buf
}

// Encode serializes the frame. PrevDigest must be nil or 32 bytes;
// nil is treated as the zero digest.
func (f Frame) Encode() []byte {
	prev := f.PrevDigest
	if prev == nil {
		prev = make([]byte, digestSize)
	}
	if len(prev) != digestSize {
		panic("chain: PrevDigest must be 32 bytes")
	}

	total := HeaderLen + len(f.Payload) + crcSize
	out := make([]byte, total)
	out[0] = Magic0
	out[1] = Magic1
	binary.BigEndian.PutUint64(out[magicSize:], f.Seq)
	copy(out[magicSize+seqSize:magicSize+seqSize+digestSize], prev)
	binary.BigEndian.PutUint32(out[magicSize+seqSize+digestSize:HeaderLen], uint32(len(f.Payload)))
	copy(out[HeaderLen:HeaderLen+len(f.Payload)], f.Payload)

	signedStart := magicSize
	signedEnd := HeaderLen + len(f.Payload)
	csum := crc32.Checksum(out[signedStart:signedEnd], crcTable)
	binary.BigEndian.PutUint32(out[signedEnd:], csum)
	return out
}

// IncompleteError reports that the buffer is missing bytes for a frame.
// It satisfies errors.Is(err, ErrNeedMoreData).
type IncompleteError struct {
	Have int // bytes currently available
	Need int // total bytes the frame header promises (0 if unknown yet)
}

func (e IncompleteError) Error() string {
	if e.Need > 0 {
		return "chain: incomplete frame: have " + itoa(e.Have) + ", need " + itoa(e.Need)
	}
	return "chain: incomplete frame: have " + itoa(e.Have)
}

func (IncompleteError) Is(target error) bool { return target == ErrNeedMoreData }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

var (
	// ErrNeedMoreData: more bytes are required; not bad data.
	ErrNeedMoreData = errors.New("chain: need more data")
	// ErrBadMagic: stream does not begin with the frame magic.
	ErrBadMagic = errors.New("chain: bad magic")
	// ErrPayloadTooLarge: declared length exceeds the configured quota.
	ErrPayloadTooLarge = errors.New("chain: payload length exceeds quota")
	// ErrCRC: frame checksum mismatch.
	ErrCRC = errors.New("chain: crc mismatch")
)

// DecodeFrame decodes one frame from the front of b.
//
// It returns the frame, the number of bytes consumed, and an error.
// ErrNeedMoreData (via IncompleteError) means the frame is simply not
// fully present yet; the caller should append more bytes and retry.
// Every other error is permanent bad data.
func DecodeFrame(b []byte, maxPayload int) (Frame, int, error) {
	if len(b) < magicSize {
		return Frame{}, 0, IncompleteError{Have: len(b)}
	}
	if b[0] != Magic0 || b[1] != Magic1 {
		return Frame{}, 0, ErrBadMagic
	}
	if len(b) < HeaderLen {
		return Frame{}, 0, IncompleteError{Have: len(b)}
	}

	seq := binary.BigEndian.Uint64(b[magicSize:])
	prev := make([]byte, digestSize)
	copy(prev, b[magicSize+seqSize:magicSize+seqSize+digestSize])
	length := int(binary.BigEndian.Uint32(b[magicSize+seqSize+digestSize : HeaderLen]))

	// Enforce the length quota BEFORE allocating the payload buffer.
	if length > maxPayload {
		return Frame{}, 0, ErrPayloadTooLarge
	}

	total := HeaderLen + length + crcSize
	if len(b) < total {
		return Frame{}, 0, IncompleteError{Have: len(b), Need: total}
	}

	payload := b[HeaderLen : HeaderLen+length]
	gotCRC := binary.BigEndian.Uint32(b[HeaderLen+length:])
	if wantCRC := crc32.Checksum(b[magicSize:HeaderLen+length], crcTable); gotCRC != wantCRC {
		return Frame{}, 0, ErrCRC
	}

	// Copy so the caller may reuse or release b.
	payloadCopy := make([]byte, length)
	copy(payloadCopy, payload)

	f := Frame{Seq: seq, PrevDigest: prev, Payload: payloadCopy}
	f.digest = f.Digest()
	return f, total, nil
}

// wireDigest returns the digest populated by DecodeFrame, recomputing it
// if the frame was constructed by hand.
func (f Frame) wireDigest() []byte {
	if f.digest != nil {
		return f.digest
	}
	return f.Digest()
}
